package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"
	upstream "github.com/TokenFlux/TokenRouter/internal/upstream"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"

	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const gatewayCompatibilityMetricsLogInterval = 1024

var gatewayCompatibilityMetricsLogCounter atomic.Uint64

// GatewayHandler handles API gateway requests
type GatewayHandler struct {
	runtimeSettings           *gateway.RuntimeSettings
	balanceUnit               usagehttp.BalanceUnitReader
	completionRecorder        *completion.Recorder
	gatewayService            *service.GatewayService
	openAIGatewayService      *service.OpenAIGatewayService
	geminiCompatService       *service.GeminiMessagesCompatService
	antigravityGatewayService *service.AntigravityGatewayService
	userService               *identity.UserService
	billingCacheService       *admission.FundingAdmission
	usageService              *usage.UsageService
	apiKeyService             *apikey.APIKeyService
	usageRecordWorkerPool     *completion.UsageRecordWorkerPool
	errorPassthroughService   *errorpolicy.ErrorPassthroughService
	contentModerationService  *moderationcore.ContentModerationService
	concurrencyHelper         *gatewayhttp.ConcurrencyHelper
	userMsgQueueHelper        *UserMsgQueueHelper
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
	userService *identity.UserService,
	concurrencyService *scheduler.ConcurrencyService,
	billingCacheService *admission.FundingAdmission,
	usageService *usage.UsageService,
	apiKeyService *apikey.APIKeyService,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	errorPassthroughService *errorpolicy.ErrorPassthroughService,
	contentModerationService *moderationcore.ContentModerationService,
	userMsgQueueService *scheduler.UserMessageQueueService,
	cfg *config.Config,
	runtimeSettings *gateway.RuntimeSettings,
	balanceUnit usagehttp.BalanceUnitReader,
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
	var umqHelper *UserMsgQueueHelper
	if userMsgQueueService != nil && cfg != nil {
		umqHelper = NewUserMsgQueueHelper(userMsgQueueService, gatewayhttp.SSEPingFormatClaude, pingInterval)
	}

	return &GatewayHandler{
		runtimeSettings: runtimeSettings, balanceUnit: balanceUnit,
		gatewayService:            gatewayService,
		openAIGatewayService:      openAIGatewayService,
		geminiCompatService:       geminiCompatService,
		antigravityGatewayService: antigravityGatewayService,
		userService:               userService,
		billingCacheService:       billingCacheService,
		usageService:              usageService,
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

// Models 处理 GET /v1/models，并返回通过账号能力与渠道规则校验的客户端模型。
// 仅未绑定分组的兼容调用会在没有显式结果时回退平台默认模型。
func (h *GatewayHandler) Models(c *gin.Context) { h.NewModelsHTTPHandler().Models(c) }

func filterModelsByCustomList(availableModels, fallbackModels, selectedModels []string) []string {
	return gatewayhttp.FilterModelsByCustomList(availableModels, fallbackModels, selectedModels)
}

func defaultModelIDsForPlatform(platform string) []string {
	return newModelDisplayHandler().DefaultModelIDsForPlatform(platform)
}

// AntigravityModels 返回 Antigravity 支持的全部模型
// GET /antigravity/models
func (h *GatewayHandler) AntigravityModels(c *gin.Context) {
	h.NewModelsHTTPHandler().AntigravityModels(c)
}

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

// prepareGatewayAttemptRequest 按当前 API Key 分组克隆请求并执行渠道模型映射。
// 每次账号尝试都重新解析当前分组，确保兜底分组不会沿用原分组的映射和用量字段。
func (h *GatewayHandler) prepareGatewayAttemptRequest(
	ctx context.Context,
	parsed *requeststate.ParsedRequest,
	body []byte,
	apiKey *apikey.APIKey,
	requestedModel string,
) (*requeststate.ParsedRequest, routing.ChannelMappingResult, error) {
	attempt, err := parsed.CloneForBody(body)
	if err != nil {
		return nil, routing.ChannelMappingResult{}, err
	}

	var groupID *int64
	if apiKey != nil && apiKey.GroupID != nil {
		value := *apiKey.GroupID
		groupID = &value
	}
	attempt.GroupID = groupID
	// 当前分组和渠道结果进入独立计划，不改变原解析位置。
	mappingRoutePlan := h.gatewayService.PlanRoute(ctx, service.APIKeyRouteGroup(apiKey), groupID, requestedModel)
	mapping := service.ChannelMappingFromRoutePlan(mappingRoutePlan)
	if !mapping.Mapped {
		return attempt, mapping, nil
	}

	attempt.Model = mapping.MappedModel
	if err := attempt.ReplaceBody(h.gatewayService.ReplaceModelInBody(attempt.Body.Bytes(), mapping.MappedModel)); err != nil {
		return nil, routing.ChannelMappingResult{}, err
	}
	return attempt, mapping, nil
}

func (h *GatewayHandler) Usage(c *gin.Context) { h.publicUsageHTTP().Usage(c) }

func (h *GatewayHandler) usageUnrestricted(c *gin.Context, ctx context.Context, apiKey *apikey.APIKey, subject middleware2.AuthSubject, usageData gin.H, dailyUsage any, modelStats any, balanceUnitName string) {
	h.publicUsageHTTP().UsageUnrestricted(c, ctx, apikey.CopyAPIKey(apiKey), subject, usageData, dailyUsage, modelStats, balanceUnitName)
}

// handleConcurrencyError 统一处理并发槽位获取失败。
func (h *GatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := gatewayhttp.ConcurrencyErrorResponse(err, slotType)
	h.handleStreamingAwareErrorWithCode(c, status, errType, code, message, streamStarted)
}

func (h *GatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, platform string, streamStarted bool) {
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody
	if service.IsOpenAISilentRefusalErrorBody(responseBody) {
		gatewayhttp.SetOpsUpstreamError(c, statusCode, service.OpenAISilentRefusalClientMessage(), "")
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", service.OpenAISilentRefusalClientMessage(), streamStarted)
		return
	}

	// 先检查透传规则
	if h.errorPassthroughService != nil && len(responseBody) > 0 {
		if rule := h.errorPassthroughService.MatchRule(platform, statusCode, responseBody); rule != nil {
			// 确定响应状态码
			respCode := statusCode
			if !rule.PassthroughCode && rule.ResponseCode != nil {
				respCode = *rule.ResponseCode
			}

			// 确定响应消息
			msg := upstream.ExtractErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				c.Set(gatewayhttp.OpsSkipPassthroughKey, true)
			}

			h.handleStreamingAwareError(c, respCode, "upstream_error", msg, streamStarted)
			return
		}
	}

	// 记录原始上游状态码，以便 ops 错误日志捕获真实的上游错误
	upstreamMsg := upstream.ExtractErrorMessage(responseBody)
	gatewayhttp.SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	// 使用默认的错误映射
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *GatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	gatewayhttp.SetOpsUpstreamError(c, statusCode, errMsg, "")
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func (h *GatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "overloaded_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
}

// handleStreamingAwareError handles errors that may occur after streaming has started
func (h *GatewayHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.handleStreamingAwareErrorWithCode(c, status, errType, "", message, streamStarted)
}

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func (h *GatewayHandler) handleStreamingAwareErrorWithCode(c *gin.Context, status int, errType, code, message string, streamStarted bool) {
	gatewayhttp.WriteAnthropicStreamError(c, status, errType, code, message, streamStarted, gatewayhttp.MarkOpsStreamError)
}

// ensureForwardErrorResponse 在 Forward 返回错误但尚未写响应时补写统一错误响应。
// Writer 已被写过时（ping 已 flush）走 streamStarted 分支，
// 让 handleStreamingAwareError 通过 SSE 发协议合规的终止事件，
// 否则下游收到的就是 silent EOF。
func (h *GatewayHandler) ensureForwardErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if gatewayhttp.IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Written() {
		streamStarted = true
	}
	h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", "Upstream request failed", streamStarted)
	return true
}

// gatewayForwardErrorAlreadyCommunicated 判断 Forward 实现返回错误前是否已经
// 向客户端写出了完整错误响应。
//
// 该判断有意比“writer size 变化”更窄：流式响应可能只发过保活 ping 或部分数据，
// 此时 handler 仍需要追加协议级终止错误。Forward 写出的非 SSE 响应不同：
// service 层辅助函数已经写出客户端可见的 JSON 响应体，再追加通用流式兜底会污染响应。
func gatewayForwardErrorAlreadyCommunicated(c *gin.Context, writerSizeBeforeForward int, err error) bool {
	if err == nil || c == nil || c.Writer == nil {
		return false
	}
	if c.Writer.Size() == writerSizeBeforeForward {
		return false
	}

	contentType := strings.ToLower(strings.TrimSpace(c.Writer.Header().Get("Content-Type")))
	if contentType == "" {
		return false
	}
	return !strings.Contains(contentType, "text/event-stream")
}

// errorResponse 返回Claude API格式的错误响应
func (h *GatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	h.errorResponseWithCode(c, status, errType, "", message)
}

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func (h *GatewayHandler) errorResponseWithCode(c *gin.Context, status int, errType, code, message string) {
	gatewayhttp.WriteAnthropicError(c, status, errType, code, message)
}

// CountTokens 委托唯一无槽计数入口。
func (h *GatewayHandler) CountTokens(c *gin.Context) { h.NewCountTokensHTTPHandler().CountTokens(c) }

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

func (h *GatewayHandler) maybeLogCompatibilityFallbackMetrics(reqLog *zap.Logger) {
	if reqLog == nil {
		return
	}
	if gatewayCompatibilityMetricsLogCounter.Add(1)%gatewayCompatibilityMetricsLogInterval != 0 {
		return
	}
	metrics := service.SnapshotOpenAICompatibilityFallbackMetrics()
	reqLog.Info("gateway.compatibility_fallback_metrics",
		zap.Int64("session_hash_legacy_read_fallback_total", metrics.SessionHashLegacyReadFallbackTotal),
		zap.Int64("session_hash_legacy_read_fallback_hit", metrics.SessionHashLegacyReadFallbackHit),
		zap.Int64("session_hash_legacy_dual_write_total", metrics.SessionHashLegacyDualWriteTotal),
		zap.Float64("session_hash_legacy_read_hit_rate", metrics.SessionHashLegacyReadHitRate),
		zap.Int64("metadata_legacy_fallback_total", metrics.MetadataLegacyFallbackTotal),
	)
}

// getUserMsgQueueMode 获取当前请求的 UMQ 模式
// 返回 "serialize" | "throttle" | ""
func (h *GatewayHandler) getUserMsgQueueMode(account *service.Account, parsed *requeststate.ParsedRequest) string {
	if h.userMsgQueueHelper == nil {
		return ""
	}
	// 仅适用于 Anthropic OAuth/SetupToken 账号
	if !account.IsAnthropicOAuthOrSetupToken() {
		return ""
	}
	if !service.IsRealUserMessage(parsed) {
		return ""
	}
	// 账号级模式优先，fallback 到全局配置
	mode := account.GetUserMsgQueueMode()
	if mode == "" {
		mode = h.cfg.Gateway.UserMessageQueue.GetEffectiveMode()
	}
	return mode
}
