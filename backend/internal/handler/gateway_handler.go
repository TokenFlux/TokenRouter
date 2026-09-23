package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
	"go.uber.org/zap"

	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
)

// GatewayHandler handles API gateway requests
type GatewayHandler struct {
	// prompts 引用 app 注入的唯一提示词规则缓存。
	prompts                   *promptpolicy.Service
	runtimeSettings           *gateway.RuntimeSettings
	completionRecorder        *completion.Recorder
	gatewayService            *service.GatewayService
	openAIGatewayService      *service.OpenAIGatewayService
	geminiCompatService       *service.GeminiMessagesCompatService
	antigravityGatewayService *service.AntigravityGatewayService
	billingCacheService       *admission.FundingAdmission
	apiKeyService             *apikey.APIKeyService
	usageRecordWorkerPool     *completion.UsageRecordWorkerPool
	errorPassthroughService   *errorpolicy.ErrorPassthroughService
	contentModerationService  *moderationcore.ContentModerationService
	concurrencyHelper         *gatewayhttp.ConcurrencyHelper
	userMsgQueueHelper        *gatewayhttp.UserMsgQueueHelper
	maxAccountSwitches        int
	maxAccountSwitchesGemini  int
	cfg                       *config.Config
}

// NewGatewayHandler creates a new GatewayHandler
func NewGatewayHandler(
	gatewayService *service.GatewayService,
	openAIGatewayService *service.OpenAIGatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	antigravityGatewayService *service.AntigravityGatewayService,

	concurrencyService *scheduler.ConcurrencyService,
	billingCacheService *admission.FundingAdmission,

	apiKeyService *apikey.APIKeyService,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	errorPassthroughService *errorpolicy.ErrorPassthroughService,
	contentModerationService *moderationcore.ContentModerationService,
	userMsgQueueService *scheduler.UserMessageQueueService,
	cfg *config.Config,
	runtimeSettings *gateway.RuntimeSettings,
	prompts *promptpolicy.Service,

) *GatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 10
	maxAccountSwitchesGemini := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
		if cfg.Gateway.MaxAccountSwitchesGemini > 0 {
			maxAccountSwitchesGemini = cfg.Gateway.MaxAccountSwitchesGemini
		}
	}

	// 初始化用户消息串行队列 helper
	var umqHelper *gatewayhttp.UserMsgQueueHelper
	if userMsgQueueService != nil && cfg != nil {
		umqHelper = gatewayhttp.NewUserMsgQueueHelper(userMsgQueueService, gatewayhttp.SSEPingFormatClaude, pingInterval)
	}

	return &GatewayHandler{
		runtimeSettings:           runtimeSettings,
		prompts:                   prompts,
		gatewayService:            gatewayService,
		openAIGatewayService:      openAIGatewayService,
		geminiCompatService:       geminiCompatService,
		antigravityGatewayService: antigravityGatewayService,
		billingCacheService:       billingCacheService,
		apiKeyService:             apiKeyService,
		usageRecordWorkerPool:     usageRecordWorkerPool,
		errorPassthroughService:   errorPassthroughService,
		contentModerationService:  contentModerationService,
		concurrencyHelper:         gatewayhttp.NewConcurrencyHelper(concurrencyService, gatewayhttp.SSEPingFormatClaude, pingInterval),
		userMsgQueueHelper:        umqHelper,
		maxAccountSwitches:        maxAccountSwitches,
		maxAccountSwitchesGemini:  maxAccountSwitchesGemini,
		cfg:                       cfg,
	}
}

// Messages 兼容入口委托目标 HTTP 适配器，生产路由由 app 直接绑定。
func (h *GatewayHandler) Messages(c *gin.Context) { h.NewMessagesHTTPHandler().Messages(c) }

func cloneAPIKeyWithGroup(apiKey *apikey.APIKey, group *routing.Group) *apikey.APIKey {
	if apiKey == nil || group == nil {
		return apiKey
	}
	cloned := *apiKey
	groupID := group.ID
	cloned.GroupID = &groupID
	cloned.Group = group
	return &cloned
}

// 兼容类型指向唯一的客户端识别值。
type InterceptType = clientmeta.InterceptType

const (
	InterceptTypeNone              = clientmeta.InterceptTypeNone
	InterceptTypeWarmup            = clientmeta.InterceptTypeWarmup
	InterceptTypeSuggestionMode    = clientmeta.InterceptTypeSuggestionMode
	InterceptTypeMaxTokensOneHaiku = clientmeta.InterceptTypeMaxTokensOneHaiku
)

func detectInterceptType(body []byte, model string, maxTokens int, isClaudeCodeClient bool) InterceptType {
	return clientmeta.DetectInterceptType(body, model, maxTokens, isClaudeCodeClient)
}

func sendMockInterceptStream(c *gin.Context, model string, interceptType InterceptType) {
	gatewayhttp.WriteInterceptStream(c, model, interceptType)
}

func sendMockInterceptResponse(c *gin.Context, model string, interceptType InterceptType) {
	gatewayhttp.WriteInterceptResponse(c, model, interceptType)
}

// getUserMsgQueueMode 获取当前请求的 UMQ 模式
// 返回 "serialize" | "throttle" | ""
func (h *GatewayHandler) getUserMsgQueueMode(account *gatewayprovider.ExecutionAccount, parsed *requeststate.ParsedRequest) string {
	if h.userMsgQueueHelper == nil {
		return ""
	}
	// 仅适用于 Anthropic OAuth/SetupToken 账号
	if !account.View().IsAnthropicOAuthOrSetupToken() {
		return ""
	}
	if !requeststate.IsRealUserMessage(parsed) {
		return ""
	}
	// 账号级模式优先，fallback 到全局配置
	mode := gatewayprovider.ExecutionRuntimeConfig(account).GetUserMsgQueueMode()
	if mode == "" {
		mode = h.cfg.Gateway.UserMessageQueue.GetEffectiveMode()
	}
	return mode
}

// prepareGatewayAttemptRequest 只绑定原渠道解析端口；请求克隆与改写由 HTTP 原生实现拥有。
func (h *GatewayHandler) prepareGatewayAttemptRequest(ctx context.Context, parsed *requeststate.ParsedRequest, body []byte, key *apikey.APIKey, model string) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error) {
	return gatewayhttp.PrepareChannelAttempt(ctx, parsed, body, key, model, func(ctx context.Context, key *apikey.APIKey, model string) routing.RoutePlan {
		var id *int64
		if key != nil {
			id = key.GroupID
		}
		return h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(key), id, model)
	})
}

// maybeLogCompatibilityFallbackMetrics 复用计数入口与文本入口的同一个采样计数器。
func (h *GatewayHandler) maybeLogCompatibilityFallbackMetrics(log *zap.Logger) {
	gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
		value := h.openAIGatewayService.SnapshotOpenAICompatibilityFallbackMetrics()
		return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: value.SessionHashLegacyReadFallbackTotal, ReadHit: value.SessionHashLegacyReadFallbackHit, DualWrite: value.SessionHashLegacyDualWriteTotal, ReadHitRate: value.SessionHashLegacyReadHitRate, MetadataTotal: value.MetadataLegacyFallbackTotal}
	})
}
