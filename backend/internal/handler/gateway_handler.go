package handler

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"github.com/TokenFlux/TokenRouter/internal/config"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"

	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

const gatewayCompatibilityMetricsLogInterval = 1024

var gatewayCompatibilityMetricsLogCounter atomic.Uint64

// GatewayHandler handles API gateway requests
type GatewayHandler struct {
	completionRecorder        *completion.Recorder
	gatewayService            *service.GatewayService
	openAIGatewayService      *service.OpenAIGatewayService
	geminiCompatService       *service.GeminiMessagesCompatService
	antigravityGatewayService *service.AntigravityGatewayService
	userService               *service.UserService
	billingCacheService       *service.BillingCacheService
	usageService              *service.UsageService
	apiKeyService             *service.APIKeyService
	usageRecordWorkerPool     *service.UsageRecordWorkerPool
	errorPassthroughService   *service.ErrorPassthroughService
	contentModerationService  *service.ContentModerationService
	concurrencyHelper         *ConcurrencyHelper
	userMsgQueueHelper        *UserMsgQueueHelper
	maxAccountSwitches        int
	maxAccountSwitchesGemini  int
	cfg                       *config.Config
	settingService            *service.SettingService
}

// NewGatewayHandler creates a new GatewayHandler
func NewGatewayHandler(
	gatewayService *service.GatewayService,
	openAIGatewayService *service.OpenAIGatewayService,
	geminiCompatService *service.GeminiMessagesCompatService,
	antigravityGatewayService *service.AntigravityGatewayService,
	userService *service.UserService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	usageService *service.UsageService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	errorPassthroughService *service.ErrorPassthroughService,
	contentModerationService *service.ContentModerationService,
	userMsgQueueService *service.UserMessageQueueService,
	cfg *config.Config,
	settingService *service.SettingService,
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
		umqHelper = NewUserMsgQueueHelper(userMsgQueueService, SSEPingFormatClaude, pingInterval)
	}

	return &GatewayHandler{
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
		concurrencyHelper:         NewConcurrencyHelper(concurrencyService, SSEPingFormatClaude, pingInterval),
		userMsgQueueHelper:        umqHelper,
		maxAccountSwitches:        maxAccountSwitches,
		maxAccountSwitchesGemini:  maxAccountSwitchesGemini,
		cfg:                       cfg,
		settingService:            settingService,
	}
}

// Messages 兼容入口委托目标 HTTP 适配器，生产路由由 app 直接绑定。
func (h *GatewayHandler) Messages(c *gin.Context) { h.NewMessagesHTTPHandler().Messages(c) }

// Models 处理 GET /v1/models，并返回通过账号能力与渠道规则校验的客户端模型。
// 仅未绑定分组的兼容调用会在没有显式结果时回退平台默认模型。
func (h *GatewayHandler) Models(c *gin.Context) { h.NewModelsHTTPHandler().Models(c) }

// compositePreferredSubscription 返回复合 Key 严格指定套餐的认证快照。
// 快照缺失时采用拒绝展示的策略，避免任意模型列表入口泄露套餐外映射。
func compositePreferredSubscription(c *gin.Context, apiKey *service.APIKey) (*service.UserSubscription, bool) {
	return (&GatewayHandler{}).NewModelsHTTPHandler().CompositePreferredSubscription(c, service.APIKeyView(apiKey))
}

// compositeGroupAvailableToUser 使用认证快照过滤已停用、已撤销授权或套餐外的复合映射。
func compositeGroupAvailableToUser(apiKey *service.APIKey, preferredSubscription *service.UserSubscription, group *service.Group) bool {
	return gatewayhttp.CompositeGroupAvailableToUser(service.APIKeyView(apiKey), preferredSubscription, service.APIKeyGroupView(group))
}

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

func cloneAPIKeyWithGroup(apiKey *service.APIKey, group *service.Group) *service.APIKey {
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
	parsed *service.ParsedRequest,
	body []byte,
	apiKey *service.APIKey,
	requestedModel string,
) (*service.ParsedRequest, service.ChannelMappingResult, error) {
	attempt, err := parsed.CloneForBody(body)
	if err != nil {
		return nil, service.ChannelMappingResult{}, err
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
		return nil, service.ChannelMappingResult{}, err
	}
	return attempt, mapping, nil
}

func (h *GatewayHandler) Usage(c *gin.Context) { h.publicUsageHTTP().Usage(c) }

func (h *GatewayHandler) usageUnrestricted(c *gin.Context, ctx context.Context, apiKey *service.APIKey, subject middleware2.AuthSubject, usageData gin.H, dailyUsage any, modelStats any, balanceUnitName string) {
	h.publicUsageHTTP().UsageUnrestricted(c, ctx, service.APIKeyView(apiKey), subject, usageData, dailyUsage, modelStats, balanceUnitName)
}

// handleConcurrencyError 统一处理并发槽位获取失败。
func (h *GatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := concurrencyErrorResponse(err, slotType)
	h.handleStreamingAwareErrorWithCode(c, status, errType, code, message, streamStarted)
}

func (h *GatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, platform string, streamStarted bool) {
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody
	if service.IsOpenAISilentRefusalErrorBody(responseBody) {
		service.SetOpsUpstreamError(c, statusCode, service.OpenAISilentRefusalClientMessage(), "")
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
			msg := service.ExtractUpstreamErrorMessage(responseBody)
			if !rule.PassthroughBody && rule.CustomMessage != nil {
				msg = *rule.CustomMessage
			}

			if rule.SkipMonitoring {
				c.Set(service.OpsSkipPassthroughKey, true)
			}

			h.handleStreamingAwareError(c, respCode, "upstream_error", msg, streamStarted)
			return
		}
	}

	// 记录原始上游状态码，以便 ops 错误日志捕获真实的上游错误
	upstreamMsg := service.ExtractUpstreamErrorMessage(responseBody)
	service.SetOpsUpstreamError(c, statusCode, upstreamMsg, "")

	// 使用默认的错误映射
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *GatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	service.SetOpsUpstreamError(c, statusCode, errMsg, "")
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
	gatewayhttp.WriteAnthropicStreamError(c, status, errType, code, message, streamStarted, service.MarkOpsStreamError)
}

// ensureForwardErrorResponse 在 Forward 返回错误但尚未写响应时补写统一错误响应。
// Writer 已被写过时（ping 已 flush）走 streamStarted 分支，
// 让 handleStreamingAwareError 通过 SSE 发协议合规的终止事件，
// 否则下游收到的就是 silent EOF。
func (h *GatewayHandler) ensureForwardErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if service.IsResponseCommitted(c) {
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

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func extractQuotaResetSeconds(err error) int { return gatewayhttp.ExtractQuotaResetSeconds(err) }

// 旧文本输出委托 gateway/httpapi 的唯一实现。
func billingErrorDetails(err error) (status int, code, message string, retryAfter int) {
	return gatewayhttp.BillingErrorDetails(err)
}

func (h *GatewayHandler) metadataBridgeEnabled() bool {
	if h == nil || h.cfg == nil {
		return true
	}
	return h.cfg.Gateway.OpenAIWS.MetadataBridgeEnabled
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

func (h *GatewayHandler) submitUsageRecordTask(c *gin.Context, task service.UsageRecordTask) {
	if task == nil {
		return
	}
	task = wrapUsageRecordTaskContext(c, task)
	if h.usageRecordWorkerPool != nil {
		if mode := h.usageRecordWorkerPool.Submit(task); mode != service.UsageRecordSubmitModeDroppedStopped {
			return
		}
		// 池已停止时处于进程关停窗口，计费任务不能静默丢失。
		// 显式 drop/sample 溢出仍保持运维配置的取舍。
		logger.L().With(
			zap.String("component", "handler.gateway.messages"),
		).Warn("gateway.usage_record_task_stopped_sync_fallback")
	}
	// 回退路径：worker 池未注入或已停止时同步执行，避免退回到无界 goroutine 模式。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.messages"),
				zap.Any("panic", recovered),
			).Error("gateway.usage_record_task_panic_recovered")
		}
	}()
	task(ctx)
}

// submitMandatoryUsageRecordTask 在工作池溢出时同步回退，不能静默丢弃结算任务。
func (h *GatewayHandler) submitMandatoryUsageRecordTask(c *gin.Context, task service.UsageRecordTask) {
	if task == nil {
		return
	}
	task = wrapUsageRecordTaskContext(c, task)
	if h.usageRecordWorkerPool != nil {
		if mode := h.usageRecordWorkerPool.Submit(task); !mode.Dropped() {
			return
		}
		logger.L().With(
			zap.String("component", "handler.gateway.usage"),
		).Warn("gateway.usage_record_task_mandatory_sync_fallback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().With(
				zap.String("component", "handler.gateway.usage"),
				zap.Any("panic", recovered),
			).Error("gateway.usage_record_task_panic_recovered")
		}
	}()
	task(ctx)
}

// getUserMsgQueueMode 获取当前请求的 UMQ 模式
// 返回 "serialize" | "throttle" | ""
func (h *GatewayHandler) getUserMsgQueueMode(account *service.Account, parsed *service.ParsedRequest) string {
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
