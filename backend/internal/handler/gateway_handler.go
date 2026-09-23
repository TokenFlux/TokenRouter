package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"

	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
	"go.uber.org/zap"

	"time"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

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

// maybeLogCompatibilityFallbackMetrics 复用计数入口与文本入口的同一个采样计数器。
func (h *GatewayHandler) maybeLogCompatibilityFallbackMetrics(log *zap.Logger) {
	gatewayhttp.LogCompatibilityFallback(log, func() gatewayhttp.CompatibilityLogSnapshot {
		value := h.openAIGatewayService.SnapshotOpenAICompatibilityFallbackMetrics()
		return gatewayhttp.CompatibilityLogSnapshot{ReadTotal: value.SessionHashLegacyReadFallbackTotal, ReadHit: value.SessionHashLegacyReadFallbackHit, DualWrite: value.SessionHashLegacyDualWriteTotal, ReadHitRate: value.SessionHashLegacyReadHitRate, MetadataTotal: value.MetadataLegacyFallbackTotal}
	})
}
