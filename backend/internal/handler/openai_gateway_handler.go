package handler

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"

	openai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"context"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	openaierrors "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"

	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// OpenAIGatewayHandler handles OpenAI API gateway requests
type OpenAIGatewayHandler struct {
	// prompts 引用 app 注入的唯一提示词规则缓存。
	prompts                    *promptpolicy.Service
	opsErrorQueue              gatewayhttp.OpsErrorLogQueue
	cyberHTTP                  *gatewayhttp.CyberHandler
	completionRecorder         *completion.Recorder
	gatewayService             *service.OpenAIGatewayService
	billingCacheService        *admission.FundingAdmission
	apiKeyService              *apikey.APIKeyService
	usageRecordWorkerPool      *completion.UsageRecordWorkerPool
	errorPassthroughService    *errorpolicy.ErrorPassthroughService
	contentModerationService   *moderationcore.ContentModerationService
	grokMediaEligibilityProber grokMediaEligibilityProber
	opsService                 *ops.OpsService
	concurrencyHelper          *gatewayhttp.ConcurrencyHelper
	imageLimiter               *scheduler.ImageConcurrencyLimiter
	maxAccountSwitches         int
	cfg                        *config.Config
}

// grokMediaEligibilityProber 在首次媒体转发前补齐 OAuth 账号的计费观测。
type grokMediaEligibilityProber interface {
	ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error)
}

// openAIForwardSucceededForScheduling 会排除以失败事件结束的 WebSocket 转发结果。
func openAIForwardSucceededForScheduling(result *forwardcore.OpenAIResult) bool {
	return result.SucceededForScheduling()
}

func openAIAccountScheduleModel(c *gin.Context, account *gatewayprovider.ExecutionAccount, forwardModel string, requireCompact bool, result *forwardcore.OpenAIResult) string {
	if result != nil {
		if actual := strings.TrimSpace(result.UpstreamModel); actual != "" {
			return actual
		}
	}
	if c != nil {
		if value, ok := c.Get(gatewayhttp.OpsUpstreamModelKey); ok {
			if actual, ok := value.(string); ok && strings.TrimSpace(actual) != "" {
				return strings.TrimSpace(actual)
			}
		}
	}
	return gatewayprovider.ExecutionModelPolicy(account).OpenAIUpstream(forwardModel, requireCompact, false)
}

type openAIModelBodyReplaceFunc func([]byte, string) []byte

func openAIChannelMappedModel(requestedModel string, mapping routing.ChannelMappingResult) string {
	return requeststate.ChannelMappedModel(requestedModel, routing.ChannelMappingResult(mapping))
}

func openAIModelMappedBody(body []byte, mapped bool, mappedModel string, replace openAIModelBodyReplaceFunc) []byte {
	return requeststate.ModelMappedBody(body, mapped, mappedModel, requeststate.ModelBodyReplacer(replace))
}

// resolveOpenAIChannelMappedImageIntent 先把客户端模型 R 映射为渠道模型 C，
// 再返回映射后的请求体、渠道模型和宽泛意图，供显式门禁与转发提示分别使用。
func resolveOpenAIChannelMappedImageIntent(
	endpoint string,
	requestedModel string,
	body []byte,
	mapping routing.ChannelMappingResult,
	platform string,
	replace openAIModelBodyReplaceFunc,
) ([]byte, string, bool) {
	routingModel := openAIChannelMappedModel(requestedModel, mapping)
	mappedBody := openAIModelMappedBody(body, mapping.Mapped, routingModel, replace)
	imageIntent := gatewayprovider.ImageIntentForPlatform(endpoint, routingModel, mappedBody, platform)
	return mappedBody, routingModel, imageIntent
}

func seedOpenAIForwardImageIntentHint(c *gin.Context, channelMapped bool, imageIntent bool) {
	if channelMapped {
		// 渠道映射改变了规范请求，保持 unknown，由 Forward 按映射后的 model/body 初始化。
		return
	}
	service.SetOpenAIImageIntentHint(c, imageIntent)
}

func newOpenAIModelMappedBodyCache(body []byte, replace openAIModelBodyReplaceFunc) func(bool, string) []byte {
	return requeststate.NewModelMappedBodyCache(body, requeststate.ModelBodyReplacer(replace))
}

// appendOpenAIAccountProxyLogFields 只追加可公开定位代理的字段，避免把代理凭据写入日志。
func appendOpenAIAccountProxyLogFields(fields []zap.Field, account *gatewayprovider.ExecutionAccount) []zap.Field {
	if account == nil {
		return fields
	}
	if account.Record.Proxy != nil {
		return append(fields,
			zap.Int64("proxy_id", account.Record.Proxy.ID),
			zap.String("proxy_name", account.Record.Proxy.Name),
			zap.String("proxy_host", account.Record.Proxy.Host),
			zap.Int("proxy_port", account.Record.Proxy.Port),
		)
	}
	if account.Record.ProxyID != nil {
		return append(fields, zap.Int64p("proxy_id", account.Record.ProxyID))
	}
	return fields
}

// handleOpenAISelectionBusinessError 保持 OpenAI handler 调用侧语义清晰。
func (h *OpenAIGatewayHandler) handleOpenAISelectionBusinessError(c *gin.Context, err error, streamStarted bool) bool {
	return handleGroupSelectionBusinessError(c, err, streamStarted, func(status int, errType string, message string, streamStarted bool) {
		h.handleStreamingAwareError(c, status, errType, message, streamStarted)
	})
}

// allowOpenAICompatibleMessagesDispatch 兼容直接调用 handler 的测试与内部入口。
func allowOpenAICompatibleMessagesDispatch(apiKey *apikey.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages)
}

// NewOpenAIGatewayHandler creates a new OpenAIGatewayHandler
func NewOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	concurrencyService *scheduler.ConcurrencyService,
	billingCacheService *admission.FundingAdmission,
	apiKeyService *apikey.APIKeyService,
	usageRecordWorkerPool *completion.UsageRecordWorkerPool,
	errorPassthroughService *errorpolicy.ErrorPassthroughService,
	contentModerationService *moderationcore.ContentModerationService,
	opsService *ops.OpsService,
	cfg *config.Config,
	prompts *promptpolicy.Service,
) *OpenAIGatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	return &OpenAIGatewayHandler{
		gatewayService:           gatewayService,
		prompts:                  prompts,
		billingCacheService:      billingCacheService,
		apiKeyService:            apiKeyService,
		usageRecordWorkerPool:    usageRecordWorkerPool,
		errorPassthroughService:  errorPassthroughService,
		contentModerationService: contentModerationService,
		opsService:               opsService,
		concurrencyHelper:        gatewayhttp.NewConcurrencyHelper(concurrencyService, gatewayhttp.SSEPingFormatComment, pingInterval),
		imageLimiter:             &scheduler.ImageConcurrencyLimiter{},
		maxAccountSwitches:       maxAccountSwitches,
		cfg:                      cfg,
	}
}

// Responses 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Responses(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Responses(c)
}

// Messages 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Messages(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Messages(c)
}

func resolveOpenAIMessagesMetadataSession(c *gin.Context, sessionHash, promptCacheKey, reqModel string, body []byte) (string, string) {
	return gatewaysession.MessagesMetadataSession(gatewayhttp.ClaudeCodeSessionIDFromHeader(c), sessionHash, promptCacheKey, reqModel, body)
}

func (h *OpenAIGatewayHandler) anthropicStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.NewOpenAITextHTTPHandler().WriteAnthropicStreamingError(c, status, errType, message, streamStarted)
}

// handleAnthropicFailoverExhausted 将上游切号错误转换为 Anthropic 格式。
func (h *OpenAIGatewayHandler) handleAnthropicFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if failoverErr != nil && gatewayprovider.IsOpenAIRequestBodyTooLarge(failoverErr) {
		gatewayhttp.SetOpsUpstreamError(c, http.StatusRequestEntityTooLarge, forwardcore.OpenAIRequestBodyTooLargeClientMessage, "")
		h.anthropicStreamingAwareError(
			c,
			http.StatusRequestEntityTooLarge,
			"invalid_request_error",
			forwardcore.OpenAIRequestBodyTooLargeClientMessage,
			streamStarted,
		)
		return
	}
	if failoverErr != nil {
		copyFailoverRetryAfter(c, failoverErr.ResponseHeaders)
	}
	if failoverErr != nil && failoverErr.IsCredentialFailure() {
		status, message := gatewayhttp.CredentialFailoverClientResponse(failoverErr)
		h.anthropicStreamingAwareError(c, status, "api_error", message, streamStarted)
		return
	}
	if failoverErr != nil && gatewayprovider.IsOpenAICapacityShed(failoverErr) && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		h.anthropicStreamingAwareError(c, status, "api_error", failoverErr.ClientMessage, streamStarted)
		return
	}
	status, errType, errMsg := h.mapUpstreamError(failoverErr.StatusCode)
	h.anthropicStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

// ensureAnthropicErrorResponse writes a fallback Anthropic error if no response was written.
func (h *OpenAIGatewayHandler) ensureAnthropicErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	h.anthropicStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
	return true
}

func (h *OpenAIGatewayHandler) validateFunctionCallOutputRequest(c *gin.Context, body []byte, reqLog *zap.Logger) bool {
	if !gjson.GetBytes(body, `input.#(type=="function_call_output")`).Exists() {
		return true
	}

	validation := openai.ValidateFunctionCallOutputContextBytes(body)
	if !validation.HasFunctionCallOutput {
		return true
	}

	previousResponseID := gjson.GetBytes(body, "previous_response_id").String()
	if strings.TrimSpace(previousResponseID) != "" || validation.HasToolCallContext {
		return true
	}

	if validation.HasFunctionCallOutputMissingCallID {
		reqLog.Warn("openai.request_validation_failed",
			zap.String("reason", "function_call_output_missing_call_id"),
		)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "function_call_output requires call_id on HTTP requests; continuation via previous_response_id is only supported on Responses WebSocket v2")
		return false
	}
	if validation.HasItemReferenceForAllCallIDs {
		return true
	}

	reqLog.Warn("openai.request_validation_failed",
		zap.String("reason", "function_call_output_missing_item_reference"),
	)
	h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "function_call_output requires item_reference ids matching each call_id on HTTP requests; continuation via previous_response_id is only supported on Responses WebSocket v2")
	return false
}

// openAISlotAcquireResult 区分槽位准入成功、已写错误和利润终检否决。
type openAISlotAcquireResult int

const (
	openAISlotAcquireOK openAISlotAcquireResult = iota
	openAISlotAcquireFailed
	openAISlotAcquireProfitVetoed
)

// 兼容调用只委托客户端报文的唯一纯实现。
func normalizeCodexDelegationBootstrap(body []byte) ([]byte, bool) {
	return requeststate.NormalizeCodexDelegationBootstrap(body)
}

// 兼容调用只委托客户端报文的唯一纯实现。
func normalizeCodexAutomationBootstrap(body []byte) ([]byte, bool) {
	return requeststate.NormalizeCodexAutomationBootstrap(body)
}

func (h *OpenAIGatewayHandler) acquireResponsesUserSlot(
	c *gin.Context,
	userID int64,
	userConcurrency int,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
) (func(), bool) {
	ctx := c.Request.Context()
	userReleaseFunc, err := h.concurrencyHelper.AcquireUserSlotWithWait(c, userID, userConcurrency, reqStream, streamStarted)
	if err != nil {
		reqLog.Warn("openai.user_slot_acquire_failed", zap.Error(err))
		h.handleConcurrencyError(c, err, "user", *streamStarted)
		return nil, false
	}
	return scheduler.WrapRelease(ctx, scheduler.ReleaseOnCancel, userReleaseFunc), true
}

func (h *OpenAIGatewayHandler) acquireResponsesAccountSlot(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	selection *gatewayprovider.SelectionResult,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
) (func(), bool) {
	release, result := h.acquireOpenAIAccountSlot(c, groupID, sessionHash, selection, reqStream, streamStarted, reqLog, nil)
	return release, result == openAISlotAcquireOK
}

type openAISlotErrorWriter func(status int, errType, code, message string)

// acquireOpenAIAccountSlot centralizes scheduler selection admission. The
// optional error writer lets non-Responses endpoints retain their wire format
// while sharing the same WaitPlan, cancellation, and release semantics.
func (h *OpenAIGatewayHandler) acquireOpenAIAccountSlot(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	selection *gatewayprovider.SelectionResult,
	reqStream bool,
	streamStarted *bool,
	reqLog *zap.Logger,
	writeError openAISlotErrorWriter,
) (func(), openAISlotAcquireResult) {
	if writeError == nil {
		writeError = func(status int, errType, code, message string) {
			h.handleStreamingAwareErrorWithCode(c, status, errType, code, message, *streamStarted, false)
		}
	}
	var projected *gatewayhttp.SelectedAccountSlot
	if selection != nil && selection.Account != nil {
		projected = &gatewayhttp.SelectedAccountSlot{AccountID: selection.Account.Record.ID, Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc, WaitPlan: selection.WaitPlan}
	}
	release, ok := gatewayhttp.AcquireSelectedAccountSlot(c, groupID, sessionHash, projected, reqStream, streamStarted, reqLog, writeError, h.concurrencyHelper, h.gatewayService, gatewayhttp.AccountSlotHooks{Acquired: gatewayhttp.MarkOpsAccountSlotAcquired, CapacityLimited: gatewayhttp.MarkOpsRoutingCapacityLimited})
	if !ok {
		return release, openAISlotAcquireFailed
	}
	return release, openAISlotAcquireOK
}

// ResponsesWebSocket 委托唯一原生 HTTP 入口及升级后的 WS 编排。
func (h *OpenAIGatewayHandler) ResponsesWebSocket(c *gin.Context) {
	h.NewResponsesWSHTTPHandler().ResponsesWebSocket(c)
}

func (h *OpenAIGatewayHandler) recoverResponsesPanic(c *gin.Context, streamStarted *bool) {
	recovered := recover()
	if recovered == nil {
		return
	}

	started := false
	if streamStarted != nil {
		started = *streamStarted
	}
	wroteFallback := h.ensureForwardErrorResponse(c, started)
	gatewayhttp.RequestLogger(c, "handler.openai_gateway.responses").Error(
		"openai.responses_panic_recovered",
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Any("panic", recovered),
		zap.ByteString("stack", debug.Stack()),
	)
}

func (h *OpenAIGatewayHandler) ensureResponsesDependencies(c *gin.Context, reqLog *zap.Logger) bool {
	missing := h.missingResponsesDependencies()
	if len(missing) == 0 {
		return true
	}

	if reqLog == nil {
		reqLog = gatewayhttp.RequestLogger(c, "handler.openai_gateway.responses")
	}
	reqLog.Error("openai.handler_dependencies_missing", zap.Strings("missing_dependencies", missing))

	if c != nil && c.Writer != nil && !c.Writer.Written() {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": gin.H{
				"type":    "api_error",
				"message": "Service temporarily unavailable",
			},
		})
	}
	return false
}

func (h *OpenAIGatewayHandler) missingResponsesDependencies() []string {
	missing := make([]string, 0, 5)
	if h == nil {
		return append(missing, "handler")
	}
	if h.gatewayService == nil {
		missing = append(missing, "gatewayService")
	}
	if h.billingCacheService == nil {
		missing = append(missing, "billingCacheService")
	}
	if h.apiKeyService == nil {
		missing = append(missing, "apiKeyService")
	}
	if h.concurrencyHelper == nil || h.concurrencyHelper.Service() == nil {
		missing = append(missing, "concurrencyHelper")
	}
	return missing
}

func getContextInt64(c *gin.Context, key string) (int64, bool) {
	if c == nil || key == "" {
		return 0, false
	}
	v, ok := c.Get(key)
	if !ok {
		return 0, false
	}
	switch t := v.(type) {
	case int64:
		return t, true
	case int:
		return int64(t), true
	case int32:
		return int64(t), true
	case float64:
		return int64(t), true
	default:
		return 0, false
	}
}

// handleConcurrencyError 统一处理并发槽位获取失败。
func (h *OpenAIGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := gatewayhttp.ConcurrencyErrorResponse(err, slotType)
	h.handleStreamingAwareErrorWithCode(c, status, errType, code, message, streamStarted, false)
}

func (h *OpenAIGatewayHandler) acquireImageGenerationSlot(c *gin.Context, streamStarted bool) (func(), bool) {
	if h == nil || h.cfg == nil || h.imageLimiter == nil {
		return nil, true
	}
	imageConcurrency := h.cfg.Gateway.ImageConcurrency
	wait := strings.TrimSpace(imageConcurrency.OverflowMode) == config.ImageConcurrencyOverflowModeWait
	release, acquired := h.imageLimiter.Acquire(
		c.Request.Context(),
		imageConcurrency.Enabled,
		imageConcurrency.MaxConcurrentRequests,
		wait,
		time.Duration(imageConcurrency.WaitTimeoutSeconds)*time.Second,
		imageConcurrency.MaxWaitingRequests,
	)
	if acquired {
		return release, true
	}
	h.handleStreamingAwareError(c, http.StatusTooManyRequests, "rate_limit_error", "Image generation concurrency limit exceeded, please retry later", streamStarted)
	return nil, false
}

func (h *OpenAIGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	var rules gatewayhttp.ErrorRuleMatcher
	if h.errorPassthroughService != nil {
		rules = h.errorPassthroughService
	}
	h.NewOpenAITextHTTPHandler().WriteFailoverExhausted(c, gatewayhttp.ProjectOpenAIFailoverError(failoverErr), streamStarted, rules, gatewayhttp.FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) {
		gatewayhttp.SetOpsUpstreamError(c, status, message, "")
	}, SkipMonitoring: func(c *gin.Context) { c.Set(gatewayhttp.OpsSkipPassthroughKey, true) }})
}

func copyFailoverRetryAfter(c *gin.Context, headers http.Header) {
	gatewayhttp.CopyFailoverRetryAfter(c, headers)
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *OpenAIGatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	gatewayhttp.SetOpsUpstreamError(c, statusCode, errMsg, "")
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func (h *OpenAIGatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	return gatewayhttp.MapOpenAIUpstreamError(statusCode)
}

// handleStreamingAwareError handles errors that may occur after streaming has started
func (h *OpenAIGatewayHandler) handleStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.handleStreamingAwareErrorWithCode(c, status, errType, "", message, streamStarted, false)
}

func (h *OpenAIGatewayHandler) handleStreamingAwareErrorWithCode(
	c *gin.Context,
	status int,
	errType string,
	code string,
	message string,
	streamStarted bool,
	countTowardsSLA bool,
) {
	h.NewOpenAITextHTTPHandler().WriteStreamingErrorWithCode(c, status, errType, code, message, streamStarted, countTowardsSLA)
}

func (h *OpenAIGatewayHandler) ensureOpenAIStreamReadErrorResponse(c *gin.Context, err error, streamStarted bool) bool {
	code, message, ok := openaierrors.OpenAIUpstreamStreamReadErrorDetails(err)
	if !ok || c == nil || c.Writer == nil || gatewayhttp.IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Written() {
		streamStarted = true
	}
	h.handleStreamingAwareErrorWithCode(
		c, http.StatusBadGateway, "upstream_error", code, message, streamStarted, true,
	)
	return true
}

// ensureForwardErrorResponse 在 Forward 返回错误但尚未写响应时补写统一错误响应。
func (h *OpenAIGatewayHandler) ensureForwardErrorResponse(c *gin.Context, streamStarted bool) bool {
	return h.ensureOpenAIForwardErrorResponse(c, streamStarted, nil)
}

func (h *OpenAIGatewayHandler) ensureOpenAIForwardErrorResponse(c *gin.Context, streamStarted bool, err error) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	// 先停止两类心跳再读 Writer 状态，避免与心跳 goroutine 竞争。
	compactKeepaliveCommitted := gatewayhttp.StopOpenAICompactSSEKeepaliveCommitted(c)
	if compactKeepaliveCommitted {
		streamStarted = true
	}
	imageKeepalivePresent := gatewayhttp.OpenAIImagesJSONKeepalivePresent(c)
	gatewayhttp.StopOpenAIImagesJSONKeepaliveCommitted(c)
	imageKeepalivePaddingOnly := false
	imageKeepaliveResponseWritten := false
	if imageKeepalivePresent {
		adjustedSize := gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
		imageKeepalivePaddingOnly = adjustedSize < 0
		imageKeepaliveResponseWritten = adjustedSize >= 0
	}
	compactKeepaliveHasMeaningfulOutput := compactKeepaliveCommitted && gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c) > 0
	// Compact 心跳可能只提交了 200 响应头而没有写语义 SSE；此时仍须补齐 response.failed。
	if (gatewayhttp.IsResponseCommitted(c) && (!compactKeepaliveCommitted || compactKeepaliveHasMeaningfulOutput)) ||
		(!compactKeepaliveCommitted && imageKeepaliveResponseWritten) {
		return false
	}
	errType := "upstream_error"
	message := "Upstream request failed"
	status := http.StatusBadGateway
	if warning, ok := forwardcore.WarningFromError(err); ok && gatewayprovider.IsOpenAICyberWarningPayload(warning.ResponseBody, warning.Message) {
		errType = "invalid_request_error"
		message = gatewayprovider.ExtractOpenAICyberWarningMessage(warning.ResponseBody, warning.Message)
		if warning.StatusCode >= 400 && warning.StatusCode <= 599 {
			status = warning.StatusCode
		}
	}
	// 普通 SSE 心跳已写出时继续追加协议终态；图片 JSON 只有心跳空白时
	// 仍按非流式响应补写一个 JSON 错误，不能误切换到 SSE 格式。
	if c.Writer.Written() && !imageKeepalivePaddingOnly {
		streamStarted = true
	}
	h.handleStreamingAwareError(c, status, errType, message, streamStarted)
	return true
}

func shouldLogOpenAIForwardFailureAsWarn(c *gin.Context, wroteFallback bool) bool {
	if wroteFallback {
		return false
	}
	if c == nil || c.Writer == nil {
		return false
	}
	return c.Writer.Written()
}

// 判断转发层是否已把上游终止错误写给客户端。
//
// 响应流可能收到状态码 200 里的终止失败事件，例如安全策略拒绝。
// 转发层会先原样转发该终止事件，再返回错误给处理层做日志和统计；
// 处理层不能再追加通用失败事件，否则严格客户端会看到重复终止事件。
func openAIForwardErrorAlreadyCommunicated(c *gin.Context, writerSizeBeforeForward int, err error) bool {
	if err == nil || c == nil || c.Writer == nil {
		return false
	}
	// 与快照同口径：排除 compact 心跳字节，避免"仅心跳写出"被误判为
	// 响应已写出（#3887）。
	if gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward || gatewayhttp.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return false
	}
	if gatewayhttp.GetOpsCyberPolicy(c) != nil {
		return true
	}

	msg := strings.TrimSpace(err.Error())
	for _, prefix := range []string{
		"upstream response failed:",
		"non-streaming openai protocol error:",
	} {
		if strings.HasPrefix(msg, prefix) {
			return true
		}
	}
	return false
}

const cyberPolicyRecordedKey = gatewayhttp.CyberPolicyRecordedKey

const cyberSessionBlockedClientMsg = moderationflow.SessionBlockedClientMessage

type cyberSessionBlockFormat int

const (
	cyberBlockFormatResponses cyberSessionBlockFormat = iota
	cyberBlockFormatChat
	cyberBlockFormatAnthropic
)

func (h *OpenAIGatewayHandler) rejectIfCyberSessionBlocked(c *gin.Context, apiKey *apikey.APIKey, body []byte, model string, format cyberSessionBlockFormat) bool {
	return h.NewCyberHTTPHandler().RejectSession(c, apikey.CopyAPIKey(apiKey), body, model, gatewayhttp.CyberBlockFormat(format))
}

func (h *OpenAIGatewayHandler) enqueueCyberSessionBlockedOpsEntry(c *gin.Context, apiKey *apikey.APIKey, model string, sessionBlockKey string) {
	h.NewCyberHTTPHandler().EnqueueBlocked(c, apikey.CopyAPIKey(apiKey), model, sessionBlockKey)
}

func (h *OpenAIGatewayHandler) recordCyberPolicyIfMarked(c *gin.Context, apiKey *apikey.APIKey, account *gatewayprovider.ExecutionAccount, subscription *billing.UserSubscription, model string, forwardErrored bool, cyberBlockArg any, channelFields routing.ChannelUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool {
	mark := gatewayhttp.GetOpsCyberPolicy(c)
	if mark == nil || c == nil {
		return false
	}
	call := gatewayhttp.CyberPolicyCall{Key: apikey.CopyAPIKey(apiKey),
		Account:        moderationAccountView(account),
		Model:          model,
		ForwardErrored: forwardErrored}
	switch value := cyberBlockArg.(type) {
	case string:
		call.BlockKey = value
	case []byte:
		if apiKey != nil {
			plan := buildCyberSessionBlockWritePlan(apiKey.ID, c, value)
			call.Plan = moderationflow.BlockPlan{ScopeKey: plan.scopeKey,
				Keys: plan.keys}
			call.HasPlan = true
		}
	}
	// 所有旧实体在提交前转为独立完成快照；后台闭包不持有 Gin 或后续可变的 turn 数据。
	compaction := gatewayhttp.IsOpenAINativeCompactionV2(c)
	if len(nativeCompaction) > 0 {
		compaction = nativeCompaction[0]
	}
	platform := capability.PlatformOpenAI
	if account != nil && strings.TrimSpace(account.Record.Platform) != "" {
		platform = account.Record.Platform
	}
	call.Usage = gatewayprovider.CaptureCyber(c.Request.Context(), gatewayprovider.CyberCapture{
		APIKey:             apiKey,
		Account:            gatewayprovider.ExecutionCompletionRecord(account),
		Subscription:       subscription,
		RequestID:          c.Writer.Header().Get("X-Request-Id"),
		Model:              model,
		Stream:             requestIsStream(c),
		InputTokens:        mark.UpstreamInTok,
		OutputTokens:       mark.UpstreamOutTok,
		InboundEndpoint:    gatewayhttp.GetInboundEndpoint(c),
		UpstreamEndpoint:   gatewayhttp.GetUpstreamEndpoint(c, platform),
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          clientip.GetClientIP(c),
		ClientSessionID:    gatewayhttp.ExtractClientSessionID(c),
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      admission.QuotaPlatform(c.Request.Context(), apiKey),
		NativeCompactionV2: compaction,
		ChannelUsageFields: channelFields,
	})
	return h.NewCyberHTTPHandler().RecordPolicy(c, call)
}

func requestIsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(gatewayhttp.OpsStreamKey); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func clearCyberPolicyTurnState(c *gin.Context) {
	gatewayhttp.ClearCyberTurnState(c, gatewayhttp.ClearOpsCyberPolicy)
}

func openAIForwardMayFailover(c *gin.Context, writerSizeBeforeForward int, failoverErr *forwardcore.UpstreamFailoverError) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if gatewayhttp.OpenAICompactKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return true
	}
	return failoverErr != nil && failoverErr.SafeToFailoverAfterWrite
}

func openAIRequestAllowsFailoverReplay(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return !gatewayhttp.FailoverClientGone(c)
}

func (h *OpenAIGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	h.NewOpenAITextHTTPHandler().WriteError(c, status, errType, message)
}

// openAICompactKeepaliveInterval 复用流式 keepalive 配置作为 compact 下游
// 心跳间隔；0 表示禁用（与流式路径语义一致）。
func (h *OpenAIGatewayHandler) openAICompactKeepaliveInterval() time.Duration {
	if h.cfg == nil || h.cfg.Gateway.StreamKeepaliveInterval <= 0 {
		return 0
	}
	return time.Duration(h.cfg.Gateway.StreamKeepaliveInterval) * time.Second
}

func setOpenAIClientTransportHTTP(c *gin.Context) {
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportHTTP)
}

func setOpenAIClientTransportWS(c *gin.Context) {
	gatewayhttp.SetOpenAIClientTransport(c, gatewayhttp.OpenAIClientTransportWS)
}

func ensureOpenAIPoolModeSessionHash(sessionHash string, account *gatewayprovider.ExecutionAccount) string {
	if sessionHash != "" || account == nil || !account.View().IsPoolMode() {
		return sessionHash
	}
	// 为当前请求生成一次性粘性会话键，确保同账号重试不会重新负载均衡到其他账号。
	return "openai-pool-retry-" + uuid.NewString()
}

func openAIWSNextAttemptMessage(current, retryPayload []byte, retryCurrentTurn bool) ([]byte, bool) {
	return gatewayws.EntryNextAttemptMessage(current, retryPayload, retryCurrentTurn)
}

type cyberSessionBlockWritePlan struct {
	scopeKey string
	keys     []string
}

func buildCyberSessionBlockWritePlan(apiKeyID int64, c *gin.Context, body []byte) cyberSessionBlockWritePlan {
	explicit := service.CyberSessionExplicitBlockKey(apiKeyID, c, body)
	transcript := service.CyberSessionTranscriptBlockKeys(apiKeyID, body)
	scope := ""
	if len(transcript) > 0 {
		scope = cyberSessionScopeKey(apiKeyID, c)
	}
	plan := moderationflow.BuildBlockPlan(explicit, transcript, scope)
	return cyberSessionBlockWritePlan{scopeKey: plan.ScopeKey, keys: plan.Keys}
}

func findBlockedCyberSessionKey(ctx context.Context, gatewayService *service.OpenAIGatewayService, apiKeyID int64, c *gin.Context, body []byte) string {
	if gatewayService == nil {
		return ""
	}
	clientIP, userAgent := "", ""
	if c != nil {
		clientIP = strings.TrimSpace(clientip.GetClientIP(c))
		userAgent = c.GetHeader("User-Agent")
	}
	return gatewayService.FindCyberSessionBlockedForRequest(ctx, apiKeyID, c, body, clientIP, userAgent)
}

func cyberSessionScopeKey(apiKeyID int64, c *gin.Context) string {
	if c == nil {
		return ""
	}
	return service.CyberSessionScopeKey(apiKeyID, strings.TrimSpace(clientip.GetClientIP(c)), c.GetHeader("User-Agent"))
}

// handleGroupSelectionBusinessError 只绑定原 Key 读取与平台展示目录。
func handleGroupSelectionBusinessError(c *gin.Context, err error, started bool, write func(int, string, string, bool)) bool {
	return gatewayhttp.WriteGroupSelectionBusinessError(c, err, started, keyhttp.GetAPIKeyFromContext, gatewayprovider.ModelDisplayCatalogue{}, write)
}
