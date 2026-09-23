package handler

import (
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/promptpolicy"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	moderationcore "github.com/TokenFlux/TokenRouter/internal/moderation"

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
		gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, status, errType, message, streamStarted)
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
	resources ...*gatewayhttp.OpenAIHTTPResources,
) *OpenAIGatewayHandler {
	pingInterval := time.Duration(0)
	maxAccountSwitches := 3
	if cfg != nil {
		pingInterval = time.Duration(cfg.Concurrency.PingInterval) * time.Second
		if cfg.Gateway.MaxAccountSwitches > 0 {
			maxAccountSwitches = cfg.Gateway.MaxAccountSwitches
		}
	}
	var shared *gatewayhttp.OpenAIHTTPResources
	if len(resources) > 0 {
		shared = resources[0]
	}
	if shared == nil {
		shared = &gatewayhttp.OpenAIHTTPResources{Concurrency: gatewayhttp.NewConcurrencyHelper(concurrencyService, gatewayhttp.SSEPingFormatComment, pingInterval), Images: &scheduler.ImageConcurrencyLimiter{}, ImageOptions: openAIImageAdmissionOptions(cfg)}
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
		concurrencyHelper:        shared.Concurrency,
		imageLimiter:             shared.Images,
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

// handleAnthropicFailoverExhausted 将上游切号错误转换为 Anthropic 格式。
func (h *OpenAIGatewayHandler) handleAnthropicFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	if failoverErr != nil && gatewayprovider.IsOpenAIRequestBodyTooLarge(failoverErr) {
		gatewayhttp.SetOpsUpstreamError(c, http.StatusRequestEntityTooLarge, forwardcore.OpenAIRequestBodyTooLargeClientMessage, "")
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(
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
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, "api_error", message, streamStarted)
		return
	}
	if failoverErr != nil && gatewayprovider.IsOpenAICapacityShed(failoverErr) && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, "api_error", failoverErr.ClientMessage, streamStarted)
		return
	}
	status, errType, errMsg := h.mapUpstreamError(failoverErr.StatusCode)
	gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, status, errType, errMsg, streamStarted)
}

// ensureAnthropicErrorResponse writes a fallback Anthropic error if no response was written.
func (h *OpenAIGatewayHandler) ensureAnthropicErrorResponse(c *gin.Context, streamStarted bool) bool {
	if c == nil || c.Writer == nil || c.Writer.Written() {
		return false
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteAnthropicStreamingError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
	return true
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
			gatewayhttp.DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(c, status, errType, code, message, *streamStarted, false)
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
	wroteFallback := gatewayhttp.DefaultOpenAIErrorOutput().EnsureFallback(c, started)
	gatewayhttp.RequestLogger(c, "handler.openai_gateway.responses").Error(
		"openai.responses_panic_recovered",
		zap.Bool("fallback_error_response_written", wroteFallback),
		zap.Any("panic", recovered),
		zap.ByteString("stack", debug.Stack()),
	)
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

func (h *OpenAIGatewayHandler) handleFailoverExhausted(c *gin.Context, failoverErr *forwardcore.UpstreamFailoverError, streamStarted bool) {
	var rules gatewayhttp.ErrorRuleMatcher
	if h.errorPassthroughService != nil {
		rules = h.errorPassthroughService
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteFailoverExhausted(c, gatewayhttp.ProjectOpenAIFailoverError(failoverErr), streamStarted, rules, gatewayhttp.FailoverErrorHooks{Upstream: func(c *gin.Context, status int, message string) {
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
	gatewayhttp.DefaultOpenAIErrorOutput().StreamError(c, status, errType, errMsg, streamStarted)
}

func (h *OpenAIGatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	return gatewayhttp.MapOpenAIUpstreamError(statusCode)
}

func (h *OpenAIGatewayHandler) ensureOpenAIStreamReadErrorResponse(c *gin.Context, err error, streamStarted bool) bool {
	code, message, ok := openaierrors.OpenAIUpstreamStreamReadErrorDetails(err)
	if !ok || c == nil || c.Writer == nil || gatewayhttp.IsResponseCommitted(c) {
		return false
	}
	if c.Writer.Written() {
		streamStarted = true
	}
	gatewayhttp.DefaultOpenAIErrorOutput().WriteStreamingErrorWithCode(
		c, http.StatusBadGateway, "upstream_error", code, message, streamStarted, true,
	)
	return true
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
