package handler

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/moderationflow"

	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"

	"github.com/TokenFlux/TokenRouter/internal/domain"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ip"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	middleware2 "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// OpenAIGatewayHandler handles OpenAI API gateway requests
type OpenAIGatewayHandler struct {
	cyberHTTP                  *gatewayhttp.CyberHandler
	completionRecorder         *completion.Recorder
	gatewayService             *service.OpenAIGatewayService
	billingCacheService        *service.BillingCacheService
	apiKeyService              *service.APIKeyService
	usageRecordWorkerPool      *service.UsageRecordWorkerPool
	errorPassthroughService    *service.ErrorPassthroughService
	contentModerationService   *service.ContentModerationService
	grokMediaEligibilityProber grokMediaEligibilityProber
	opsService                 *service.OpsService
	concurrencyHelper          *ConcurrencyHelper
	imageLimiter               *imageConcurrencyLimiter
	maxAccountSwitches         int
	cfg                        *config.Config
}

func newOpenAIWSLocalRoutingRejectedError(model string, err error) error {
	return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, gatewayws.EntryLocalRoutingReason(model), gatewayws.EntryLocalRoutingCause(err))
}

func shouldReportOpenAIWSProxyAccountFailure(err error) bool {
	return gatewayws.EntryShouldReportFailure(err)
}

func openAIWSIngressEndedByClient(err error) bool {
	return gatewayhttp.ResponsesWSEndedByClient(err, wsEntryCloseInfo(err))
}

// grokMediaEligibilityProber 在首次媒体转发前补齐 OAuth 账号的计费观测。
type grokMediaEligibilityProber interface {
	ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error)
}

// openAIForwardSucceededForScheduling 会排除以失败事件结束的 WebSocket 转发结果。
func openAIForwardSucceededForScheduling(result *service.OpenAIForwardResult) bool {
	return result.SucceededForScheduling()
}

func openAIAccountScheduleModel(c *gin.Context, account *service.Account, forwardModel string, requireCompact bool, result *service.OpenAIForwardResult) string {
	if result != nil {
		if actual := strings.TrimSpace(result.UpstreamModel); actual != "" {
			return actual
		}
	}
	if c != nil {
		if value, ok := c.Get(service.OpsUpstreamModelKey); ok {
			if actual, ok := value.(string); ok && strings.TrimSpace(actual) != "" {
				return strings.TrimSpace(actual)
			}
		}
	}
	return service.ResolveOpenAIAccountUpstreamModelForRequest(account, forwardModel, requireCompact)
}

func resolveOpenAIMessagesDispatchMappedModel(args ...any) string {
	var apiKey *service.APIKey
	var requestedModel string
	for _, arg := range args {
		switch value := arg.(type) {
		case *service.APIKey:
			apiKey = value
		case string:
			requestedModel = value
		}
	}
	if apiKey == nil || apiKey.Group == nil {
		return ""
	}
	return strings.TrimSpace(apiKey.Group.ResolveMessagesDispatchModel(requestedModel))
}

// resolveOpenAIMessagesAccountLayerModel 在渠道映射 C 之后执行分组映射 D，并保留协议模型规范化。
func resolveOpenAIMessagesAccountLayerModel(apiKey *service.APIKey, channelMappedModel string) string {
	channelMappedModel = strings.TrimSpace(channelMappedModel)
	if mappedModel := resolveOpenAIMessagesDispatchMappedModel(apiKey, channelMappedModel); mappedModel != "" {
		return mappedModel
	}
	return service.NormalizeOpenAICompatRequestedModel(channelMappedModel)
}

// resolveOpenAIMessagesAccountLayerModelForRequest 登记分组派发后的模型，供响应恢复与映射链记录使用。
func resolveOpenAIMessagesAccountLayerModelForRequest(ctx context.Context, apiKey *service.APIKey, channelMappedModel string) string {
	model := resolveOpenAIMessagesAccountLayerModel(apiKey, channelMappedModel)
	service.RegisterAPIKeyModelRedirectStage(ctx, model)
	return model
}

type openAIModelBodyReplaceFunc func([]byte, string) []byte

func openAIChannelMappedModel(requestedModel string, mapping service.ChannelMappingResult) string {
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
	mapping service.ChannelMappingResult,
	platform string,
	replace openAIModelBodyReplaceFunc,
) ([]byte, string, bool) {
	routingModel := openAIChannelMappedModel(requestedModel, mapping)
	mappedBody := openAIModelMappedBody(body, mapping.Mapped, routingModel, replace)
	imageIntent := service.IsImageGenerationIntentForPlatform(endpoint, routingModel, mappedBody, platform)
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
func appendOpenAIAccountProxyLogFields(fields []zap.Field, account *service.Account) []zap.Field {
	if account == nil {
		return fields
	}
	if account.Proxy != nil {
		return append(fields,
			zap.Int64("proxy_id", account.Proxy.ID),
			zap.String("proxy_name", account.Proxy.Name),
			zap.String("proxy_host", account.Proxy.Host),
			zap.Int("proxy_port", account.Proxy.Port),
		)
	}
	if account.ProxyID != nil {
		return append(fields, zap.Int64p("proxy_id", account.ProxyID))
	}
	return fields
}

// handleGroupSelectionBusinessError 将账号选择阶段的本地分组限制转换为明确的客户端侧错误。
func handleGroupSelectionBusinessError(c *gin.Context, err error, streamStarted bool, writeError func(int, string, string, bool)) bool {
	if errors.Is(err, service.ErrClaudeCodeOnly) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		writeError(http.StatusForbidden, "permission_error", service.ErrClaudeCodeOnly.Error(), streamStarted)
		return true
	}

	var modelErr *service.GroupModelUnsupportedError
	if errors.As(err, &modelErr) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		message := modelErr.Error()
		if apiKey, ok := middleware2.GetAPIKeyFromContext(c); ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.CustomModelsListEnabled() {
			platform := strings.TrimSpace(modelErr.Platform)
			if platform == "" {
				platform = apiKey.Group.Platform
			}
			availableModels := filterModelsByCustomList(modelErr.AvailableModels, defaultModelIDsForPlatform(platform), apiKey.Group.ModelsListConfig.Models)
			message = (&service.GroupModelUnsupportedError{
				RequestedModel:  modelErr.RequestedModel,
				AvailableModels: availableModels,
			}).Error()
		}
		writeError(http.StatusForbidden, "permission_error", message, streamStarted)
		return true
	}
	return false
}

// handleOpenAISelectionBusinessError 保持 OpenAI handler 调用侧语义清晰。
func (h *OpenAIGatewayHandler) handleOpenAISelectionBusinessError(c *gin.Context, err error, streamStarted bool) bool {
	return handleGroupSelectionBusinessError(c, err, streamStarted, func(status int, errType string, message string, streamStarted bool) {
		h.handleStreamingAwareError(c, status, errType, message, streamStarted)
	})
}

func openAICompatibleRequestPlatform(apiKey *service.APIKey) string {
	if apiKey != nil && apiKey.Group != nil {
		switch apiKey.Group.Platform {
		case service.PlatformGrok, service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek:
			return apiKey.Group.Platform
		}
	}
	return service.PlatformOpenAI
}

// effectiveAPIKeyPlatform 返回当前 API key 在 handler 层应使用的平台。
// 强制平台路由由中间件单独处理；没有可识别的平台时保持 OpenAI 兼容默认值。
func effectiveAPIKeyPlatform(c *gin.Context, apiKey *service.APIKey) string {
	if c != nil {
		if forced, ok := middleware2.GetForcePlatformFromContext(c); ok && strings.TrimSpace(forced) != "" {
			return strings.TrimSpace(forced)
		}
	}
	return openAICompatibleRequestPlatform(apiKey)
}

// openAIResponsesRequiredCapability 根据显式生图意图选择账号必须支持的端点能力。
func openAIResponsesRequiredCapability(imageIntent bool, platform string) service.OpenAIEndpointCapability {
	if imageIntent && platform == service.PlatformOpenAI {
		return service.OpenAIEndpointCapabilityResponses
	}
	return service.OpenAIEndpointCapabilityTextGeneration
}

// openAIResponsesRequiredCapabilityForRequest 让两类压缩都要求 Responses 能力，
// 其中原生 V2 还必须通过自身独立的账号模式和探测状态门禁。
func openAIResponsesRequiredCapabilityForRequest(imageIntent bool, nativeCompactionV2 bool, legacyCompact bool, platform string) service.OpenAIEndpointCapability {
	if nativeCompactionV2 && platform == service.PlatformOpenAI {
		return service.OpenAIEndpointCapabilityRemoteCompactionV2
	}
	if legacyCompact && platform == service.PlatformOpenAI {
		return service.OpenAIEndpointCapabilityResponses
	}
	return openAIResponsesRequiredCapability(imageIntent, platform)
}

// allowOpenAICompatibleMessagesDispatch 兼容直接调用 handler 的测试与内部入口。
func allowOpenAICompatibleMessagesDispatch(apiKey *service.APIKey) bool {
	if apiKey == nil || apiKey.Group == nil {
		return true
	}
	return apiKey.Group.AllowsClientProtocol(domain.ProtocolAnthropicMessages)
}

// NewOpenAIGatewayHandler creates a new OpenAIGatewayHandler
func NewOpenAIGatewayHandler(
	gatewayService *service.OpenAIGatewayService,
	concurrencyService *service.ConcurrencyService,
	billingCacheService *service.BillingCacheService,
	apiKeyService *service.APIKeyService,
	usageRecordWorkerPool *service.UsageRecordWorkerPool,
	errorPassthroughService *service.ErrorPassthroughService,
	contentModerationService *service.ContentModerationService,
	opsService *service.OpsService,
	cfg *config.Config,
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
		billingCacheService:      billingCacheService,
		apiKeyService:            apiKeyService,
		usageRecordWorkerPool:    usageRecordWorkerPool,
		errorPassthroughService:  errorPassthroughService,
		contentModerationService: contentModerationService,
		opsService:               opsService,
		concurrencyHelper:        NewConcurrencyHelper(concurrencyService, SSEPingFormatComment, pingInterval),
		imageLimiter:             &imageConcurrencyLimiter{},
		maxAccountSwitches:       maxAccountSwitches,
		cfg:                      cfg,
	}
}

// Responses 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Responses(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Responses(c)
}

func isOpenAILegacyCompactPath(c *gin.Context) bool {
	return service.IsOpenAIResponsesCompactPath(c)
}

// isBareOpenAIResponsesPath 仅匹配裸 /responses 端点（无 /compact 等子路径），
// body-signal 提升只允许发生在这里，避免误伤 /responses/{id}/... 形态的请求。
func isBareOpenAIResponsesPath(c *gin.Context) bool {
	if c == nil || c.Request == nil || c.Request.URL == nil {
		return false
	}
	normalizedPath := strings.TrimRight(strings.TrimSpace(c.Request.URL.Path), "/")
	switch normalizedPath {
	case EndpointResponses, "/openai/v1/responses", "/responses", "/backend-api/codex/responses":
		return true
	default:
		return false
	}
}

// isOpenAIRemoteCompactionV2Request 按 wire 形状识别原生 remote compaction v2 流式协议。
func isOpenAIRemoteCompactionV2Request(body []byte) bool {
	stream, valid := parseOpenAICompatibleStream(body)
	return valid && stream && service.HasCompactionTriggerInInput(body)
}

// normalizeOpenAIResponsesCompactRequest 保留 Codex remote compaction v2 原生的
// 流式 /responses 链路；不满足原生 V2 wire 形状的 body-signal 请求仍提升到旧 compact 桥接链路。
// 返回归一化后的 body；ok=false 表示错误响应已写出，调用方应直接 return。
func (h *OpenAIGatewayHandler) normalizeOpenAIResponsesCompactRequest(c *gin.Context, reqLog *zap.Logger, body []byte) ([]byte, bool) {
	isCompactRequest := isOpenAILegacyCompactPath(c)
	if !isCompactRequest && isBareOpenAIResponsesPath(c) && service.HasCompactionTriggerInInput(body) {
		if normalized, changed, err := service.NormalizeCompactionTriggerInputOrder(body); err != nil {
			reqLog.Warn("codex.remote_compact.trigger_order_normalization_failed", zap.Error(err))
		} else if changed {
			body = normalized
		}
		if isOpenAIRemoteCompactionV2Request(body) {
			// 原生 V2 必须在出站前保留协商能力，不能被路径保持逻辑吞掉。
			service.MarkOpenAINativeCompactionV2(c)
			return body, true
		}
		c.Request.URL.Path = strings.TrimRight(c.Request.URL.Path, "/") + "/compact"
		isCompactRequest = true
		clientStream := gjson.GetBytes(body, "stream").Bool()
		if clientStream {
			service.MarkOpenAICompactClientStream(c)
		}
		reqLog.Info("codex.remote_compact.detected_body_signal", zap.Bool("client_stream", clientStream))
	}
	if !isCompactRequest {
		return body, true
	}
	if compactSeed := strings.TrimSpace(gjson.GetBytes(body, "prompt_cache_key").String()); compactSeed != "" {
		c.Set(service.OpenAICompactSessionSeedKeyForTest(), compactSeed)
	}
	normalizedCompactBody, normalizedCompact, compactErr := service.NormalizeOpenAICompactRequestBodyForTest(body)
	if compactErr != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to normalize compact request body")
		return nil, false
	}
	if normalizedCompact {
		body = normalizedCompactBody
	}
	return body, true
}

func (h *OpenAIGatewayHandler) logOpenAIRemoteCompactOutcome(c *gin.Context, startedAt time.Time) {
	if !isOpenAILegacyCompactPath(c) {
		return
	}

	var (
		ctx    = context.Background()
		path   string
		status int
	)
	if c != nil {
		if c.Request != nil {
			ctx = c.Request.Context()
			if c.Request.URL != nil {
				path = strings.TrimSpace(c.Request.URL.Path)
			}
		}
		if c.Writer != nil {
			status = c.Writer.Status()
		}
	}

	outcome := "failed"
	if status >= 200 && status < 300 {
		outcome = "succeeded"
	}
	// compact 心跳提交后失败的 wire 状态码固化为 200，真实结局以流内错误
	// 标记为准（response.failed 降级路径会 MarkOpsStreamError）。
	if outcome == "succeeded" && c != nil {
		if _, hasStreamErr := service.GetOpsStreamError(c); hasStreamErr {
			outcome = "failed"
		}
	}
	latencyMs := time.Since(startedAt).Milliseconds()
	if latencyMs < 0 {
		latencyMs = 0
	}

	fields := []zap.Field{
		zap.String("component", "handler.openai_gateway.responses"),
		zap.Bool("remote_compact", true),
		zap.String("compact_outcome", outcome),
		zap.Int("status_code", status),
		zap.Int64("latency_ms", latencyMs),
		zap.String("path", path),
		zap.Bool("force_codex_cli", h != nil && h.cfg != nil && h.cfg.Gateway.ForceCodexCLI),
	}

	if c != nil {
		if userAgent := strings.TrimSpace(c.GetHeader("User-Agent")); userAgent != "" {
			fields = append(fields, zap.String("request_user_agent", userAgent))
		}
		if v, ok := c.Get(opsModelKey); ok {
			if model, ok := v.(string); ok && strings.TrimSpace(model) != "" {
				fields = append(fields, zap.String("request_model", strings.TrimSpace(model)))
			}
		}
		if v, ok := c.Get(opsAccountIDKey); ok {
			if accountID, ok := v.(int64); ok && accountID > 0 {
				fields = append(fields, zap.Int64("account_id", accountID))
			}
		}
		if c.Writer != nil {
			if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("x-request-id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			} else if upstreamRequestID := strings.TrimSpace(c.Writer.Header().Get("X-Request-Id")); upstreamRequestID != "" {
				fields = append(fields, zap.String("upstream_request_id", upstreamRequestID))
			}
		}
	}

	log := logger.FromContext(ctx).With(fields...)
	if outcome == "succeeded" {
		log.Info("codex.remote_compact.succeeded")
		return
	}
	log.Warn("codex.remote_compact.failed")
}

// Messages 仅保留兼容入口，HTTP 准入组合使用目标包唯一实现。
func (h *OpenAIGatewayHandler) Messages(c *gin.Context) {
	h.NewOpenAITextHTTPHandler().Messages(c)
}

func resolveOpenAIMessagesMetadataSession(c *gin.Context, sessionHash, promptCacheKey, reqModel string, body []byte) (string, string) {
	return gatewaysession.MessagesMetadataSession(service.ClaudeCodeSessionIDFromHeader(c), sessionHash, promptCacheKey, reqModel, body)
}

func (h *OpenAIGatewayHandler) anthropicStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	h.NewOpenAITextHTTPHandler().WriteAnthropicStreamingError(c, status, errType, message, streamStarted)
}

// handleAnthropicFailoverExhausted 将上游切号错误转换为 Anthropic 格式。
func (h *OpenAIGatewayHandler) handleAnthropicFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool) {
	if failoverErr != nil && failoverErr.IsOpenAIRequestBodyTooLarge() {
		service.SetOpsUpstreamError(c, http.StatusRequestEntityTooLarge, service.OpenAIRequestBodyTooLargeClientMessage, "")
		h.anthropicStreamingAwareError(
			c,
			http.StatusRequestEntityTooLarge,
			"invalid_request_error",
			service.OpenAIRequestBodyTooLargeClientMessage,
			streamStarted,
		)
		return
	}
	if failoverErr != nil {
		copyFailoverRetryAfter(c, failoverErr.ResponseHeaders)
	}
	if failoverErr != nil && failoverErr.IsCredentialFailure() {
		status, message := credentialFailoverClientResponse(failoverErr)
		h.anthropicStreamingAwareError(c, status, "api_error", message, streamStarted)
		return
	}
	if failoverErr != nil && failoverErr.IsOpenAICapacityShed() && strings.TrimSpace(failoverErr.ClientMessage) != "" {
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

	validation := service.ValidateFunctionCallOutputContextBytes(body)
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
	return wrapReleaseOnDone(ctx, userReleaseFunc), true
}

func (h *OpenAIGatewayHandler) acquireResponsesAccountSlot(
	c *gin.Context,
	groupID *int64,
	sessionHash string,
	selection *service.AccountSelectionResult,
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
	selection *service.AccountSelectionResult,
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
		projected = &gatewayhttp.SelectedAccountSlot{AccountID: selection.Account.ID, Acquired: selection.Acquired, ReleaseFunc: selection.ReleaseFunc, WaitPlan: selection.WaitPlan}
	}
	release, ok := gatewayhttp.AcquireSelectedAccountSlot(c, groupID, sessionHash, projected, reqStream, streamStarted, reqLog, writeError, h.concurrencyHelper, h.gatewayService, gatewayhttp.AccountSlotHooks{Acquired: service.MarkOpsAccountSlotAcquired, CapacityLimited: markOpsRoutingCapacityLimited})
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
	requestLogger(c, "handler.openai_gateway.responses").Error(
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
		reqLog = requestLogger(c, "handler.openai_gateway.responses")
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

func (h *OpenAIGatewayHandler) submitUsageRecordTask(c *gin.Context, task service.UsageRecordTask) {
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
			zap.String("component", "handler.openai_gateway.responses"),
		).Warn("openai.usage_record_task_stopped_sync_fallback")
	}
	// 回退路径：worker 池未注入或已停止时同步执行，避免退回到无界 goroutine 模式。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.responses"),
				zap.Any("panic", recovered),
			).Error("openai.usage_record_task_panic_recovered")
		}
	}()
	task(ctx)
}

func (h *OpenAIGatewayHandler) submitOpenAIUsageRecordTask(c *gin.Context, result *service.OpenAIForwardResult, task service.UsageRecordTask) {
	if result != nil && result.ImageCount > 0 {
		h.submitMandatoryUsageRecordTask(c, task)
		return
	}
	h.submitUsageRecordTask(c, task)
}

func (h *OpenAIGatewayHandler) submitMandatoryUsageRecordTask(c *gin.Context, task service.UsageRecordTask) {
	if task == nil {
		return
	}
	task = wrapUsageRecordTaskContext(c, task)
	if h.usageRecordWorkerPool != nil {
		if mode := h.usageRecordWorkerPool.Submit(task); !mode.Dropped() {
			return
		}
		logger.L().With(
			zap.String("component", "handler.openai_gateway.usage"),
		).Warn("openai.usage_record_task_mandatory_sync_fallback")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	defer func() {
		if recovered := recover(); recovered != nil {
			logger.L().With(
				zap.String("component", "handler.openai_gateway.usage"),
				zap.Any("panic", recovered),
			).Error("openai.usage_record_task_panic_recovered")
		}
	}()
	task(ctx)
}

// handleConcurrencyError 统一处理并发槽位获取失败。
func (h *OpenAIGatewayHandler) handleConcurrencyError(c *gin.Context, err error, slotType string, streamStarted bool) {
	status, errType, code, message := concurrencyErrorResponse(err, slotType)
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

func (h *OpenAIGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *service.UpstreamFailoverError, streamStarted bool) {
	var rules gatewayhttp.ErrorRuleMatcher
	if h.errorPassthroughService != nil {
		rules = h.errorPassthroughService
	}
	h.NewOpenAITextHTTPHandler().WriteFailoverExhausted(c, projectOpenAIFailoverError(failoverErr), streamStarted, rules, gatewayhttp.FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) { service.SetOpsUpstreamError(c, status, message, "") }, SkipMonitoring: func(c *gin.Context) { c.Set(service.OpsSkipPassthroughKey, true) }})
}

func credentialFailoverClientResponse(failoverErr *service.UpstreamFailoverError) (int, string) {
	if failoverErr != nil && failoverErr.Reason == service.OpenAIUpstreamAccessStateReason && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		return status, failoverErr.ClientMessage
	}
	if failoverErr != nil && failoverErr.Reason == service.AntigravityCredentialRejectedReason {
		return http.StatusBadGateway, service.AntigravityCredentialRejectedClientMessage
	}
	return http.StatusServiceUnavailable, service.GrokCredentialUnavailableClientMessage
}

func copyFailoverRetryAfter(c *gin.Context, headers http.Header) {
	gatewayhttp.CopyFailoverRetryAfter(c, headers)
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *OpenAIGatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	service.SetOpsUpstreamError(c, statusCode, errMsg, "")
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
	code, message, ok := service.OpenAIUpstreamStreamReadErrorDetails(err)
	if !ok || c == nil || c.Writer == nil || service.IsResponseCommitted(c) {
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
	compactKeepaliveCommitted := service.StopOpenAICompactSSEKeepaliveCommitted(c)
	if compactKeepaliveCommitted {
		streamStarted = true
	}
	imageKeepalivePresent := service.OpenAIImagesJSONKeepalivePresent(c)
	service.StopOpenAIImagesJSONKeepaliveCommitted(c)
	imageKeepalivePaddingOnly := false
	imageKeepaliveResponseWritten := false
	if imageKeepalivePresent {
		adjustedSize := service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c)
		imageKeepalivePaddingOnly = adjustedSize < 0
		imageKeepaliveResponseWritten = adjustedSize >= 0
	}
	compactKeepaliveHasMeaningfulOutput := compactKeepaliveCommitted && service.OpenAICompactKeepaliveAdjustedWrittenSize(c) > 0
	// Compact 心跳可能只提交了 200 响应头而没有写语义 SSE；此时仍须补齐 response.failed。
	if (service.IsResponseCommitted(c) && (!compactKeepaliveCommitted || compactKeepaliveHasMeaningfulOutput)) ||
		(!compactKeepaliveCommitted && imageKeepaliveResponseWritten) {
		return false
	}
	errType := "upstream_error"
	message := "Upstream request failed"
	status := http.StatusBadGateway
	if warning, ok := service.ExtractOpenAIUpstreamWarning(err); ok && service.IsOpenAICyberWarningPayload(warning.ResponseBody, warning.Message) {
		errType = "invalid_request_error"
		message = service.ExtractOpenAICyberWarningMessage(warning.ResponseBody, warning.Message)
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
	if service.OpenAICompactKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward ||
		service.OpenAIImagesJSONKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return false
	}
	if service.GetOpsCyberPolicy(c) != nil {
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

func (h *OpenAIGatewayHandler) rejectIfCyberSessionBlocked(c *gin.Context, apiKey *service.APIKey, body []byte, model string, format cyberSessionBlockFormat) bool {
	return h.NewCyberHTTPHandler().RejectSession(c, service.APIKeyView(apiKey), body, model, gatewayhttp.CyberBlockFormat(format))
}

func (h *OpenAIGatewayHandler) enqueueCyberSessionBlockedOpsEntry(c *gin.Context, apiKey *service.APIKey, model string, sessionBlockKey string) {
	h.NewCyberHTTPHandler().EnqueueBlocked(c, service.APIKeyView(apiKey), model, sessionBlockKey)
}

func (h *OpenAIGatewayHandler) recordCyberPolicyIfMarked(c *gin.Context, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, model string, forwardErrored bool, cyberBlockArg any, channelFields service.ChannelUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool {
	mark := service.GetOpsCyberPolicy(c)
	if mark == nil || c == nil {
		return false
	}
	call := gatewayhttp.CyberPolicyCall{Key: service.APIKeyView(apiKey),
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
	compaction := service.IsOpenAINativeCompactionV2(c)
	if len(nativeCompaction) > 0 {
		compaction = nativeCompaction[0]
	}
	platform := service.PlatformOpenAI
	if account != nil && strings.TrimSpace(account.Platform) != "" {
		platform = account.Platform
	}
	call.Usage = service.CompletionCyberInput(c.Request.Context(), service.CyberPolicyUsageInput{
		APIKey:             apiKey,
		Account:            account,
		Subscription:       subscription,
		RequestID:          c.Writer.Header().Get("X-Request-Id"),
		Model:              model,
		Stream:             requestIsStream(c),
		InputTokens:        mark.UpstreamInTok,
		OutputTokens:       mark.UpstreamOutTok,
		InboundEndpoint:    GetInboundEndpoint(c),
		UpstreamEndpoint:   GetUpstreamEndpoint(c, platform),
		UserAgent:          c.GetHeader("User-Agent"),
		IPAddress:          ip.GetClientIP(c),
		ClientSessionID:    service.ExtractClientSessionID(c),
		RequestPayloadHash: requestPayloadHash,
		APIKeyService:      h.apiKeyService,
		QuotaPlatform:      service.QuotaPlatform(c.Request.Context(), apiKey),
		NativeCompactionV2: compaction,
		ChannelUsageFields: channelFields,
	})
	return h.NewCyberHTTPHandler().RecordPolicy(c, call)
}

func requestIsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	if v, ok := c.Get(opsStreamKey); ok {
		if b, ok := v.(bool); ok {
			return b
		}
	}
	return false
}

func clearCyberPolicyTurnState(c *gin.Context) {
	gatewayhttp.ClearCyberTurnState(c, service.ClearOpsCyberPolicy)
}

func openAIForwardMayFailover(c *gin.Context, writerSizeBeforeForward int, failoverErr *service.UpstreamFailoverError) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if service.OpenAICompactKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward {
		return true
	}
	return failoverErr != nil && failoverErr.SafeToFailoverAfterWrite
}

func openAIRequestAllowsFailoverReplay(c *gin.Context) bool {
	if c == nil || c.Request == nil {
		return false
	}
	return !failoverClientGone(c)
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
	service.SetOpenAIClientTransport(c, service.OpenAIClientTransportHTTP)
}

func setOpenAIClientTransportWS(c *gin.Context) {
	service.SetOpenAIClientTransport(c, service.OpenAIClientTransportWS)
}

func ensureOpenAIPoolModeSessionHash(sessionHash string, account *service.Account) string {
	if sessionHash != "" || account == nil || !account.IsPoolMode() {
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
		clientIP = strings.TrimSpace(ip.GetClientIP(c))
		userAgent = c.GetHeader("User-Agent")
	}
	return gatewayService.FindCyberSessionBlockedForRequest(ctx, apiKeyID, c, body, clientIP, userAgent)
}

func cyberSessionScopeKey(apiKeyID int64, c *gin.Context) string {
	if c == nil {
		return ""
	}
	return service.CyberSessionScopeKey(apiKeyID, strings.TrimSpace(ip.GetClientIP(c)), c.GetHeader("User-Agent"))
}

func summarizeWSCloseErrorForLog(err error) (string, string) {
	if err == nil {
		return "-", "-"
	}
	statusCode := coderws.CloseStatus(err)
	if statusCode == -1 {
		return "-", "-"
	}
	closeStatus := fmt.Sprintf("%d(%s)", int(statusCode), statusCode.String())
	closeReason := "-"
	var closeErr coderws.CloseError
	if errors.As(err, &closeErr) {
		reason := strings.TrimSpace(closeErr.Reason)
		if reason != "" {
			closeReason = reason
		}
	}
	return closeStatus, closeReason
}
