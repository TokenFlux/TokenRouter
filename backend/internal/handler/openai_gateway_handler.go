package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

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

var errOpenAIWSLocalRoutingRejected = errors.New("local websocket routing rejected")

// newOpenAIWSLocalRoutingRejectedError 标记请求在本地路由阶段被拒绝，避免把未发送到上游的错误归咎于账号。
func newOpenAIWSLocalRoutingRejectedError(model string, err error) error {
	reason := fmt.Sprintf("model %s is not available for this websocket channel or account", strings.TrimSpace(model))
	cause := fmt.Errorf("%w: %w", errOpenAIWSLocalRoutingRejected, err)
	return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, reason, cause)
}

// shouldReportOpenAIWSProxyAccountFailure 排除明确的本地模型路由拒绝，其余代理错误维持现有上报行为。
func shouldReportOpenAIWSProxyAccountFailure(err error) bool {
	if err == nil || errors.Is(err, errOpenAIWSLocalRoutingRejected) || service.IsOpenAIWSSessionPreemptedError(err) {
		return false
	}
	// 分组推理策略在发送上游前拒绝请求，不应污染账号健康状态。
	var overLimit *service.ReasoningEffortOverLimitError
	return !errors.As(err, &overLimit)
}

// openAIWSIngressEndedByClient reports whether a finished ingress WebSocket turn
// ended the way a healthy client ends one, rather than through an upstream or
// account fault.
//
// Three error shapes describe that same benign outcome and only the first was
// recognised:
//
//   - *service.OpenAIWSClientCloseError carrying 1000 — the gateway closing the
//     socket on its own terms, e.g. the inter-turn idle timeout.
//   - a bare coderws.CloseError{Code: 1000} — what coder/websocket returns when
//     the client closes cleanly. ReadOpenAIWSClientMessage hands conn.Read's
//     error back verbatim, so nothing ever wraps it into the type above and an
//     errors.As against that type cannot see it.
//   - context.Canceled — the client went away mid-turn. That path closes with
//     StatusGoingAway (1001) and carries the cancellation as its cause, so a
//     check for 1000 alone never matched it either.
//
// The last two fell through to shouldReportOpenAIWSProxyAccountFailure, which
// filters only model-switch and session-preemption errors. Everything else
// reaches ObserveOpenAIAPIKeyHealthFailure and scheduler.ReportResult(false), so
// a client that merely disconnected counted against the upstream account's
// health and could trip it out of scheduling.
//
// failoverClientGone already states the rule this restores for the HTTP failover
// path — a cancelled client context "被误报成账号耗尽" is a bug, not a signal —
// and summarizeWSCloseErrorForLog already reads the close code the correct way,
// which is why the resulting WARN printed close_status=1000(StatusNormalClosure)
// for an error that was, in the same breath, being charged to the account.
//
// Deliberately narrow. StatusGoingAway is not matched on its own: the gateway
// emits 1001 when it tears a session down for its own reasons too, and the
// client-cancellation case is already covered by context.Canceled.
// context.DeadlineExceeded is left out as well — the idle-timeout path wraps it
// in a 1000 close error and stays benign through the first check, while any
// other deadline is a genuine stall worth reporting.
func openAIWSIngressEndedByClient(err error) bool {
	if err == nil {
		return true
	}
	var closeErr *service.OpenAIWSClientCloseError
	if errors.As(err, &closeErr) && closeErr.StatusCode() == coderws.StatusNormalClosure {
		return true
	}
	if coderws.CloseStatus(err) == coderws.StatusNormalClosure {
		return true
	}
	return errors.Is(err, context.Canceled)
}

func openAIWSTurnBillingModel(result *service.OpenAIForwardResult, mapping service.ChannelMappingResult, requestedModel, upstreamModel string) string {
	billingModel := ""
	if result != nil {
		billingModel = strings.TrimSpace(result.BillingModel)
	}
	if billingModel == "" {
		billingModel = strings.TrimSpace(upstreamModel)
	}
	if billingModel == "" {
		billingModel = strings.TrimSpace(requestedModel)
	}

	requestedModel = strings.TrimSpace(requestedModel)
	switch mapping.BillingModelSource {
	case service.BillingModelSourceRequested:
		if requestedModel != "" {
			billingModel = requestedModel
		}
	case service.BillingModelSourceChannelMapped:
		mappedModel := strings.TrimSpace(mapping.MappedModel)
		if mappedModel != "" && mappedModel != requestedModel {
			billingModel = mappedModel
		}
	}
	return billingModel
}

// grokMediaEligibilityProber 在首次媒体转发前补齐 OAuth 账号的计费观测。
type grokMediaEligibilityProber interface {
	ProbeMediaEligibility(ctx context.Context, accountID int64) (bool, string, error)
}

const maxOpenAIFirstOutputTimeoutSwitches = 1

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

// openAIChannelMappedModel 返回渠道模型 C；渠道没有有效结果时保留客户端模型 R。
func openAIChannelMappedModel(requestedModel string, mapping service.ChannelMappingResult) string {
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		return strings.TrimSpace(requestedModel)
	}
	return routingModel
}

// openAIModelMappedBody 在存在渠道映射时返回替换模型后的请求体。
func openAIModelMappedBody(body []byte, mapped bool, mappedModel string, replace openAIModelBodyReplaceFunc) []byte {
	if !mapped || replace == nil {
		return body
	}
	return replace(body, mappedModel)
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

// newOpenAIModelMappedBodyCache 缓存同一入口请求体的模型替换结果，避免账号切换重试时重复解析 JSON。
func newOpenAIModelMappedBodyCache(body []byte, replace openAIModelBodyReplaceFunc) func(bool, string) []byte {
	replacedBodies := make(map[string][]byte)
	return func(mapped bool, mappedModel string) []byte {
		if !mapped {
			return body
		}
		if cachedBody, ok := replacedBodies[mappedModel]; ok {
			return cachedBody
		}
		replacedBody := openAIModelMappedBody(body, true, mappedModel, replace)
		replacedBodies[mappedModel] = replacedBody
		return replacedBody
	}
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
	return apiKey.Group.AllowsClientProtocol(service.GroupClientProtocolAnthropicMessages)
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

// Responses handles OpenAI Responses API endpoint
// POST /openai/v1/responses
// @project-doc docs/interfaces/openai_upstream.md#openai_protocol_dispatch
func (h *OpenAIGatewayHandler) Responses(c *gin.Context) {
	// 局部兜底：确保该 handler 内部任何 panic 都不会击穿到进程级。
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	compactStartedAt := time.Now()
	defer h.logOpenAIRemoteCompactOutcome(c, compactStartedAt)
	setOpenAIClientTransportHTTP(c)

	requestStart := time.Now()

	// Get apiKey and user from context (set by ApiKeyAuth middleware)
	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.responses",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	// Read request body
	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		logRequestBodyReadFailure(reqLog, c.Request, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	setOpsRequestContext(c, "", false)
	body, ok = h.normalizeOpenAIResponsesCompactRequest(c, reqLog, body)
	if !ok {
		return
	}
	legacyCompact := isOpenAILegacyCompactPath(c)
	nativeCompactionV2 := isBareOpenAIResponsesPath(c) && isOpenAIRemoteCompactionV2Request(body)
	// body-signal compact：上游 unary 等待期间向下游发 SSE 注释行心跳，防止
	// 反向代理空闲超时掐断长压缩连接（#3887）。首拍延迟一个心跳间隔，快速
	// 失败仍走 JSON+状态码链路；未标记客户端流式或间隔为 0 时是 no-op。
	stopCompactKeepalive := service.StartOpenAICompactSSEKeepalive(c, h.openAICompactKeepaliveInterval())
	defer stopCompactKeepalive()

	// 校验请求体 JSON 合法性
	if !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 用户提示词替换必须在 compact 归一化之后、模型解析之前执行。
	body = h.gatewayService.ApplyUserPromptReplacement(c.Request.Context(), body, "openai_responses")
	sessionHashBody := body

	// 使用 gjson 只读提取字段做校验，避免完整 Unmarshal
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	if cappedBody, changed, policyErr := applyOpenAIReasoningEffortPolicyForRequest(c, apiKey, body); policyErr != nil {
		respondOpenAIReasoningEffortPolicyError(c, policyErr, h.errorResponse)
		return
	} else if changed {
		body = cappedBody
	}
	if normalizedBody, changed := normalizeCodexAutomationBootstrap(body); changed {
		body = normalizedBody
		reqLog.Info("openai.codex_automation_bootstrap_normalized",
			zap.String("normalization", "call_output_to_user_message"),
		)
	}
	if normalizedBody, changed := normalizeCodexDelegationBootstrap(body); changed {
		body = normalizedBody
		reqLog.Info("openai.codex_delegation_bootstrap_normalized",
			zap.String("normalization", "call_output_to_user_message"),
		)
	}

	reqStream, ok := parseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", invalidStreamFieldTypeMessage)
		return
	}
	if _, err := service.ValidateOpenAIServiceTierField(body); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	previousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if previousResponseID != "" {
		previousResponseIDKind := service.ClassifyOpenAIPreviousResponseIDKind(previousResponseID)
		reqLog = reqLog.With(
			zap.Bool("has_previous_response_id", true),
			zap.String("previous_response_id_kind", previousResponseIDKind),
			zap.Int("previous_response_id_len", len(previousResponseID)),
		)
		if previousResponseIDKind == service.OpenAIPreviousResponseIDKindMessageID {
			reqLog.Warn("openai.request_validation_failed",
				zap.String("reason", "previous_response_id_looks_like_message_id"),
			)
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "previous_response_id must be a response.id (resp_*), not a message id")
			return
		}
		groupID := int64(0)
		if apiKey.GroupID != nil {
			groupID = *apiKey.GroupID
		}
		owned, ownershipErr := h.gatewayService.ValidateOpenAIHTTPResponseOwner(
			c.Request.Context(),
			groupID,
			previousResponseID,
			subject.UserID,
			apiKey.ID,
		)
		if ownershipErr != nil {
			reqLog.Warn("openai.previous_response_owner_lookup_failed", zap.Error(ownershipErr))
		}
		if !owned {
			reqLog.Warn("openai.request_validation_failed", zap.String("reason", "previous_response_owner_mismatch"))
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "previous_response_id is not available for this user")
			return
		}
	}
	service.SetOpenAIHTTPResponseOwner(c, subject.UserID, apiKey.ID)

	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))
	setOpenAICyberWarningRequestSnapshot(c, service.ContentModerationProtocolOpenAIResponses, body)

	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	// 渠道模型 C 决定生图并发和账号端点能力，客户端模型 R 继续用于日志与会话语义。
	channelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	forwardBody, routingModel, forwardImageIntent := resolveOpenAIChannelMappedImageIntent(
		"/v1/responses", reqModel, body, channelMapping, openAICompatibleRequestPlatform(apiKey), h.gatewayService.ReplaceModelInBody,
	)
	forwardModel := strings.TrimSpace(routingModel)
	if forwardModel == "" {
		forwardModel = reqModel
	}
	// 权限、并发和账号能力只看显式意图；宽泛意图继续供转发层处理工具与计费。
	imageIntent := service.IsExplicitImageGenerationIntent("/v1/responses", routingModel, forwardBody)
	// 只有 HTTP Responses 入口会按账号开关进入自动透传，供 upstream 限制计算真实模型。
	selectionCtx := service.WithOpenAIHTTPPassthroughRouting(c.Request.Context())
	// 错误诊断也必须看到相同入口语义，避免把可透传模型误报为 model_not_found。
	c.Request = c.Request.WithContext(selectionCtx)
	if imageIntent {
		// 生图家族限流依赖上下文标记，必须使用渠道映射后的显式意图结果。
		selectionCtx = service.WithOpenAIImageGenerationIntent(selectionCtx)
	}
	if imageIntent && !service.GroupAllowsResponsesImages(apiKey.Group) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		h.errorResponse(c, http.StatusForbidden, "permission_error", service.ImageGenerationPermissionMessage())
		return
	}
	var imageReleaseFunc func()
	if imageIntent {
		var imageAcquired bool
		imageReleaseFunc, imageAcquired = h.acquireImageGenerationSlot(c, streamStarted)
		if !imageAcquired {
			return
		}
		if imageReleaseFunc != nil {
			defer imageReleaseFunc()
		}
	}

	seedOpenAIForwardImageIntentHint(c, channelMapping.Mapped, forwardImageIntent)

	// 提前校验 function_call_output 是否具备可关联上下文，避免上游 400。
	if !h.validateFunctionCallOutputRequest(c, body, reqLog) {
		return
	}

	// 绑定错误透传服务，允许 service 层在非 failover 错误场景复用规则。
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	// Get subscription info (may be nil)
	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	requestPlatform := openAICompatibleRequestPlatform(apiKey)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	// 确保请求取消时也会释放槽位，避免长连接被动中断造成泄漏
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 2. Re-check billing eligibility after wait
	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	// Generate session hash (header first; fallback to prompt_cache_key)
	explicitSessionHash := h.gatewayService.GenerateExplicitSessionHash(c, sessionHashBody)
	sessionHash := h.gatewayService.GenerateSessionHash(c, sessionHashBody)
	if h.rejectIfCyberSessionBlocked(c, apiKey, sessionHashBody, reqModel, cyberBlockFormatResponses) {
		return
	}
	if explicitSessionHash != "" {
		if err := h.ensureOpenAISessionIsolation(c.Request.Context(), apiKey, subject.UserID, service.SessionIsolationSourceOpenAI, explicitSessionHash); h.handleOpenAISessionIsolationError(c, err, streamStarted) {
			return
		}
	}
	c.Request = c.Request.WithContext(service.WithOpenAIGuardianParentAffinity(
		c.Request.Context(), c, sessionHashBody, reqModel,
	))
	requireCompact := legacyCompact

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	firstOutputTimeoutSwitchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	var passthroughFailoverState openAIPassthroughFailoverState

	// 生图意图的 /v1/responses 请求必须调度到确实支持 Responses API 的账号，否则
	// 会在 forward 阶段被静默降级为无法生图的 Chat Completions 直转（#4417）。
	// 仅对 OpenAI 平台生效：Grok 生图走独立的 forwardGrokResponses 路径，不应被过滤。
	// 复用前置权限与并发阶段按渠道模型 C 和未再修改的 forwardBody 确认的显式生图意图，
	// 避免大 tools 请求重复扫描。
	// 该判断已排除 Codex 被动 image_gen namespace，避免 CC-only 账号被误过滤（#4476）。
	requiredCapability := openAIResponsesRequiredCapabilityForRequest(
		imageIntent,
		nativeCompactionV2,
		legacyCompact,
		requestPlatform,
	)

	for {
		// 流式 Forward 会主动分离上游请求，以便客户端断开后继续回收用量；每次账号尝试前
		// 重新检查客户端上下文，避免已取消请求启动 failover 重放。
		if !openAIRequestAllowsFailoverReplay(c) {
			return
		}
		// Select account supporting the requested model
		reqLog.Debug("openai.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
			selectionCtx,
			apiKey.GroupID,
			previousResponseID,
			sessionHash,
			reqModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportAny,
			requiredCapability,
			requireCompact,
			false,
			requestPlatform,
		)
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("openai.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("openai.account_select_failed",
				zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				if legacyCompact && errors.Is(err, service.ErrNoAvailableCompactAccounts) {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
					h.handleStreamingAwareError(c, http.StatusServiceUnavailable, "compact_not_supported", "No available accounts support /responses/compact", streamStarted)
					return
				}
				cls := classifyNoAccountErrorFromGin(c, h.gatewayService, apiKey, reqModel, reqModel, requestPlatform)
				cls = classifySelectionFailureError(err, cls)
				if !cls.ModelNotFound {
					markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
				}
				h.handleStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
				return
			}
			if lastFailoverErr != nil {
				h.handleFailoverExhausted(c, lastFailoverErr, streamStarted)
			} else {
				h.handleFailoverExhaustedSimple(c, 502, streamStarted)
			}
			return
		}
		if selection == nil || selection.Account == nil {
			cls := classifyOpenAICompatibleNoAccountErrorFromGin(c, h.gatewayService, apiKey, reqModel, reqModel)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			h.handleStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
			return
		}
		if previousResponseID != "" && selection != nil && selection.Account != nil {
			reqLog.Debug("openai.account_selected_with_previous_response_id", zap.Int64("account_id", selection.Account.ID))
		}
		reqLog.Debug("openai.account_schedule_decision",
			zap.String("layer", scheduleDecision.Layer),
			zap.Bool("sticky_previous_hit", scheduleDecision.StickyPreviousHit),
			zap.Bool("sticky_session_hit", scheduleDecision.StickySessionHit),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
			zap.Int("top_k", scheduleDecision.TopK),
			zap.Int64("latency_ms", scheduleDecision.LatencyMs),
			zap.Float64("load_skew", scheduleDecision.LoadSkew),
		)
		account := selection.Account
		if previousResponseID != "" && requestPlatform == service.PlatformOpenAI && !account.IsOpenAIApiKey() {
			// The public Responses HTTP API supports previous_response_id on API-key
			// accounts. OAuth/SetupToken upstreams do not, so keep searching instead
			// of silently deleting continuation state from a mixed account pool.
			failedAccountIDs[account.ID] = struct{}{}
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
				selection.ReleaseFunc = nil
			}
			lastFailoverErr = &service.UpstreamFailoverError{
				StatusCode:       http.StatusBadRequest,
				Stage:            service.GatewayFailureStageInference,
				Scope:            service.GatewayFailureScopeRequest,
				Reason:           service.OpenAIHTTPContinuationUnsupportedReason,
				ClientStatusCode: http.StatusBadRequest,
				ClientMessage:    "previous_response_id requires an OpenAI API-key account for HTTP requests",
			}
			reqLog.Debug("openai.account_skipped_http_continuation_unsupported",
				zap.Int64("account_id", account.ID),
				zap.String("account_type", account.Type),
			)
			continue
		}
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		reqLog.Debug("openai.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
		setOpsSelectedAccount(c, account.ID, account.Platform)

		accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, reqStream, &streamStarted, reqLog)
		if !acquired {
			return
		}

		// Forward request
		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()
		// 用扣除非语义心跳字节的口径快照：心跳注释不构成语义响应，
		// 不能因心跳字节变化而放弃 failover 换号（#3887）。
		writerSizeBeforeForward := service.OpenAICompactKeepaliveAdjustedWrittenSize(c)
		// 跨透传边界时，从不可变的 canonical 请求体派生当前尝试体，
		// 避免非透传上游拒绝透传账号产生的私有加密 reasoning 项。
		attemptBody := h.deriveOpenAIForwardAttemptBody(reqLog, forwardBody, account, &passthroughFailoverState)
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			return h.gatewayService.Forward(c.Request.Context(), c, account, attemptBody)
		}()
		var cyberBlockBodyHTTP []byte
		if service.GetOpsCyberPolicy(c) != nil {
			cyberBlockBodyHTTP = sessionHashBody
		}
		cyberPolicyHandled := h.recordCyberPolicyIfMarked(c, apiKey, account, subscription, reqModel, err != nil, cyberBlockBodyHTTP, clientRequestedUsageFields(c, channelMapping, reqModel, ""), service.HashUsageRequestPayload(body), nativeCompactionV2)
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if err == nil && result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		// 错误路径可能携带断开排水或流中断前已累计的 usage；与成功路径共用同一
		// 提交函数，避免上游已计费而平台漏记。failover 的 nil result 自动跳过。
		submitResponsesUsage := func(res *service.OpenAIForwardResult) {
			if res == nil {
				return
			}
			stampOpenAIRequestedReasoningEffort(res, c)
			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			requestPayloadHash := service.HashUsageRequestPayload(body)
			inboundEndpoint := GetInboundEndpoint(c)
			upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, res)
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
			clientSessionID := service.ExtractClientSessionID(c)
			h.submitOpenAIUsageRecordTask(c, res, func(ctx context.Context) {
				if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
					Result:             res,
					APIKey:             apiKey,
					User:               apiKey.User,
					Account:            account,
					Subscription:       subscription,
					InboundEndpoint:    inboundEndpoint,
					UpstreamEndpoint:   upstreamEndpoint,
					UserAgent:          userAgent,
					IPAddress:          clientIP,
					RequestPayloadHash: requestPayloadHash,
					RequestBody:        body,
					APIKeyService:      h.apiKeyService,
					QuotaPlatform:      quotaPlatform,
					ClientSessionID:    clientSessionID,
					ChannelUsageFields: channelMapping.ToUsageFields(reqModel, res.UpstreamModel),
					CyberBlocked:       cyberPolicyHandled,
					NativeCompactionV2: nativeCompactionV2,
				}); err != nil {
					logger.L().With(
						zap.String("component", "handler.openai_gateway.responses"),
						zap.Int64("user_id", subject.UserID),
						zap.Int64("api_key_id", apiKey.ID),
						zap.Any("group_id", apiKey.GroupID),
						zap.String("model", reqModel),
						zap.Int64("account_id", account.ID),
					).Error("openai.record_usage_failed", zap.Error(err))
				}
			})
		}
		if err != nil {
			if result != nil && result.ImageCount > 0 {
				reqLog.Warn("openai.forward_partial_error_with_image_result",
					zap.Int64("account_id", account.ID),
					zap.Int("image_count", result.ImageCount),
					zap.Error(err),
				)
			} else {
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					if failoverClientGone(c) {
						reqLog.Info("openai.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					h.recordOpenAICyberWarning(c, reqLog, apiKey, account, reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
					if !openAIForwardMayFailover(c, writerSizeBeforeForward, failoverErr) {
						h.gatewayService.ObserveOpenAIAccountHealthFailure(c.Request.Context(), account, err)
						h.handleFailoverExhausted(c, failoverErr, true)
						return
					}
					// openAIForwardMayFailover 已确认写出的字节不含语义输出，
					// 但重试耗尽时仍须按已提交的 SSE 响应返回流内错误。
					if c.Writer.Written() {
						streamStarted = true
					}
					if failoverErr.ShouldReportAccountScheduleFailure() {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, forwardModel, requireCompact, nil), false, nil, err)
					}
					if !failoverErr.ShouldRetryNextAccount() {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					if openAIFirstOutputFailoverExhausted(failoverErr, &firstOutputTimeoutSwitchCount) {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					// 池模式：同账号重试
					if failoverErr.RetryableOnSameAccount {
						retryLimit := effectiveSameAccountRetryLimit(failoverErr, account)
						if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
							sameAccountRetryCount[account.ID]++
							retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
							reqLog.Warn("openai.pool_mode_same_account_retry",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
								zap.Int("retry_limit", retryLimit),
								zap.Int("retry_count", sameAccountRetryCount[account.ID]),
								zap.Duration("retry_delay", retryDelay),
							)
							select {
							case <-c.Request.Context().Done():
								return
							case <-time.After(retryDelay):
							}
							continue
						}
					}
					h.gatewayService.RecordOpenAIAccountSwitchForSelection(selection)
					failedAccountIDs[account.ID] = struct{}{}
					lastFailoverErr = failoverErr
					if switchCount >= maxAccountSwitches {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					switchCount++
					if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
						h.handleFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					failoverSwitchFields := []zap.Field{
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					}
					failoverSwitchFields = appendOpenAIAccountProxyLogFields(failoverSwitchFields, account)
					reqLog.Warn("openai.upstream_failover_switching", failoverSwitchFields...)
					continue
				}
				statusCode := 0
				if v, ok := getContextInt64(c, service.OpsUpstreamStatusCodeKey); ok {
					statusCode = int(v)
				}
				recordedWarning := h.recordOpenAIForwardErrorCyberWarning(c, reqLog, apiKey, account, reqModel, statusCode, err)
				if !recordedWarning {
					h.recordOpenAICyberWarning(c, reqLog, apiKey, account, reqModel, statusCode, nil, err.Error())
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, forwardModel, requireCompact, result), false, nil, err)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
				wroteFallback := false
				// cyber warning 场景下，service 层可能已经把上游 response.failed/JSON 错误写给下游。
				// 此时不再补写第二个 fallback，避免客户端看到重复的终止事件。
				if !upstreamErrorAlreadyCommunicated && (!recordedWarning || service.OpenAICompactKeepaliveAdjustedWrittenSize(c) == writerSizeBeforeForward) {
					wroteFallback = h.ensureOpenAIForwardErrorResponse(c, streamStarted, err)
				}
				fields := []zap.Field{
					zap.Int64("account_id", account.ID),
					zap.Bool("fallback_error_response_written", wroteFallback),
					zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
					zap.Error(err),
				}
				submitResponsesUsage(result)
				if shouldLogOpenAIForwardFailureAsWarn(c, wroteFallback) {
					reqLog.Warn("openai.forward_failed", fields...)
					return
				}
				reqLog.Error("openai.forward_failed", fields...)
				return
			}
		}
		if result != nil {
			// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
			if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
				h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(c.Request.Context(), account.ID, result.ResponseHeaders)
			}
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, forwardModel, requireCompact, result), openAIForwardSucceededForScheduling(result), result.FirstTokenMs)
		} else {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, forwardModel, requireCompact, result), openAIForwardSucceededForScheduling(result), nil)
		}

		submitResponsesUsage(result)
		reqLog.Debug("openai.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
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

// Messages handles Anthropic Messages API requests routed to OpenAI platform.
// POST /v1/messages (when group platform is OpenAI)
func (h *OpenAIGatewayHandler) Messages(c *gin.Context) {
	streamStarted := false
	defer h.recoverAnthropicMessagesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := requestLogger(
		c,
		"handler.openai_gateway.messages",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// 检查分组是否允许 /v1/messages 调度
	if !allowOpenAICompatibleMessagesDispatch(apiKey) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
		h.anthropicErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group does not allow Anthropic Messages requests")
		return
	}

	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}

	body, err := readLenientJSONRequestBodyWithPrealloc(c.Request, h.cfg)
	if err != nil {
		if maxErr, ok := extractMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", buildBodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if !gjson.ValidBytes(body) {
		logRequestBodyParseFailure(reqLog, body, nil)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 用户提示词替换必须早于模型解析、内容审计和会话 hash，确保后续链路看到同一份请求体。
	body = h.gatewayService.ApplyUserPromptReplacement(c.Request.Context(), body, "anthropic_messages")

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	bindOpenAIReasoningEffortPolicyForMessagesRequest(c, apiKey, body)
	reqStream := gjson.GetBytes(body, "stream").Bool()

	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	setOpsRequestContext(c, reqModel, reqStream)
	setOpsEndpointContext(c, "", int16(service.RequestTypeFromLegacy(reqStream, false)))
	setOpenAICyberWarningRequestSnapshot(c, service.ContentModerationProtocolAnthropicMessages, body)

	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolAnthropicMessages, reqModel, body); decision != nil && decision.Blocked {
		h.anthropicErrorResponse(c, contentModerationStatus(decision), contentModerationErrorCode(decision), decision.Message)
		return
	}

	// 解析渠道级模型映射
	channelMappingMsg, _ := h.gatewayService.ResolveChannelMappingAndRestrict(c.Request.Context(), apiKey.GroupID, reqModel)
	channelMappedModel := strings.TrimSpace(channelMappingMsg.MappedModel)
	if channelMappedModel == "" {
		channelMappedModel = reqModel
	}
	accountLayerModel := resolveOpenAIMessagesAccountLayerModelForRequest(c.Request.Context(), apiKey, channelMappedModel)
	mappedBodyForMessages := newOpenAIModelMappedBodyCache(body, h.gatewayService.ReplaceModelInBody)

	// 绑定错误透传服务，允许 service 层在非 failover 错误场景复用规则。
	if h.errorPassthroughService != nil {
		service.BindErrorPassthroughService(c, h.errorPassthroughService)
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	requestPlatform := openAICompatibleRequestPlatform(apiKey)

	service.SetOpsLatencyMs(c, service.OpsAuthLatencyMsKey, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.acquireResponsesUserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	if err := h.billingCacheService.CheckBillingEligibility(c.Request.Context(), apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai_messages.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := billingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.anthropicStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	sessionHash := h.gatewayService.GenerateSessionHash(c, body)
	promptCacheKey := h.gatewayService.ExtractSessionID(c, body)
	sessionHash, promptCacheKey = resolveOpenAIMessagesMetadataSession(c, sessionHash, promptCacheKey, reqModel, body)
	if h.rejectIfCyberSessionBlocked(c, apiKey, body, reqModel, cyberBlockFormatAnthropic) {
		return
	}
	isolationSource := service.SessionIsolationSourceOpenAI
	explicitSessionHash := h.gatewayService.GenerateExplicitSessionHash(c, body)
	if explicitSessionHash == "" {
		if isolationSessionID := metadataSessionIsolationID(gjson.GetBytes(body, "metadata.user_id").String()); isolationSessionID != "" {
			explicitSessionHash = isolationSessionID
			isolationSource = service.SessionIsolationSourceGateway
		}
	}
	if explicitSessionHash != "" {
		if err := h.ensureOpenAISessionIsolation(c.Request.Context(), apiKey, subject.UserID, isolationSource, explicitSessionHash); h.handleAnthropicSessionIsolationError(c, err, streamStarted) {
			return
		}
	}

	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	sameAccountRetryCount := make(map[int64]int)
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	currentRoutingModel := accountLayerModel

	for {
		if failoverClientGone(c) {
			return
		}
		reqLog.Debug("openai_messages.account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapabilityAndRoutingModel(
			c.Request.Context(),
			apiKey.GroupID,
			"", // no previous_response_id
			sessionHash,
			reqModel,
			accountLayerModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportAny,
			service.OpenAIEndpointCapabilityTextGeneration,
			false,
			false,
			requestPlatform,
		)
		if err != nil {
			if failoverClientGone(c) {
				reqLog.Info("openai_messages.account_select_aborted_client_disconnected", zap.Error(err))
				return
			}
			reqLog.Warn("openai_messages.account_select_failed",
				zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if len(failedAccountIDs) == 0 {
				if err != nil {
					if h.handleOpenAISelectionBusinessError(c, err, streamStarted) {
						return
					}
					cls := classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin(c, h.gatewayService, apiKey, accountLayerModel, reqModel)
					if !cls.ModelNotFound {
						markOpsRoutingCapacityLimitedIfNoAvailable(c, err)
					}
					h.anthropicStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
					return
				}
			} else {
				if lastFailoverErr != nil {
					h.handleAnthropicFailoverExhausted(c, lastFailoverErr, streamStarted)
				} else {
					h.anthropicStreamingAwareError(c, http.StatusBadGateway, "api_error", "Upstream request failed", streamStarted)
				}
				return
			}
		}
		if selection == nil || selection.Account == nil {
			cls := classifyOpenAICompatibleResolvedRoutingNoAccountErrorFromGin(c, h.gatewayService, apiKey, accountLayerModel, reqModel)
			if !cls.ModelNotFound {
				markOpsRoutingCapacityLimited(c)
			}
			h.anthropicStreamingAwareError(c, cls.Status, cls.ErrType, cls.Message, streamStarted)
			return
		}
		account := selection.Account
		sessionHash = ensureOpenAIPoolModeSessionHash(sessionHash, account)
		reqLog.Debug("openai_messages.account_selected", zap.Int64("account_id", account.ID), zap.String("account_name", account.Name))
		_ = scheduleDecision
		setOpsSelectedAccount(c, account.ID, account.Platform)

		accountReleaseFunc, acquired := h.acquireResponsesAccountSlot(c, apiKey.GroupID, sessionHash, selection, reqStream, &streamStarted, reqLog)
		if !acquired {
			return
		}

		service.SetOpsLatencyMs(c, service.OpsRoutingLatencyMsKey, time.Since(routingStart).Milliseconds())
		forwardStart := time.Now()

		// 应用渠道模型映射到请求体
		forwardBody := mappedBodyForMessages(channelMappingMsg.Mapped, channelMappingMsg.MappedModel)
		writerSizeBeforeForward := c.Writer.Size()
		result, err := func() (*service.OpenAIForwardResult, error) {
			defer func() {
				if accountReleaseFunc != nil {
					accountReleaseFunc()
				}
			}()
			tlsRouterMatch := h.gatewayService.MatchOpenAITLSFingerprintRouterForRequest(c, account)
			if err := h.gatewayService.EnforceOpenAIClientPolicyForRequest(c.Request.Context(), c, account, forwardBody, tlsRouterMatch); err != nil {
				return nil, err
			}
			return h.gatewayService.ForwardAsAnthropic(c.Request.Context(), c, account, forwardBody, promptCacheKey, accountLayerModel, tlsRouterMatch)
		}()
		var cyberBlockBodyMsg []byte
		if service.GetOpsCyberPolicy(c) != nil {
			cyberBlockBodyMsg = body
		}
		cyberPolicyHandled := h.recordCyberPolicyIfMarked(c, apiKey, account, subscription, reqModel, err != nil, cyberBlockBodyMsg, clientRequestedUsageFields(c, channelMappingMsg, reqModel, ""), service.HashUsageRequestPayload(body))
		forwardDurationMs := time.Since(forwardStart).Milliseconds()
		upstreamLatencyMs, _ := getContextInt64(c, service.OpsUpstreamLatencyMsKey)
		responseLatencyMs := forwardDurationMs
		if upstreamLatencyMs > 0 && forwardDurationMs > upstreamLatencyMs {
			responseLatencyMs = forwardDurationMs - upstreamLatencyMs
		}
		service.SetOpsLatencyMs(c, service.OpsResponseLatencyMsKey, responseLatencyMs)
		if err == nil && result != nil && result.FirstTokenMs != nil {
			service.SetOpsLatencyMs(c, service.OpsTimeToFirstTokenMsKey, int64(*result.FirstTokenMs))
		}
		// Messages 错误结果中的部分 usage 已由上游计量，不能因下游断开而丢弃。
		submitMessagesUsage := func(res *service.OpenAIForwardResult) {
			if res == nil {
				return
			}
			stampOpenAIRequestedReasoningEffort(res, c)
			userAgent := c.GetHeader("User-Agent")
			clientIP := ip.GetClientIP(c)
			requestPayloadHash := service.HashUsageRequestPayload(body)
			inboundEndpoint := GetInboundEndpoint(c)
			upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, res)
			quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
			clientSessionID := service.ExtractClientSessionID(c)
			h.submitOpenAIUsageRecordTask(c, res, func(ctx context.Context) {
				if err := h.gatewayService.RecordUsage(ctx, &service.OpenAIRecordUsageInput{
					Result:             res,
					APIKey:             apiKey,
					User:               apiKey.User,
					Account:            account,
					Subscription:       subscription,
					InboundEndpoint:    inboundEndpoint,
					UpstreamEndpoint:   upstreamEndpoint,
					UserAgent:          userAgent,
					IPAddress:          clientIP,
					RequestPayloadHash: requestPayloadHash,
					RequestBody:        body,
					APIKeyService:      h.apiKeyService,
					QuotaPlatform:      quotaPlatform,
					ClientSessionID:    clientSessionID,
					ChannelUsageFields: channelMappingMsg.ToUsageFields(reqModel, res.UpstreamModel),
					CyberBlocked:       cyberPolicyHandled,
				}); err != nil {
					logger.L().With(
						zap.String("component", "handler.openai_gateway.messages"),
						zap.Int64("user_id", subject.UserID),
						zap.Int64("api_key_id", apiKey.ID),
						zap.Any("group_id", apiKey.GroupID),
						zap.String("model", reqModel),
						zap.Int64("account_id", account.ID),
					).Error("openai_messages.record_usage_failed", zap.Error(err))
				}
			})
		}
		if err != nil {
			if result != nil && result.ImageCount > 0 {
				reqLog.Warn("openai_messages.forward_partial_error_with_image_result",
					zap.Int64("account_id", account.ID),
					zap.Int("image_count", result.ImageCount),
					zap.Error(err),
				)
			} else {
				// 该错误已由 service 层写入本地 403；不把策略拒绝当成账号故障。
				var overLimit *service.ReasoningEffortOverLimitError
				if errors.As(err, &overLimit) {
					reqLog.Info("openai_messages.reasoning_effort_policy_denied",
						zap.String("reason", overLimit.Error()),
					)
					return
				}
				var failoverErr *service.UpstreamFailoverError
				if errors.As(err, &failoverErr) {
					if failoverClientGone(c) {
						reqLog.Info("openai_messages.failover_aborted_client_disconnected",
							zap.Int64("account_id", account.ID),
							zap.Int("upstream_status", failoverErr.StatusCode),
						)
						return
					}
					h.recordOpenAICyberWarning(c, reqLog, apiKey, account, reqModel, failoverErr.StatusCode, failoverErr.ResponseBody, err.Error())
					if c.Writer.Size() != writerSizeBeforeForward {
						h.gatewayService.ObserveOpenAIAccountHealthFailure(c.Request.Context(), account, err)
						h.handleAnthropicFailoverExhausted(c, failoverErr, true)
						return
					}
					if failoverErr.ShouldReportAccountScheduleFailure() {
						h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, currentRoutingModel, false, nil), false, nil, err)
					}
					if !failoverErr.ShouldRetryNextAccount() {
						h.handleAnthropicFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					// 池模式：同账号重试
					if failoverErr.RetryableOnSameAccount {
						retryLimit := effectiveSameAccountRetryLimit(failoverErr, account)
						if sameAccountRetryAllowed(failoverErr, sameAccountRetryCount[account.ID], retryLimit) {
							sameAccountRetryCount[account.ID]++
							retryDelay := sameAccountRetryDelayFor(failoverErr, sameAccountRetryCount[account.ID])
							reqLog.Warn("openai_messages.pool_mode_same_account_retry",
								zap.Int64("account_id", account.ID),
								zap.Int("upstream_status", failoverErr.StatusCode),
								zap.Int("retry_limit", retryLimit),
								zap.Int("retry_count", sameAccountRetryCount[account.ID]),
								zap.Duration("retry_delay", retryDelay),
							)
							select {
							case <-c.Request.Context().Done():
								return
							case <-time.After(retryDelay):
							}
							continue
						}
					}
					h.gatewayService.RecordOpenAIAccountSwitchForSelection(selection)
					failedAccountIDs[account.ID] = struct{}{}
					lastFailoverErr = failoverErr
					if switchCount >= maxAccountSwitches {
						h.handleAnthropicFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					switchCount++
					if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
						h.handleAnthropicFailoverExhausted(c, failoverErr, streamStarted)
						return
					}
					reqLog.Warn("openai_messages.upstream_failover_switching",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("switch_count", switchCount),
						zap.Int("max_switches", maxAccountSwitches),
					)
					continue
				}
				if result != nil && result.ClientDisconnect {
					reqLog.Info("openai_messages.client_disconnected",
						zap.Int64("account_id", account.ID),
						zap.Error(err),
					)
					submitMessagesUsage(result)
					return
				}
				statusCode := 0
				if v, ok := getContextInt64(c, service.OpsUpstreamStatusCodeKey); ok {
					statusCode = int(v)
				}
				recordedWarning := h.recordOpenAIForwardErrorCyberWarning(c, reqLog, apiKey, account, reqModel, statusCode, err)
				if !recordedWarning {
					h.recordOpenAICyberWarning(c, reqLog, apiKey, account, reqModel, statusCode, nil, err.Error())
				}
				h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, currentRoutingModel, false, result), false, nil, err)
				upstreamErrorAlreadyCommunicated := openAIForwardErrorAlreadyCommunicated(c, writerSizeBeforeForward, err)
				wroteFallback := false
				if !upstreamErrorAlreadyCommunicated && (!recordedWarning || c.Writer.Size() == writerSizeBeforeForward) {
					wroteFallback = h.ensureAnthropicErrorResponse(c, streamStarted)
				}
				reqLog.Warn("openai_messages.forward_failed",
					zap.Int64("account_id", account.ID),
					zap.Bool("fallback_error_response_written", wroteFallback),
					zap.Bool("upstream_error_response_already_written", upstreamErrorAlreadyCommunicated),
					zap.Error(err),
				)
				submitMessagesUsage(result)
				return
			}
		}
		if result != nil {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, currentRoutingModel, false, result), true, result.FirstTokenMs)
		} else {
			h.gatewayService.ReportOpenAIAccountScheduleResult(account, openAIAccountScheduleModel(c, account, currentRoutingModel, false, result), true, nil)
		}

		submitMessagesUsage(result)
		reqLog.Debug("openai_messages.request_completed",
			zap.Int64("account_id", account.ID),
			zap.Int("switch_count", switchCount),
		)
		return
	}
}

func resolveOpenAIMessagesMetadataSession(c *gin.Context, sessionHash, promptCacheKey, reqModel string, body []byte) (string, string) {
	// Anthropic metadata.user_id 只作为账号粘性信号。上游 GPT/Codex 缓存键
	// 交给 ForwardAsAnthropic 从 cache_control 或完整消息 digest 派生，避免
	// 固定 metadata key 压住后续 turn 的缓存滚动。
	//
	// Claude Code 的 X-Claude-Code-Session-Id 是比 body content fallback 更稳定的
	// 会话边界，但它只用于本地账号粘性；不要把它提升为 prompt_cache_key 或上游
	// session_id，否则会改变现有 Messages→Codex 缓存滚动语义。
	if promptCacheKey == "" {
		if claudeSessionID := service.ClaudeCodeSessionIDFromHeader(c); claudeSessionID != "" {
			return service.DeriveSessionHashFromSeed(claudeSessionID), promptCacheKey
		}
	}
	if sessionHash != "" {
		return sessionHash, promptCacheKey
	}
	if userID := strings.TrimSpace(gjson.GetBytes(body, "metadata.user_id").String()); userID != "" {
		seed := reqModel + "-" + userID
		sessionHash = service.DeriveSessionHashFromSeed(seed)
	}
	return sessionHash, promptCacheKey
}

// anthropicErrorResponse writes an error in Anthropic Messages API format.
func (h *OpenAIGatewayHandler) anthropicErrorResponse(c *gin.Context, status int, errType, message string) {
	c.JSON(status, gin.H{
		"type": "error",
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
}

// anthropicStreamingAwareError handles errors that may occur during streaming,
// using Anthropic SSE error format.
func (h *OpenAIGatewayHandler) anthropicStreamingAwareError(c *gin.Context, status int, errType, message string, streamStarted bool) {
	if streamStarted {
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errPayload, _ := json.Marshal(gin.H{
				"type": "error",
				"error": gin.H{
					"type":    errType,
					"message": message,
				},
			})
			fmt.Fprintf(c.Writer, "event: error\ndata: %s\n\n", errPayload) //nolint:errcheck
			flusher.Flush()
		}
		return
	}
	h.anthropicErrorResponse(c, status, errType, message)
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

func normalizeCodexDelegationBootstrap(body []byte) ([]byte, bool) {
	// 已有任务通过 send_message_to_thread 唤醒时会携带 previous_response_id；
	// 完整历史回放还会带有已配对的调用项。delegation 仍是客户端注入的用户输入，
	// 不属于这些历史调用的结果，因此允许它与可明确配对的历史上下文共存。
	return normalizeCodexCallOutputBootstrap(body, isCodexDelegationCandidate, true)
}

func normalizeCodexAutomationBootstrap(body []byte) ([]byte, bool) {
	return normalizeCodexCallOutputBootstrap(body, isCodexAutomationCandidate, false)
}

func normalizeCodexCallOutputBootstrap(body []byte, isCandidate func(map[string]any) bool, allowHistoricalContext bool) ([]byte, bool) {
	if !hasUniqueJSONMembers(body) {
		return body, false
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.UseNumber()
	var request map[string]any
	if err := decoder.Decode(&request); err != nil {
		return body, false
	}
	if previousResponseID, exists := request["previous_response_id"]; exists {
		value, ok := previousResponseID.(string)
		if !ok || (!allowHistoricalContext && strings.TrimSpace(value) != "") {
			return body, false
		}
	}
	input, ok := request["input"].([]any)
	if !ok {
		return body, false
	}

	// Responses built-ins follow the *_call / *_call_output naming convention,
	// so classify by the wire type shape instead of maintaining an incomplete
	// allowlist. Delegation may coexist with historical anchors only when their
	// IDs make them unambiguous; automation retains the bootstrap-only boundary.
	for _, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		typ := stringField(item, "type")
		if isCandidate(item) {
			callIDValue, exists := item["call_id"]
			callID, isString := callIDValue.(string)
			if exists && (!isString || strings.TrimSpace(callID) != "") {
				return body, false
			}
			continue
		}
		if typ == "item_reference" {
			if allowHistoricalContext && strings.TrimSpace(stringField(item, "id")) != "" {
				continue
			}
			return body, false
		}
		if strings.HasSuffix(typ, "_call") || isResponsesCallOutputType(typ) {
			if allowHistoricalContext && strings.TrimSpace(stringField(item, "call_id")) != "" {
				continue
			}
			return body, false
		}
	}

	changed := false
	for i, raw := range input {
		item, ok := raw.(map[string]any)
		if !ok || !isCandidate(item) {
			continue
		}
		output, ok := item["output"].(string)
		if !ok {
			continue
		}
		input[i] = map[string]any{
			"type": "message",
			"role": "user",
			"content": []any{map[string]any{
				"type": "input_text",
				"text": output,
			}},
		}
		changed = true
	}
	if !changed {
		return body, false
	}
	normalized, err := json.Marshal(request)
	if err != nil {
		return body, false
	}
	return normalized, true
}

func hasUniqueJSONMembers(body []byte) bool {
	decoder := json.NewDecoder(bytes.NewReader(body))
	if !consumeUniqueJSONValue(decoder) {
		return false
	}
	_, err := decoder.Token()
	return err == io.EOF
}

func consumeUniqueJSONValue(decoder *json.Decoder) bool {
	token, err := decoder.Token()
	if err != nil {
		return false
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return true
	}

	switch delim {
	case '{':
		members := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return false
			}
			key, ok := keyToken.(string)
			if !ok {
				return false
			}
			if _, duplicate := members[key]; duplicate {
				return false
			}
			members[key] = struct{}{}
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim('}')
	case '[':
		for decoder.More() {
			if !consumeUniqueJSONValue(decoder) {
				return false
			}
		}
		end, err := decoder.Token()
		return err == nil && end == json.Delim(']')
	default:
		return false
	}
}

func isResponsesCallOutputType(typ string) bool {
	return strings.HasSuffix(typ, "_call_output") || typ == "tool_search_output"
}

func isCodexDelegationCandidate(item map[string]any) bool {
	if stringField(item, "type") != "function_call_output" ||
		!isCodexDelegationTool(stringField(item, "namespace"), stringField(item, "name")) {
		return false
	}
	output, ok := item["output"].(string)
	return ok && validCodexDelegationEnvelope(output)
}

func isCodexAutomationCandidate(item map[string]any) bool {
	if stringField(item, "type") != "function_call_output" ||
		stringField(item, "namespace") != "codex_app" ||
		stringField(item, "name") != "automation_update" {
		return false
	}
	output, ok := item["output"].(string)
	return ok && (validCodexAutomationBootstrap(output) || validCodexAutomationHeartbeat(output))
}

func stringField(item map[string]any, key string) string {
	value, _ := item[key].(string)
	return value
}

func isCodexDelegationTool(namespace, name string) bool {
	return (namespace == "codex_app" || namespace == "codex_tui") &&
		(name == "create_thread" || name == "send_message_to_thread")
}

func validCodexAutomationBootstrap(value string) bool {
	normalized := strings.ReplaceAll(value, "\r\n", "\n")
	if strings.ContainsRune(normalized, '\r') {
		return false
	}
	lines := strings.Split(normalized, "\n")
	if len(lines) < 6 {
		return false
	}
	if _, ok := codexAutomationHeaderValue(lines[0], "Automation: "); !ok {
		return false
	}
	automationID, ok := codexAutomationHeaderValue(lines[1], "Automation ID: ")
	if !ok || !validCodexAutomationID(automationID) {
		return false
	}
	expectedMemory := "Automation memory: $CODEX_HOME/automations/" + automationID + "/memory.md"
	if lines[2] != expectedMemory {
		return false
	}
	lastRun, ok := codexAutomationHeaderValue(lines[3], "Last run: ")
	if !ok || !validCodexAutomationLastRun(lastRun) || lines[4] != "" {
		return false
	}
	return strings.TrimSpace(strings.Join(lines[5:], "\n")) != ""
}

func codexAutomationHeaderValue(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	value := strings.TrimPrefix(line, prefix)
	return value, value != "" && strings.TrimSpace(value) == value
}

func validCodexAutomationID(value string) bool {
	if len(value) == 0 || len(value) > 128 || value == "." || value == ".." {
		return false
	}
	for i := 0; i < len(value); i++ {
		c := value[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_' || c == '.' {
			continue
		}
		return false
	}
	return true
}

func validCodexAutomationLastRun(value string) bool {
	if value == "never" {
		return true
	}
	separator := strings.LastIndex(value, " (")
	if separator <= 0 || !strings.HasSuffix(value, ")") {
		return false
	}
	runAt, err := time.Parse(time.RFC3339Nano, value[:separator])
	if err != nil {
		return false
	}
	epochMillis, err := strconv.ParseInt(value[separator+2:len(value)-1], 10, 64)
	return err == nil && runAt.UnixMilli() == epochMillis
}

func validCodexAutomationHeartbeat(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, automationIDSeen bool
	var automationID bytes.Buffer
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			id := automationID.String()
			return rootSeen && automationIDSeen && depth == 0 &&
				strings.TrimSpace(id) == id && validCodexAutomationID(id)
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen || current.Name.Local != "heartbeat" {
					return false
				}
				rootSeen = true
			} else if automationIDSeen || current.Name.Local != "automation_id" {
				return false
			}
			automationIDSeen = depth == 2
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = automationID.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment, xml.ProcInst, xml.Directive:
			return false
		}
	}
}

func validCodexDelegationEnvelope(value string) bool {
	decoder := xml.NewDecoder(strings.NewReader(value))
	var rootSeen, sourceSeen, inputSeen bool
	var childName string
	var childText bytes.Buffer
	depth := 0
	for {
		token, err := decoder.Token()
		if err == io.EOF {
			return rootSeen && depth == 0 && sourceSeen && inputSeen
		}
		if err != nil {
			return false
		}
		switch current := token.(type) {
		case xml.StartElement:
			depth++
			if current.Name.Space != "" || len(current.Attr) != 0 || (depth == 1 && current.Name.Local != "codex_delegation") || depth > 2 {
				return false
			}
			if depth == 1 {
				if rootSeen {
					return false
				}
				rootSeen = true
				continue
			}
			if current.Name.Local != "source_thread_id" && current.Name.Local != "input" {
				return false
			}
			childName = current.Name.Local
			childText.Reset()
		case xml.EndElement:
			if current.Name.Space != "" {
				return false
			}
			if depth == 2 {
				if current.Name.Local != childName || strings.TrimSpace(childText.String()) == "" {
					return false
				}
				if childName == "source_thread_id" {
					if sourceSeen {
						return false
					}
					sourceSeen = true
				} else {
					if inputSeen {
						return false
					}
					inputSeen = true
				}
				childName = ""
			}
			depth--
			if depth < 0 {
				return false
			}
		case xml.CharData:
			if depth == 2 {
				_, _ = childText.Write(current)
			} else if len(bytes.TrimSpace(current)) != 0 {
				return false
			}
		case xml.Comment:
			return false
		case xml.ProcInst, xml.Directive:
			return false
		}
	}
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
	if selection == nil || selection.Account == nil {
		markOpsRoutingCapacityLimited(c)
		writeError(http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		return nil, openAISlotAcquireFailed
	}

	ctx := c.Request.Context()
	account := selection.Account
	if selection.Acquired {
		service.MarkOpsAccountSlotAcquired(c)
		return wrapReleaseOnDone(ctx, selection.ReleaseFunc), openAISlotAcquireOK
	}
	if selection.WaitPlan == nil {
		markOpsRoutingCapacityLimited(c)
		writeError(http.StatusServiceUnavailable, "api_error", "", "No available accounts")
		return nil, openAISlotAcquireFailed
	}

	fastReleaseFunc, fastAcquired, err := h.concurrencyHelper.TryAcquireAccountSlot(
		ctx,
		account.ID,
		selection.WaitPlan.MaxConcurrency,
	)
	if err != nil {
		reqLog.Warn("openai.account_slot_quick_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		status, errType, code, message := concurrencyErrorResponse(err, "account")
		writeError(status, errType, code, message)
		return nil, openAISlotAcquireFailed
	}
	if fastAcquired {
		service.MarkOpsAccountSlotAcquired(c)
		if err := h.gatewayService.BindStickySession(ctx, groupID, sessionHash, account.ID); err != nil {
			reqLog.Warn("openai.bind_sticky_session_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}
		return wrapReleaseOnDone(ctx, fastReleaseFunc), openAISlotAcquireOK
	}

	canWait, waitErr := h.concurrencyHelper.IncrementAccountWaitCount(ctx, account.ID, selection.WaitPlan.MaxWaiting)
	if waitErr != nil {
		reqLog.Warn("openai.account_wait_counter_increment_failed", zap.Int64("account_id", account.ID), zap.Error(waitErr))
	} else if !canWait {
		reqLog.Info("openai.account_wait_queue_full",
			zap.Int64("account_id", account.ID),
			zap.Int("max_waiting", selection.WaitPlan.MaxWaiting),
		)
		writeError(http.StatusTooManyRequests, "rate_limit_error", gatewayQueueFullCode, "Too many pending requests, please retry later")
		return nil, openAISlotAcquireFailed
	}

	accountWaitCounted := waitErr == nil && canWait
	releaseWait := func() {
		if accountWaitCounted {
			h.concurrencyHelper.DecrementAccountWaitCount(ctx, account.ID)
			accountWaitCounted = false
		}
	}
	defer releaseWait()

	accountReleaseFunc, err := h.concurrencyHelper.AcquireAccountSlotWithWaitTimeout(
		c,
		account.ID,
		selection.WaitPlan.MaxConcurrency,
		selection.WaitPlan.Timeout,
		reqStream,
		streamStarted,
	)
	if err != nil {
		reqLog.Warn("openai.account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		status, errType, code, message := concurrencyErrorResponse(err, "account")
		writeError(status, errType, code, message)
		return nil, openAISlotAcquireFailed
	}

	// Slot acquired: no longer waiting in queue.
	releaseWait()
	service.MarkOpsAccountSlotAcquired(c)
	if err := h.gatewayService.BindStickySession(ctx, groupID, sessionHash, account.ID); err != nil {
		reqLog.Warn("openai.bind_sticky_session_failed", zap.Int64("account_id", account.ID), zap.Error(err))
	}
	return wrapReleaseOnDone(ctx, accountReleaseFunc), openAISlotAcquireOK
}

// ResponsesWebSocket handles OpenAI Responses API WebSocket ingress endpoint
// GET /openai/v1/responses (Upgrade: websocket)
func (h *OpenAIGatewayHandler) ResponsesWebSocket(c *gin.Context) {
	if !isOpenAIWSUpgradeRequest(c.Request) {
		h.errorResponse(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	setOpenAIClientTransportWS(c)

	apiKey, ok := middleware2.GetAPIKeyFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := middleware2.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := requestLogger(
		c,
		"handler.openai_gateway.responses_ws",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.Bool("openai_ws_mode", true),
	)
	if !h.ensureResponsesDependencies(c, reqLog) {
		return
	}
	reqLog.Info("openai.websocket_ingress_started")
	clientIP := ip.GetClientIP(c)
	userAgent := strings.TrimSpace(c.GetHeader("User-Agent"))
	clientLifecycleCtx := c.Request.Context()
	ctx := clientLifecycleCtx
	maxIngressConnections := 0
	if h.cfg != nil {
		maxIngressConnections = h.cfg.Gateway.OpenAIWS.MaxIngressConnectionsPerAPIKey
	}
	ingressLease, ingressLeaseAcquired, ingressLeaseErr := h.concurrencyHelper.AcquireOpenAIWSIngressLease(ctx, apiKey.ID, maxIngressConnections)
	if ingressLeaseErr != nil {
		reqLog.Error("openai.websocket_ingress_lease_acquire_failed", zap.Error(ingressLeaseErr))
		h.errorResponse(c, http.StatusServiceUnavailable, "service_unavailable", "WebSocket ingress capacity is temporarily unavailable")
		return
	}
	if !ingressLeaseAcquired {
		reqLog.Info("openai.websocket_ingress_capacity_rejected", zap.Int("max_ingress_connections_per_api_key", maxIngressConnections))
		c.Header("Retry-After", "5")
		h.errorResponse(c, http.StatusTooManyRequests, "rate_limit_error", "Too many open WebSocket connections, please retry later")
		return
	}
	if ingressLease != nil {
		defer ingressLease.Release()
		ctx = ingressLease.Context()
		c.Request = c.Request.WithContext(ctx)
	}

	wsConn, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{
		CompressionMode: coderws.CompressionContextTakeover,
	})
	if err != nil {
		reqLog.Warn("openai.websocket_accept_failed",
			zap.Error(err),
			zap.String("client_ip", clientIP),
			zap.String("request_user_agent", userAgent),
			zap.String("upgrade_header", strings.TrimSpace(c.GetHeader("Upgrade"))),
			zap.String("connection_header", strings.TrimSpace(c.GetHeader("Connection"))),
			zap.String("sec_websocket_version", strings.TrimSpace(c.GetHeader("Sec-WebSocket-Version"))),
			zap.Bool("has_sec_websocket_key", strings.TrimSpace(c.GetHeader("Sec-WebSocket-Key")) != ""),
		)
		return
	}
	defer func() {
		_ = wsConn.CloseNow()
	}()
	wsConn.SetReadLimit(service.ResolveOpenAIWSClientReadLimitBytes(h.cfg))

	firstMessageTimeout := service.ResolveOpenAIWSClientFirstMessageTimeout(h.cfg)
	msgType, firstMessage, err := service.ReadOpenAIWSClientMessage(
		ctx,
		wsConn,
		firstMessageTimeout,
		coderws.StatusPolicyViolation,
		"missing first response.create message",
	)
	if err != nil {
		if errors.Is(context.Cause(ctx), service.ErrOpenAIWSIngressLeaseLost) {
			reqLog.Warn("openai.websocket_ingress_lease_lost_before_first_message", zap.Error(err))
			closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
			return
		}
		closeStatus, closeReason := summarizeWSCloseErrorForLog(err)
		reqLog.Warn("openai.websocket_read_first_message_failed",
			zap.Error(err),
			zap.String("client_ip", clientIP),
			zap.String("close_status", closeStatus),
			zap.String("close_reason", closeReason),
			zap.Duration("read_timeout", firstMessageTimeout),
		)
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "missing first response.create message")
		return
	}
	firstTurnStartedAt := time.Now()
	if msgType != coderws.MessageText && msgType != coderws.MessageBinary {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "unsupported websocket message type")
		return
	}
	if !gjson.ValidBytes(firstMessage) {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "invalid JSON payload")
		return
	}
	// 用户提示词替换必须在首帧模型解析、内容审计和会话 hash 前执行，保证 WS 首轮请求与 HTTP 入口一致。
	firstMessage = h.gatewayService.ApplyUserPromptReplacement(ctx, firstMessage, "openai_responses")
	firstMessage, err = service.RewriteAPIKeyAdditionalModels(firstMessage, apiKey.ModelMapping)
	if err != nil {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "invalid websocket tool model")
		return
	}

	reqModel := strings.TrimSpace(gjson.GetBytes(firstMessage, "model").String())
	if reqModel == "" {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "model is required in first response.create payload")
		return
	}
	clientReqModel := reqModel
	ctx, reqModel = apiKeyModelRedirectContext(ctx, apiKey, clientReqModel)
	c.Request = c.Request.WithContext(ctx)
	previousResponseID := strings.TrimSpace(gjson.GetBytes(firstMessage, "previous_response_id").String())
	previousResponseIDKind := service.ClassifyOpenAIPreviousResponseIDKind(previousResponseID)
	if previousResponseID != "" && previousResponseIDKind == service.OpenAIPreviousResponseIDKindMessageID {
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "previous_response_id must be a response.id (resp_*), not a message id")
		return
	}
	firstMessageToolCoverage := service.AnalyzeToolCallOutputContextCoverageBytes(firstMessage)
	previousResponseCanMove := !firstMessageToolCoverage.HasFunctionCallOutput || firstMessageToolCoverage.ContextCoversAllCallIDs
	reqLog = reqLog.With(
		zap.Bool("ws_ingress", true),
		zap.String("model", clientReqModel),
		zap.Bool("has_previous_response_id", previousResponseID != ""),
		zap.String("previous_response_id_kind", previousResponseIDKind),
	)
	setOpsRequestContext(c, clientReqModel, true)
	setOpsEndpointContext(c, "", int16(service.RequestTypeWSV2))
	setOpenAICyberWarningRequestSnapshot(c, service.ContentModerationProtocolOpenAIResponses, firstMessage)
	// WS passthrough 的客户端帧和上游事件可能并发回调，按 turn 保存提示词摘要需要加锁。
	cyberPromptExcerptByTurn := map[int]string{}
	cyberSnapshotByTurn := map[int]service.ContentModerationInput{}
	var cyberPromptExcerptMu sync.RWMutex
	setCyberPromptExcerpt := func(turn int, promptExcerpt string, snapshot service.ContentModerationInput) {
		if turn <= 0 {
			return
		}
		cyberPromptExcerptMu.Lock()
		cyberPromptExcerptByTurn[turn] = strings.TrimSpace(promptExcerpt)
		cyberSnapshotByTurn[turn] = snapshot
		cyberPromptExcerptMu.Unlock()
	}
	getCyberSnapshot := func(turn int) service.ContentModerationInput {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberSnapshotByTurn[turn]
	}
	getCyberPromptExcerpt := func(turn int) string {
		cyberPromptExcerptMu.RLock()
		defer cyberPromptExcerptMu.RUnlock()
		return cyberPromptExcerptByTurn[turn]
	}
	clearCyberPromptExcerpt := func(turn int) {
		cyberPromptExcerptMu.Lock()
		delete(cyberPromptExcerptByTurn, turn)
		delete(cyberSnapshotByTurn, turn)
		cyberPromptExcerptMu.Unlock()
	}
	setCyberPromptExcerpt(1, currentOpenAICyberWarningPromptExcerpt(c), currentOpenAICyberWarningSnapshot(c))

	if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, reqModel, firstMessage); decision != nil && decision.Blocked {
		writeContentModerationWSError(ctx, wsConn, decision)
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, decision.Message)
		return
	}
	// 首帧已经完整可用，先检查显式会话及其派生会话是否被风控屏蔽，再建立上游连接。
	if cyberBlockKey := findBlockedCyberSessionKey(c.Request.Context(), h.gatewayService, apiKey.ID, c, firstMessage); cyberBlockKey != "" {
		writeCyberSessionBlockedWSError(c.Request.Context(), wsConn)
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, cyberSessionBlockedClientMsg)
		h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, reqModel, cyberBlockKey)
		return
	}

	// 首轮账号选择必须按渠道模型 C 判断生图能力，避免别名映射绕过 Responses 能力检查。
	channelMappingWS, _ := h.gatewayService.ResolveChannelMappingAndRestrict(ctx, apiKey.GroupID, reqModel)
	mappedFirstMessage, routingModelWS, _ := resolveOpenAIChannelMappedImageIntent(
		"/v1/responses", reqModel, firstMessage, channelMappingWS, openAICompatibleRequestPlatform(apiKey), h.gatewayService.ReplaceModelInBody,
	)
	imageIntent := service.IsExplicitImageGenerationIntent("/v1/responses", routingModelWS, mappedFirstMessage)
	initialSchedulingCtx := ctx
	if imageIntent {
		// 首轮账号选择也要遵守显式生图请求的模型级限流。
		initialSchedulingCtx = service.WithOpenAIImageGenerationIntent(initialSchedulingCtx)
	}
	if imageIntent && !service.GroupAllowsResponsesImages(apiKey.Group) {
		service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, service.ImageGenerationPermissionMessage())
		return
	}

	var currentUserRelease func()
	var currentAccountRelease func()
	releaseAccountSlot := func() {
		if currentAccountRelease != nil {
			currentAccountRelease()
			currentAccountRelease = nil
		}
	}
	releaseTurnSlots := func() {
		releaseAccountSlot()
		if currentUserRelease != nil {
			currentUserRelease()
			currentUserRelease = nil
		}
	}
	// 必须尽早注册，确保任何 early return 都能释放已获取的并发槽位。
	defer releaseTurnSlots()

	userReleaseFunc, userAcquired, err := h.concurrencyHelper.TryAcquireUserSlotForAPIKey(ctx, subject.UserID, subject.Concurrency, apiKey.ID)
	if err != nil {
		reqLog.Warn("openai.websocket_user_slot_acquire_failed", zap.Error(err))
		closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "failed to acquire user concurrency slot")
		return
	}
	if !userAcquired {
		closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "too many concurrent requests, please retry later")
		return
	}
	currentUserRelease = wrapReleaseOnDone(ctx, userReleaseFunc)
	ensureUserSlotHeld := func() bool {
		if currentUserRelease != nil {
			return true
		}
		userReleaseFunc, userAcquired, err := h.concurrencyHelper.TryAcquireUserSlotForAPIKey(ctx, subject.UserID, subject.Concurrency, apiKey.ID)
		if err != nil {
			reqLog.Warn("openai.websocket_user_slot_reacquire_failed", zap.Error(err))
			closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "failed to acquire user concurrency slot")
			return false
		}
		if !userAcquired {
			closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "too many concurrent requests, please retry later")
			return false
		}
		currentUserRelease = wrapReleaseOnDone(ctx, userReleaseFunc)
		return true
	}

	subscription, _ := middleware2.GetSubscriptionFromContext(c)
	requestPlatform := openAICompatibleRequestPlatform(apiKey)
	if err := h.billingCacheService.CheckBillingEligibility(ctx, apiKey.User, apiKey, apiKey.Group, subscription, service.QuotaPlatform(c.Request.Context(), apiKey)); err != nil {
		reqLog.Info("openai.websocket_billing_eligibility_check_failed", zap.Error(err))
		closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "billing check failed")
		return
	}

	sessionHash := h.gatewayService.GenerateSessionHashWithFallback(
		c,
		firstMessage,
		openAIWSIngressFallbackSessionSeed(subject.UserID, apiKey.ID, apiKey.GroupID),
	)
	var cyberBlockedThisConn atomic.Bool
	explicitSessionHash := h.gatewayService.GenerateExplicitSessionHash(c, firstMessage)
	if explicitSessionHash != "" {
		if err := h.ensureOpenAISessionIsolation(ctx, apiKey, subject.UserID, service.SessionIsolationSourceOpenAI, explicitSessionHash); err != nil {
			writeSessionIsolationWSError(ctx, wsConn, err)
			closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, openAIWSSessionIsolationCloseReason(err))
			return
		}
	}
	if previousResponseID != "" {
		previousResponseHash := service.DeriveSessionHashFromSeed(previousResponseID)
		if err := h.ensureOpenAISessionIsolation(ctx, apiKey, subject.UserID, service.SessionIsolationSourceOpenAIPreviousResponse, previousResponseHash); err != nil {
			writeSessionIsolationWSError(ctx, wsConn, err)
			closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, openAIWSSessionIsolationCloseReason(err))
			return
		}
	}
	ctx = service.WithOpenAIGuardianParentAffinity(ctx, c, firstMessage, reqModel)
	maxAccountSwitches := h.maxAccountSwitches
	switchCount := 0
	failedAccountIDs := make(map[int64]struct{})
	var lastFailoverErr *service.UpstreamFailoverError
	var oauth429FailoverState service.OpenAIOAuth429FailoverState
	wsAttemptMessage := append([]byte(nil), firstMessage...)
	handleWSFailover := func(selection *service.AccountSelectionResult, account *service.Account, failoverErr *service.UpstreamFailoverError) bool {
		if ctx.Err() != nil {
			return false
		}
		if failoverErr.ShouldReportAccountScheduleFailure() {
			h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(channelMappingWS.MappedModel), false, nil)
		}
		releaseAccountSlot()
		if !failoverErr.ShouldRetryNextAccount() {
			closeOpenAIWSFailoverExhausted(c, wsConn, failoverErr)
			return false
		}
		if ctx.Err() != nil {
			return false
		}
		h.gatewayService.RecordOpenAIAccountSwitchForSelection(selection)
		failedAccountIDs[account.ID] = struct{}{}
		lastFailoverErr = failoverErr
		if switchCount >= maxAccountSwitches {
			closeOpenAIWSFailoverExhausted(c, wsConn, failoverErr)
			return false
		}
		switchCount++
		if h.gatewayService.ShouldStopOpenAIOAuth429Failover(account, failoverErr.StatusCode, switchCount, &oauth429FailoverState) {
			closeOpenAIWSFailoverExhausted(c, wsConn, failoverErr)
			return false
		}
		reqLog.Warn("openai.websocket_upstream_failover_switching",
			zap.Int64("account_id", account.ID),
			zap.Int("upstream_status", failoverErr.StatusCode),
			zap.Int("switch_count", switchCount),
			zap.Int("max_switches", maxAccountSwitches),
		)
		if ctx.Err() != nil {
			return false
		}
		return ensureUserSlotHeld()
	}

	// 与 HTTP Responses 路径保持一致：生图意图请求要求账号支持 Responses API（#4417）。
	// WSv2 传输本身已隐含 Responses 支持，此处为防御性对齐。
	// 首轮显式意图已按渠道模型 C 判断，被动 namespace 不会误过滤账号（#4476）。
	requiredCapability := service.OpenAIEndpointCapabilityTextGeneration
	if imageIntent && requestPlatform == service.PlatformOpenAI {
		requiredCapability = service.OpenAIEndpointCapabilityResponses
	}

	for {
		if ctx.Err() != nil {
			return
		}
		reqLog.Debug("openai.websocket_account_selecting", zap.Int("excluded_account_count", len(failedAccountIDs)))
		selection, scheduleDecision, err := h.gatewayService.SelectAccountWithSchedulerForCapability(
			initialSchedulingCtx,
			apiKey.GroupID,
			previousResponseID,
			sessionHash,
			reqModel,
			failedAccountIDs,
			service.OpenAIUpstreamTransportResponsesWebsocketV2Ingress,
			requiredCapability,
			false,
			previousResponseCanMove,
			requestPlatform,
		)
		if err != nil {
			reqLog.Warn("openai.websocket_account_select_failed",
				zap.Error(openAICompatibleSelectionErrorForLog(err, requestPlatform)),
				zap.Int("excluded_account_count", len(failedAccountIDs)),
			)
			if lastFailoverErr != nil {
				closeOpenAIWSFailoverExhausted(c, wsConn, lastFailoverErr)
			} else {
				closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "no available account")
			}
			return
		}
		if selection == nil || selection.Account == nil {
			if lastFailoverErr != nil {
				closeOpenAIWSFailoverExhausted(c, wsConn, lastFailoverErr)
			} else {
				closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "no available account")
			}
			return
		}

		account := selection.Account
		accountMaxConcurrency := account.Concurrency
		if selection.WaitPlan != nil && selection.WaitPlan.MaxConcurrency > 0 {
			accountMaxConcurrency = selection.WaitPlan.MaxConcurrency
		}
		accountReleaseFunc := selection.ReleaseFunc
		if !selection.Acquired {
			if selection.WaitPlan == nil {
				closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "account is busy, please retry later")
				return
			}
			fastReleaseFunc, fastAcquired, err := h.concurrencyHelper.TryAcquireAccountSlot(
				ctx,
				account.ID,
				selection.WaitPlan.MaxConcurrency,
			)
			if err != nil {
				reqLog.Warn("openai.websocket_account_slot_acquire_failed", zap.Int64("account_id", account.ID), zap.Error(err))
				closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "failed to acquire account concurrency slot")
				return
			}
			if !fastAcquired {
				closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "account is busy, please retry later")
				return
			}
			accountReleaseFunc = fastReleaseFunc
		}
		currentAccountRelease = wrapReleaseOnDone(ctx, accountReleaseFunc)
		if err := h.gatewayService.BindStickySession(ctx, apiKey.GroupID, sessionHash, account.ID); err != nil {
			reqLog.Warn("openai.websocket_bind_sticky_session_failed", zap.Int64("account_id", account.ID), zap.Error(err))
		}

		token, _, err := h.gatewayService.GetRequestCredential(ctx, c, account)
		if err != nil {
			reqLog.Warn("openai.websocket_get_access_token_failed", zap.Int64("account_id", account.ID), zap.Error(err))
			if ctx.Err() != nil {
				return
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				if handleWSFailover(selection, account, failoverErr) {
					continue
				}
				return
			}
			closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "failed to get access token")
			return
		}
		tlsRouterMatch := h.gatewayService.MatchOpenAITLSFingerprintRouterForRequest(c, account)
		if err := h.gatewayService.EnforceOpenAIClientPolicyForRequest(ctx, c, account, firstMessage, tlsRouterMatch); err != nil {
			reqLog.Warn("openai.websocket_client_policy_rejected", zap.Int64("account_id", account.ID), zap.Error(err))
			closeOpenAIClientWS(wsConn, coderws.StatusPolicyViolation, "client is not allowed")
			return
		}

		reqLog.Debug("openai.websocket_account_selected",
			zap.Int64("account_id", account.ID),
			zap.String("account_name", account.Name),
			zap.String("schedule_layer", scheduleDecision.Layer),
			zap.Int("candidate_count", scheduleDecision.CandidateCount),
		)

		// 首帧保持客户端模型 R，由 service 层与后续 turn 一样逐轮执行 R -> C -> U。
		wsFirstMessageForUsageFallback := append([]byte(nil), firstMessage...)
		// 每轮通过现有鉴权缓存刷新策略；刷新失败时沿用最近一次有效值，避免瞬时故障中断长连接。
		var currentFastModePolicy atomic.Value
		currentFastModePolicy.Store(apiKey.FastModePolicy)
		maxReasoningEffort := ""
		maxReasoningEffortOverLimit := ""
		var reasoningEffortMappings []service.ReasoningEffortMapping
		if apiKey.Group != nil && apiKey.Group.Platform == service.PlatformOpenAI {
			maxReasoningEffort = apiKey.Group.MaxReasoningEffort
			maxReasoningEffortOverLimit = apiKey.Group.MaxReasoningEffortOverLimit
			reasoningEffortMappings = apiKey.Group.ReasoningEffortMappings
		}
		hooks := &service.OpenAIWSIngressHooks{
			ClientLifecycleContext:      clientLifecycleCtx,
			InitialRequestModel:         reqModel,
			InitialTurnStartedAt:        firstTurnStartedAt,
			MaxReasoningEffort:          maxReasoningEffort,
			MaxReasoningEffortOverLimit: maxReasoningEffortOverLimit,
			ReasoningEffortMappings:     reasoningEffortMappings,
			ResolveFastModePolicy: func(_ int) string {
				fallback, _ := currentFastModePolicy.Load().(string)
				if h.apiKeyService == nil || strings.TrimSpace(apiKey.Key) == "" {
					return fallback
				}
				refreshed, refreshErr := h.apiKeyService.GetByKey(ctx, apiKey.Key)
				if refreshErr != nil || refreshed == nil {
					return fallback
				}
				policy, ok := service.NormalizeAPIKeyFastModePolicy(refreshed.FastModePolicy)
				if !ok {
					return fallback
				}
				currentFastModePolicy.Store(policy)
				return policy
			},
			ResolveRoutingModel: func(_ int, requestedModel string, payload []byte) (string, error) {
				requestedModel = strings.TrimSpace(requestedModel)
				if requestedModel == "" {
					requestedModel = clientReqModel
				}
				turnCtx, redirectedModel := apiKeyModelRedirectContext(ctx, apiKey, requestedModel)
				turnMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(turnCtx, apiKey.GroupID, redirectedModel)
				mappedPayload, turnRoutingModel, _ := resolveOpenAIChannelMappedImageIntent(
					"/v1/responses", redirectedModel, payload, turnMapping, requestPlatform, h.gatewayService.ReplaceModelInBody,
				)
				turnImageIntent := service.IsExplicitImageGenerationIntent("/v1/responses", turnRoutingModel, mappedPayload)
				if turnImageIntent && !service.GroupAllowsResponsesImages(apiKey.Group) {
					service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalFeatureGate)
					return "", service.NewOpenAIWSClientCloseError(
						coderws.StatusPolicyViolation,
						service.ImageGenerationPermissionMessage(),
						nil,
					)
				}
				if turnImageIntent {
					// 后续 turn 的账号资格检查必须包含该轮生图限流范围。
					turnCtx = service.WithOpenAIImageGenerationIntent(turnCtx)
				}
				turnCapability := service.OpenAIEndpointCapabilityTextGeneration
				if turnImageIntent && requestPlatform == service.PlatformOpenAI {
					turnCapability = service.OpenAIEndpointCapabilityResponses
				}
				routingModel, resolveErr := h.gatewayService.ResolveOpenAIWSRoutingModelForAccount(
					turnCtx,
					apiKey.GroupID,
					account,
					redirectedModel,
					turnCapability,
				)
				if resolveErr != nil {
					return "", newOpenAIWSLocalRoutingRejectedError(redirectedModel, resolveErr)
				}
				return routingModel, nil
			},
			BeforeRequest: func(turn int, payload []byte, originalModel, _ string) ([]byte, error) {
				if turn == 1 {
					return payload, nil
				}
				if !gjson.ValidBytes(payload) {
					return payload, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket request payload", errors.New("invalid json"))
				}
				rewrittenPayload, rewriteErr := service.RewriteAPIKeyAdditionalModels(payload, apiKey.ModelMapping)
				if rewriteErr != nil {
					return payload, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "invalid websocket tool model", rewriteErr)
				}
				payload = rewrittenPayload
				payloadPreviousResponseID := strings.TrimSpace(gjson.GetBytes(payload, "previous_response_id").String())
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = strings.TrimSpace(gjson.GetBytes(payload, "model").String())
				}
				if model == "" {
					model = clientReqModel
				}
				_, model = apiKeyModelRedirectContext(ctx, apiKey, model)
				setOpenAICyberWarningRequestSnapshot(c, service.ContentModerationProtocolOpenAIResponses, payload)
				setCyberPromptExcerpt(turn, currentOpenAICyberWarningPromptExcerpt(c), currentOpenAICyberWarningSnapshot(c))
				if decision := h.checkContentModeration(c, reqLog, apiKey, subject, service.ContentModerationProtocolOpenAIResponses, model, payload); decision != nil && decision.Blocked {
					writeContentModerationWSError(ctx, wsConn, decision)
					return payload, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, decision.Message, nil)
				}
				if payloadPreviousResponseID != "" {
					previousResponseHash := service.DeriveSessionHashFromSeed(payloadPreviousResponseID)
					if err := h.ensureOpenAISessionIsolation(ctx, apiKey, subject.UserID, service.SessionIsolationSourceOpenAIPreviousResponse, previousResponseHash); err != nil {
						writeSessionIsolationWSError(ctx, wsConn, err)
						return payload, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, openAIWSSessionIsolationCloseReason(err), err)
					}
				}
				if explicitHash := h.gatewayService.GenerateExplicitSessionHash(c, payload); explicitHash != "" {
					if err := h.ensureOpenAISessionIsolation(ctx, apiKey, subject.UserID, service.SessionIsolationSourceOpenAI, explicitHash); err != nil {
						writeSessionIsolationWSError(ctx, wsConn, err)
						return payload, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, openAIWSSessionIsolationCloseReason(err), err)
					}
				}
				return payload, nil
			},
			BeforeTurn: func(turn int) error {
				if cyberBlockedThisConn.Load() {
					return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, cyberSessionBlockedClientMsg, nil)
				}
				if turn == 1 {
					return nil
				}
				// 防御式清理：避免异常路径下旧槽位覆盖导致泄漏。
				releaseTurnSlots()
				// 非首轮 turn 需要重新抢占并发槽位，避免长连接空闲占槽。
				userReleaseFunc, userAcquired, err := h.concurrencyHelper.TryAcquireUserSlotForAPIKey(ctx, subject.UserID, subject.Concurrency, apiKey.ID)
				if err != nil {
					return service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "failed to acquire user concurrency slot", err)
				}
				if !userAcquired {
					return service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "too many concurrent requests, please retry later", nil)
				}
				accountReleaseFunc, accountAcquired, err := h.concurrencyHelper.TryAcquireAccountSlot(ctx, account.ID, accountMaxConcurrency)
				if err != nil {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return service.NewOpenAIWSClientCloseError(coderws.StatusInternalError, "failed to acquire account concurrency slot", err)
				}
				if !accountAcquired {
					if userReleaseFunc != nil {
						userReleaseFunc()
					}
					return service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "account is busy, please retry later", nil)
				}
				currentUserRelease = wrapReleaseOnDone(ctx, userReleaseFunc)
				currentAccountRelease = wrapReleaseOnDone(ctx, accountReleaseFunc)
				return nil
			},
			OnUpstreamError: func(turn int, originalModel string, statusCode int, responseBody []byte, warningText string) {
				model := strings.TrimSpace(originalModel)
				if model == "" {
					model = clientReqModel
				}
				h.recordOpenAICyberWarningWithSnapshot(c, reqLog, apiKey, account, model, statusCode, responseBody, warningText, getCyberPromptExcerpt(turn), getCyberSnapshot(turn))
			},
			AfterTurn: func(capture service.OpenAIWSTurnCapture) {
				turn := capture.Turn
				result := capture.Result
				turnErr := capture.Err
				turnClientModel := strings.TrimSpace(capture.OriginalModel)
				if turnClientModel == "" {
					turnClientModel = clientReqModel
				}
				turnCtx, turnModel := apiKeyModelRedirectContext(ctx, apiKey, turnClientModel)
				turnChannelMapping, _ := h.gatewayService.ResolveChannelMappingAndRestrict(turnCtx, apiKey.GroupID, turnModel)
				releaseTurnSlots()
				defer clearCyberPolicyTurnState(c)
				turnRequestBodyForCyber := capture.RequestBody
				if len(turnRequestBodyForCyber) == 0 {
					turnRequestBodyForCyber = wsFirstMessageForUsageFallback
				}
				cyberPolicyHandled := h.recordCyberPolicyIfMarked(
					c,
					apiKey,
					account,
					subscription,
					turnModel,
					turnErr != nil,
					turnRequestBodyForCyber,
					turnChannelMapping.ToUsageFields(turnModel, ""),
					service.HashUsageRequestPayload(turnRequestBodyForCyber),
				)
				if cyberPolicyHandled {
					cyberBlockedThisConn.Store(true)
				}
				defer clearCyberPromptExcerpt(turn)
				if turnErr != nil {
					if result == nil || result.ImageCount <= 0 {
						return
					}
					if cyberPolicyHandled {
						return
					}
					reqLog.Warn("openai.websocket_partial_error_with_image_result",
						zap.Int64("account_id", account.ID),
						zap.Int("image_count", result.ImageCount),
						zap.Error(turnErr),
					)
				}
				if result == nil {
					return
				}
				// WS 每个 turn 的渠道映射可能覆盖默认计费模型，统一在记录用量前解析。
				result.BillingModel = openAIWSTurnBillingModel(result, turnChannelMapping, turnModel, result.UpstreamModel)
				// 排除 spark 影子:其 codex_* 仅由 QueryUsage(/wham/usage bengalfox)更新(外审第7轮 P1)。
				if account.Type == service.AccountTypeOAuth && !account.IsShadow() {
					h.gatewayService.UpdateCodexUsageSnapshotFromHeaders(ctx, account.ID, result.ResponseHeaders)
				}
				scheduleModel := strings.TrimSpace(result.UpstreamModel)
				if scheduleModel == "" {
					scheduleModel = account.GetMappedModel(turnChannelMapping.MappedModel)
				}
				h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, scheduleModel, openAIForwardSucceededForScheduling(result), result.FirstTokenMs)
				inboundEndpoint := GetInboundEndpoint(c)
				upstreamEndpoint := resolveOpenAIUpstreamEndpoint(c, account, result)
				turnRequestBody := capture.RequestBody
				if len(turnRequestBody) == 0 {
					turnRequestBody = wsFirstMessageForUsageFallback
				}
				turnPayloadHash := service.HashUsageRequestPayload(turnRequestBody)
				cyberBlocked := cyberPolicyHandled
				quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
				clientSessionID := service.ExtractClientSessionID(c)
				h.submitOpenAIUsageRecordTask(c, result, func(taskCtx context.Context) {
					taskCtx = service.PropagateAPIKeyModelRedirectTrace(taskCtx, turnCtx)
					if err := h.gatewayService.RecordUsage(taskCtx, &service.OpenAIRecordUsageInput{
						Result:             result,
						APIKey:             apiKey,
						User:               apiKey.User,
						Account:            account,
						Subscription:       subscription,
						InboundEndpoint:    inboundEndpoint,
						UpstreamEndpoint:   upstreamEndpoint,
						UserAgent:          userAgent,
						IPAddress:          clientIP,
						RequestPayloadHash: turnPayloadHash,
						RequestBody:        append([]byte(nil), turnRequestBody...),
						PricingAt:          capture.StartedAt,
						APIKeyService:      h.apiKeyService,
						QuotaPlatform:      quotaPlatform,
						ClientSessionID:    clientSessionID,
						ChannelUsageFields: turnChannelMapping.ToUsageFields(turnModel, result.UpstreamModel),
						CyberBlocked:       cyberBlocked,
					}); err != nil {
						reqLog.Error("openai.websocket_record_usage_failed",
							zap.Int64("account_id", account.ID),
							zap.String("request_id", result.RequestID),
							zap.Error(err),
						)
					}
				})
			},
		}

		// service 层会在解析首帧时执行渠道及账号映射，此处只处理会话链字段。
		wsFirstMessage := append([]byte(nil), wsAttemptMessage...)
		// 切组/会话失配防护：previous_response_id 未在当前分组命中粘连账号时，
		// 说明该会话链不属于本次调度到的账号；原样转发会触发上游会话链鉴权失败。
		// 因此只在上下文可迁移时剥离首包 previous_response_id，后续 turn 仍由 WS 转发层处理。
		if previousResponseID != "" && !scheduleDecision.StickyPreviousHit && previousResponseCanMove {
			wsFirstMessage = service.RemovePreviousResponseIDFromBody(wsFirstMessage)
			reqLog.Debug("openai.websocket_previous_response_id_stripped_cross_group",
				zap.Int64("account_id", account.ID),
				zap.String("schedule_layer", scheduleDecision.Layer),
			)
		}

		if preemptCtx, cleanupPreempt, armed := h.gatewayService.BeginOpenAIWSIngressSessionPreemption(ctx, c, account, wsFirstMessage); armed {
			ctx = preemptCtx
			defer cleanupPreempt()
		}

		if err := h.gatewayService.ProxyResponsesWebSocketFromClient(ctx, c, wsConn, account, token, wsFirstMessage, hooks); err != nil {
			if service.IsOpenAIWSSessionPreemptedError(err) {
				return
			}
			var failoverErr *service.UpstreamFailoverError
			if errors.As(err, &failoverErr) {
				retryPayload, retryCurrentTurn := service.OpenAIWSCurrentTurnRetryPayload(err)
				nextAttemptMessage, retrySafe := openAIWSNextAttemptMessage(wsAttemptMessage, retryPayload, retryCurrentTurn)
				if !retrySafe {
					closeOpenAIWSFailoverExhausted(c, wsConn, failoverErr)
					return
				}
				wsAttemptMessage = nextAttemptMessage
				if retryCurrentTurn {
					previousResponseID = ""
					reqLog.Warn("openai.websocket_current_turn_failover_retry",
						zap.Int64("account_id", account.ID),
						zap.Int("upstream_status", failoverErr.StatusCode),
						zap.Int("retry_payload_bytes", len(retryPayload)),
					)
				}
				if handleWSFailover(selection, account, failoverErr) {
					continue
				}
				return
			}

			if errors.Is(context.Cause(ctx), service.ErrOpenAIWSIngressLeaseLost) {
				reqLog.Warn("openai.websocket_ingress_lease_lost",
					zap.Int64("account_id", account.ID),
					zap.Error(err),
				)
				closeOpenAIClientWS(wsConn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
				return
			}

			var closeErr *service.OpenAIWSClientCloseError
			hasClientCloseErr := errors.As(err, &closeErr)
			var overLimit *service.ReasoningEffortOverLimitError
			if errors.As(err, &overLimit) {
				// WS 策略拒绝属于本地业务限制，既不冷却账号，也不计入 SLA。
				service.MarkOpsClientBusinessLimited(c, service.OpsClientBusinessLimitedReasonLocalPolicyDenied)
			}
			if openAIWSIngressEndedByClient(err) {
				closedFields := []zap.Field{zap.Int64("account_id", account.ID)}
				if hasClientCloseErr {
					closedFields = append(closedFields, zap.String("reason", closeErr.Reason()))
				} else {
					closedFields = append(closedFields, zap.Error(err))
				}
				reqLog.Info("openai.websocket_ingress_closed_normally", closedFields...)
				// A bare coderws.CloseError or a plain cancellation carries no
				// gateway-chosen close frame; mirror the client's clean 1000
				// rather than the 1011 the proxy-failure tail would have sent.
				if hasClientCloseErr {
					closeOpenAIClientWS(wsConn, closeErr.StatusCode(), closeErr.Reason())
				} else {
					closeOpenAIClientWS(wsConn, coderws.StatusNormalClosure, "")
				}
				return
			}

			if shouldReportOpenAIWSProxyAccountFailure(err) {
				h.gatewayService.ReportOpenAIAccountScheduleResultForSelection(selection, account.ID, account.GetMappedModel(channelMappingWS.MappedModel), false, nil)
			}
			closeStatus, closeReason := summarizeWSCloseErrorForLog(err)
			proxyFailedFields := []zap.Field{
				zap.Int64("account_id", account.ID),
				zap.Error(err),
				zap.String("close_status", closeStatus),
				zap.String("close_reason", closeReason),
			}
			proxyFailedFields = appendOpenAIAccountProxyLogFields(proxyFailedFields, account)
			reqLog.Warn("openai.websocket_proxy_failed", proxyFailedFields...)
			if hasClientCloseErr {
				closeOpenAIClientWS(wsConn, closeErr.StatusCode(), closeErr.Reason())
				return
			}
			closeOpenAIClientWS(wsConn, coderws.StatusInternalError, "upstream websocket proxy failed")
			return
		}
		reqLog.Info("openai.websocket_ingress_closed", zap.Int64("account_id", account.ID))
		return
	}

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

// recoverAnthropicMessagesPanic recovers from panics in the Anthropic Messages
// handler and returns an Anthropic-formatted error response.
func (h *OpenAIGatewayHandler) recoverAnthropicMessagesPanic(c *gin.Context, streamStarted *bool) {
	recovered := recover()
	if recovered == nil {
		return
	}

	started := streamStarted != nil && *streamStarted
	requestLogger(c, "handler.openai_gateway.messages").Error(
		"openai.messages_panic_recovered",
		zap.Bool("stream_started", started),
		zap.Any("panic", recovered),
		zap.ByteString("stack", debug.Stack()),
	)
	if !started {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "Internal server error")
	}
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
	if h.concurrencyHelper == nil || h.concurrencyHelper.concurrencyService == nil {
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
	if failoverErr == nil {
		h.handleFailoverExhaustedSimple(c, http.StatusBadGateway, streamStarted)
		return
	}
	if failoverErr.IsOpenAIRequestBodyTooLarge() {
		service.SetOpsUpstreamError(c, http.StatusRequestEntityTooLarge, service.OpenAIRequestBodyTooLargeClientMessage, "")
		h.handleStreamingAwareError(
			c,
			http.StatusRequestEntityTooLarge,
			"invalid_request_error",
			service.OpenAIRequestBodyTooLargeClientMessage,
			streamStarted,
		)
		return
	}
	if failoverErr.Reason == service.OpenAIHTTPContinuationUnsupportedReason {
		message := strings.TrimSpace(failoverErr.ClientMessage)
		if message == "" {
			message = "previous_response_id requires an OpenAI API-key account for HTTP requests"
		}
		h.handleStreamingAwareError(c, http.StatusBadRequest, "invalid_request_error", message, streamStarted)
		return
	}
	copyFailoverRetryAfter(c, failoverErr.ResponseHeaders)
	if failoverErr.IsCredentialFailure() {
		status, message := credentialFailoverClientResponse(failoverErr)
		h.handleStreamingAwareError(c, status, "upstream_error", message, streamStarted)
		return
	}
	if failoverErr.IsOpenAICapacityShed() && strings.TrimSpace(failoverErr.ClientMessage) != "" {
		status := failoverErr.ClientStatusCode
		if status <= 0 {
			status = http.StatusServiceUnavailable
		}
		h.handleStreamingAwareError(c, status, "server_error", failoverErr.ClientMessage, streamStarted)
		return
	}
	statusCode := failoverErr.StatusCode
	responseBody := failoverErr.ResponseBody
	if service.IsOpenAISilentRefusalErrorBody(responseBody) {
		service.SetOpsUpstreamError(c, statusCode, service.OpenAISilentRefusalClientMessage(), "")
		h.handleStreamingAwareError(c, http.StatusBadGateway, "upstream_error", service.OpenAISilentRefusalClientMessage(), streamStarted)
		return
	}
	if service.IsOpenAICyberWarningPayload(responseBody, "") {
		message := service.ExtractOpenAICyberWarningMessage(responseBody, "")
		service.SetOpsUpstreamError(c, statusCode, message, "")
		responseStatus := statusCode
		if responseStatus < 400 || responseStatus > 599 {
			responseStatus = http.StatusBadGateway
		}
		h.handleStreamingAwareError(c, responseStatus, "invalid_request_error", message, streamStarted)
		return
	}

	// 先检查透传规则
	if h.errorPassthroughService != nil && len(responseBody) > 0 {
		if rule := h.errorPassthroughService.MatchRule("openai", statusCode, responseBody); rule != nil {
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
	if c == nil || headers == nil {
		return
	}
	retryAfter := strings.TrimSpace(headers.Get("Retry-After"))
	if retryAfter == "" || len(retryAfter) > 128 || strings.ContainsAny(retryAfter, "\r\n") || !isSafeRetryAfter(retryAfter) {
		return
	}
	c.Header("Retry-After", retryAfter)
}

func isSafeRetryAfter(value string) bool {
	digitsOnly := true
	for _, char := range value {
		if char < '0' || char > '9' {
			digitsOnly = false
			break
		}
	}
	if digitsOnly {
		seconds, err := strconv.ParseUint(value, 10, 32)
		return err == nil && seconds <= uint64((7*24*time.Hour)/time.Second)
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return false
	}
	return !retryAt.After(time.Now().Add(7 * 24 * time.Hour))
}

// handleFailoverExhaustedSimple 简化版本，用于没有响应体的情况
func (h *OpenAIGatewayHandler) handleFailoverExhaustedSimple(c *gin.Context, statusCode int, streamStarted bool) {
	status, errType, errMsg := h.mapUpstreamError(statusCode)
	service.SetOpsUpstreamError(c, statusCode, errMsg, "")
	h.handleStreamingAwareError(c, status, errType, errMsg, streamStarted)
}

func (h *OpenAIGatewayHandler) mapUpstreamError(statusCode int) (int, string, string) {
	switch statusCode {
	case 401:
		return http.StatusBadGateway, "upstream_error", "Upstream authentication failed, please contact administrator"
	case 403:
		return http.StatusBadGateway, "upstream_error", "Upstream access forbidden, please contact administrator"
	case 429:
		return http.StatusTooManyRequests, "rate_limit_error", "Upstream rate limit exceeded, please retry later"
	case 529:
		return http.StatusServiceUnavailable, "upstream_error", "Upstream service overloaded, please retry later"
	case 500, 502, 503, 504:
		return http.StatusBadGateway, "upstream_error", "Upstream service temporarily unavailable"
	default:
		return http.StatusBadGateway, "upstream_error", "Upstream request failed"
	}
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
	// body-signal compact 心跳可能已把响应头提交为 200：先停心跳（建立
	// happens-before，接管 ResponseWriter），并升级为流内错误处理。
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		streamStarted = true
	}
	if streamStarted {
		if countTowardsSLA {
			service.MarkOpsStreamFailure(c, errType, code, message, status)
		} else {
			service.MarkOpsStreamError(c, errType, message, status)
		}
		// /v1/responses 的严格 SDK（Codex CLI）要求终止事件必须属于
		// response.completed/failed/incomplete/cancelled 集合。
		// 通用 `event: error` 帧不被识别为终止事件，会导致
		// "stream closed before response.completed"。
		if inboundIsResponses(c) {
			if writeResponsesFailedSSE(c, errType, code, message) {
				return
			}
		}
		// Stream already started, send error as SSE event then close
		flusher, ok := c.Writer.(http.Flusher)
		if ok {
			errorObject := gin.H{"type": errType, "message": message}
			if code != "" {
				errorObject["code"] = code
			}
			payload, err := json.Marshal(gin.H{"error": errorObject})
			if err != nil {
				payload = []byte(`{"error":{"type":"upstream_error","message":"Upstream request failed"}}`)
			}
			errorEvent := "event: error\ndata: " + string(payload) + "\n\n"
			if _, err := fmt.Fprint(c.Writer, errorEvent); err != nil {
				_ = c.Error(err)
			}
			flusher.Flush()
		}
		return
	}

	// Normal case: return JSON response with proper status code
	if code == "" {
		h.errorResponse(c, status, errType, message)
		return
	}
	c.JSON(status, gin.H{"error": gin.H{
		"type": errType, "code": code, "message": message,
	}})
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

const cyberPolicyRecordedKey = "ops_cyber_recorded"

type cyberPolicyOpsErrorMeta struct {
	RequestID        string
	ClientRequestID  string
	Platform         string
	Model            string
	RequestPath      string
	Stream           bool
	InboundEndpoint  string
	UpstreamEndpoint string
	UserAgent        string
	APIKeyPrefix     string
	UserID           int64
	APIKeyID         int64
	AccountID        int64
	GroupID          *int64
	ClientIP         string
	CreatedAt        time.Time
	SessionBlockKey  string
}

func buildCyberPolicyOpsErrorEntry(meta cyberPolicyOpsErrorMeta, mark *service.CyberPolicyMark) *service.OpsInsertErrorLogInput {
	if mark == nil {
		return nil
	}
	rt := int16(service.RequestTypeCyberBlocked)
	entry := &service.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		UpstreamEndpoint:  meta.UpstreamEndpoint,
		RequestedModel:    meta.Model,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_policy",
		Severity:          "P3",
		StatusCode:        mark.UpstreamStatus,
		IsBusinessLimited: true,
		ErrorMessage:      "cyber_policy: " + strings.TrimSpace(mark.Message),
		ErrorBody:         mark.Body,
		ErrorSource:       "upstream_http",
		ErrorOwner:        "provider",
		CreatedAt:         meta.CreatedAt,
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	if meta.AccountID > 0 {
		entry.AccountID = &meta.AccountID
	}
	if meta.GroupID != nil {
		entry.GroupID = cloneHandlerInt64Ptr(meta.GroupID)
	}
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}

const cyberSessionBlockedClientMsg = "该会话已被网络安全策略屏蔽，请开启新会话 / This session is blocked by cyber-security policy, please start a new session"

func buildCyberSessionBlockedOpsEntry(meta cyberPolicyOpsErrorMeta) *service.OpsInsertErrorLogInput {
	rt := int16(service.RequestTypeCyberBlocked)
	entry := &service.OpsInsertErrorLogInput{
		RequestID:         meta.RequestID,
		ClientRequestID:   meta.ClientRequestID,
		Platform:          meta.Platform,
		Model:             meta.Model,
		RequestPath:       meta.RequestPath,
		Stream:            meta.Stream,
		InboundEndpoint:   meta.InboundEndpoint,
		RequestedModel:    meta.Model,
		RequestType:       &rt,
		UserAgent:         meta.UserAgent,
		APIKeyPrefix:      meta.APIKeyPrefix,
		ErrorPhase:        "request",
		ErrorType:         "cyber_policy_session_blocked",
		Severity:          "P3",
		StatusCode:        http.StatusForbidden,
		IsBusinessLimited: true,
		ErrorMessage:      "cyber_policy_session_blocked: request rejected locally by session block",
		ErrorBody:         "session_block_key=" + meta.SessionBlockKey,
		ErrorSource:       "gateway_local",
		ErrorOwner:        "platform",
		CreatedAt:         meta.CreatedAt,
	}
	if meta.UserID > 0 {
		entry.UserID = &meta.UserID
	}
	if meta.APIKeyID > 0 {
		entry.APIKeyID = &meta.APIKeyID
	}
	if meta.GroupID != nil {
		entry.GroupID = cloneHandlerInt64Ptr(meta.GroupID)
	}
	if meta.ClientIP != "" {
		entry.ClientIP = &meta.ClientIP
	}
	return entry
}

type cyberSessionBlockFormat int

const (
	cyberBlockFormatResponses cyberSessionBlockFormat = iota
	cyberBlockFormatChat
	cyberBlockFormatAnthropic
)

// cyberSessionBlockAppliesToGroup 只对已纳入风控范围的分组启用会话屏蔽，范围读取失败时放行请求。
func (h *OpenAIGatewayHandler) cyberSessionBlockAppliesToGroup(c *gin.Context, apiKey *service.APIKey) bool {
	if h == nil || h.contentModerationService == nil || c == nil || c.Request == nil || apiKey == nil {
		return false
	}
	inScope, err := h.contentModerationService.CyberSessionBlockGroupInScope(c.Request.Context(), apiKey.GroupID)
	if err != nil {
		requestLogger(c, "handler.openai_gateway.cyber_session_block").Warn("content_moderation.cyber_session_block_scope_check_failed", zap.Error(err))
		return false
	}
	return inScope
}

func (h *OpenAIGatewayHandler) rejectIfCyberSessionBlocked(c *gin.Context, apiKey *service.APIKey, body []byte, model string, format cyberSessionBlockFormat) bool {
	if h == nil || h.gatewayService == nil || apiKey == nil || c == nil {
		return false
	}
	if enabled, _ := h.gatewayService.CyberSessionBlockRuntime(c.Request.Context()); !enabled {
		return false
	}
	if !h.cyberSessionBlockAppliesToGroup(c, apiKey) {
		return false
	}
	key := findBlockedCyberSessionKey(c.Request.Context(), h.gatewayService, apiKey.ID, c, body)
	if key == "" {
		return false
	}
	// body-signal compact 心跳可能已把响应头提交为 200（cyber 检查在用户槽位
	// 长等待之后执行）：以 response.failed 终止事件回传；未提交时停拍后照常
	// 写 JSON（#3887）。
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		service.MarkOpsStreamError(c, "permission_error", cyberSessionBlockedClientMsg, http.StatusForbidden)
		if writeResponsesFailedSSE(c, "permission_error", "", cyberSessionBlockedClientMsg) {
			h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, model, key)
			return true
		}
	}
	switch format {
	case cyberBlockFormatAnthropic:
		c.JSON(http.StatusForbidden, gin.H{"type": "error", "error": gin.H{
			"type":    "permission_error",
			"message": cyberSessionBlockedClientMsg,
		}})
	default:
		c.JSON(http.StatusForbidden, gin.H{"error": gin.H{
			"type":    "permission_error",
			"code":    "session_blocked_by_cyber_policy",
			"message": cyberSessionBlockedClientMsg,
		}})
	}
	h.enqueueCyberSessionBlockedOpsEntry(c, apiKey, model, key)
	return true
}

func (h *OpenAIGatewayHandler) enqueueCyberSessionBlockedOpsEntry(c *gin.Context, apiKey *service.APIKey, model string, sessionBlockKey string) {
	if h == nil || h.opsService == nil || c == nil {
		return
	}
	// 已经写入专用会话屏蔽日志后，外层错误中间件不得再追加一条通用权限日志。
	c.Set(opsDedicatedErrorRecordedKey, true)
	meta := h.cyberPolicyOpsMeta(c, apiKey, nil, model, service.PlatformOpenAI, false, sessionBlockKey)
	enqueueOpsErrorLog(h.opsService, buildCyberSessionBlockedOpsEntry(meta))
}

func (h *OpenAIGatewayHandler) recordCyberPolicyIfMarked(c *gin.Context, apiKey *service.APIKey, account *service.Account, subscription *service.UserSubscription, model string, forwardErrored bool, cyberBlockArg any, channelFields service.ChannelUsageFields, requestPayloadHash string, nativeCompaction ...bool) bool {
	mark := service.GetOpsCyberPolicy(c)
	if mark == nil || c == nil {
		return false
	}
	cyberBlockKey := ""
	switch value := cyberBlockArg.(type) {
	case string:
		cyberBlockKey = strings.TrimSpace(value)
	case []byte:
		if apiKey != nil {
			plan := buildCyberSessionBlockWritePlan(apiKey.ID, c, value)
			cyberBlockKey = plan.scopeKey
			if len(plan.keys) > 0 && h.gatewayService != nil {
				blockCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
				h.gatewayService.MarkCyberSessionBlocked(blockCtx, plan.scopeKey, plan.keys)
				cancel()
			}
		}
	}
	if c.GetBool(cyberPolicyRecordedKey) {
		return true
	}

	platform := service.PlatformOpenAI
	if account != nil && strings.TrimSpace(account.Platform) != "" {
		platform = account.Platform
	}
	meta := h.cyberPolicyOpsMeta(c, apiKey, account, model, platform, requestIsStream(c), cyberBlockKey)
	responseBody := []byte(mark.Body)
	promptExcerpt := currentOpenAICyberWarningPromptExcerpt(c)
	if h.contentModerationService != nil {
		input := buildOpenAICyberWarningInput(c, apiKey, account, model, mark.UpstreamStatus, responseBody, mark.Message, promptExcerpt)
		inScope, err := h.contentModerationService.CyberWarningInScope(c.Request.Context(), input)
		if err != nil {
			requestLogger(c, "handler.openai_gateway.cyber_policy").Warn("content_moderation.cyber_policy_scope_check_failed", zap.Error(err))
			return false
		}
		if !inScope {
			return false
		}
	}
	c.Set(cyberPolicyRecordedKey, true)
	h.recordOpenAICyberWarningWithPromptExcerpt(c, requestLogger(c, "handler.openai_gateway.cyber_policy"), apiKey, account, model, mark.UpstreamStatus, responseBody, mark.Message, promptExcerpt)
	quotaPlatform := service.QuotaPlatform(c.Request.Context(), apiKey)
	// 提前拍成标量，避免在下方 goroutine 内访问 gin.Context。
	clientSessionID := service.ExtractClientSessionID(c)
	nativeCompactionV2 := service.IsOpenAINativeCompactionV2(c)
	if len(nativeCompaction) > 0 {
		nativeCompactionV2 = nativeCompaction[0]
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if forwardErrored && h.gatewayService != nil {
			h.gatewayService.RecordCyberPolicyUsageLog(ctx, service.CyberPolicyUsageInput{
				APIKey:             apiKey,
				Account:            account,
				Subscription:       subscription,
				RequestID:          meta.RequestID,
				Model:              model,
				Stream:             meta.Stream,
				InputTokens:        mark.UpstreamInTok,
				OutputTokens:       mark.UpstreamOutTok,
				InboundEndpoint:    meta.InboundEndpoint,
				UpstreamEndpoint:   meta.UpstreamEndpoint,
				UserAgent:          meta.UserAgent,
				IPAddress:          meta.ClientIP,
				ClientSessionID:    clientSessionID,
				RequestPayloadHash: requestPayloadHash,
				APIKeyService:      h.apiKeyService,
				QuotaPlatform:      quotaPlatform,
				NativeCompactionV2: nativeCompactionV2,
				ChannelUsageFields: channelFields,
			})
		}
		if h.gatewayService != nil && cyberBlockKey != "" {
			h.gatewayService.MarkCyberSessionBlocked(ctx, "", []string{cyberBlockKey})
		}
		if h.opsService != nil {
			enqueueOpsErrorLog(h.opsService, buildCyberPolicyOpsErrorEntry(meta, mark))
		}
	}()
	return true
}

func (h *OpenAIGatewayHandler) cyberPolicyOpsMeta(c *gin.Context, apiKey *service.APIKey, account *service.Account, model string, platform string, stream bool, sessionBlockKey string) cyberPolicyOpsErrorMeta {
	meta := cyberPolicyOpsErrorMeta{
		RequestID:        c.Writer.Header().Get("X-Request-Id"),
		ClientRequestID:  c.GetHeader("X-Request-Id"),
		Platform:         platform,
		Model:            strings.TrimSpace(model),
		Stream:           stream,
		InboundEndpoint:  GetInboundEndpoint(c),
		UpstreamEndpoint: GetUpstreamEndpoint(c, platform),
		UserAgent:        c.GetHeader("User-Agent"),
		ClientIP:         ip.GetClientIP(c),
		CreatedAt:        time.Now(),
		SessionBlockKey:  sessionBlockKey,
	}
	if c.Request != nil && c.Request.URL != nil {
		meta.RequestPath = c.Request.URL.Path
	}
	if apiKey != nil {
		meta.APIKeyID = apiKey.ID
		meta.APIKeyPrefix = keyPrefix(apiKey.Key, 8)
		meta.UserID = apiKey.UserID
		meta.GroupID = cloneHandlerInt64Ptr(apiKey.GroupID)
	}
	if account != nil {
		meta.AccountID = account.ID
	}
	return meta
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

func cloneHandlerInt64Ptr(in *int64) *int64 {
	if in == nil {
		return nil
	}
	v := *in
	return &v
}

func clearCyberPolicyTurnState(c *gin.Context) {
	if c == nil {
		return
	}
	service.ClearOpsCyberPolicy(c)
	c.Set(cyberPolicyRecordedKey, false)
	c.Set(openAICyberWarningRecordedKey, false)
}

func writeCyberSessionBlockedWSError(ctx context.Context, conn *coderws.Conn) {
	if conn == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_cyber_session_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "permission_error",
			"code":    "session_blocked_by_cyber_policy",
			"message": cyberSessionBlockedClientMsg,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_cyber_session_blocked","type":"error","error":{"type":"permission_error","code":"session_blocked_by_cyber_policy","message":"This session is blocked by cyber-security policy, please start a new session"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
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

func openAIFirstOutputFailoverExhausted(failoverErr *service.UpstreamFailoverError, switchCount *int) bool {
	if failoverErr == nil || !failoverErr.SafeToFailoverAfterWrite || switchCount == nil {
		return false
	}
	if *switchCount >= maxOpenAIFirstOutputTimeoutSwitches {
		return true
	}
	*switchCount = *switchCount + 1
	return false
}

// errorResponse returns OpenAI API format error response
func (h *OpenAIGatewayHandler) errorResponse(c *gin.Context, status int, errType, message string) {
	// body-signal compact 心跳可能已把响应头提交为 200：JSON 错误体会与已
	// 提交的 SSE 流交错，必须降级为 response.failed 终止事件（#3887）。
	if service.StopOpenAICompactSSEKeepaliveCommitted(c) {
		service.MarkOpsStreamError(c, errType, message, status)
		if writeResponsesFailedSSE(c, errType, "", message) {
			return
		}
	}
	c.JSON(status, gin.H{
		"error": gin.H{
			"type":    errType,
			"message": message,
		},
	})
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

func openAIWSIngressFallbackSessionSeed(userID, apiKeyID int64, groupID *int64) string {
	gid := int64(0)
	if groupID != nil {
		gid = *groupID
	}
	return fmt.Sprintf("openai_ws_ingress:%d:%d:%d", gid, userID, apiKeyID)
}

func isOpenAIWSUpgradeRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Connection"))), "upgrade")
}

func closeOpenAIClientWS(conn *coderws.Conn, status coderws.StatusCode, reason string) {
	if conn == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = conn.Close(status, reason)
	_ = conn.CloseNow()
}

// openAIWSNextAttemptMessage 选择账号切换后的首个 WS 请求，并隔离其字节缓冲。
func openAIWSNextAttemptMessage(current, retryPayload []byte, retryCurrentTurn bool) ([]byte, bool) {
	if !retryCurrentTurn {
		return append([]byte(nil), current...), true
	}
	if len(retryPayload) == 0 {
		return nil, false
	}
	return append([]byte(nil), retryPayload...), true
}

func closeOpenAIWSFailoverExhausted(c *gin.Context, conn *coderws.Conn, failoverErr *service.UpstreamFailoverError) {
	intendedStatus := http.StatusBadGateway
	errorType := "upstream_error"
	errorCode := "upstream_ws_failover_exhausted"
	message := "upstream websocket proxy failed"
	closeStatus := coderws.StatusInternalError

	if failoverErr != nil {
		if reason := strings.TrimSpace(string(failoverErr.Reason)); reason != "" {
			errorCode = reason
		}
		if failoverErr.Stage == service.GatewayFailureStageAccountAuth {
			intendedStatus = http.StatusServiceUnavailable
			errorType = "api_error"
			message = service.GrokCredentialUnavailableClientMessage
			closeStatus = coderws.StatusTryAgainLater
		} else {
			switch failoverErr.StatusCode {
			case http.StatusTooManyRequests:
				intendedStatus = http.StatusTooManyRequests
				errorType = "rate_limit_error"
				message = "upstream rate limit exceeded, please retry later"
				closeStatus = coderws.StatusTryAgainLater
			case 529, http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
				intendedStatus = failoverErr.StatusCode
				message = "upstream service temporarily unavailable"
				closeStatus = coderws.StatusTryAgainLater
			case http.StatusUnauthorized, http.StatusForbidden:
				intendedStatus = failoverErr.StatusCode
				errorType = "authentication_error"
				message = "upstream websocket authentication failed"
				closeStatus = coderws.StatusPolicyViolation
			}
		}
	}

	service.MarkOpsStreamFailure(c, errorType, errorCode, message, intendedStatus)
	closeOpenAIClientWS(conn, closeStatus, message)
}

func writeContentModerationWSError(ctx context.Context, conn *coderws.Conn, decision *service.ContentModerationDecision) {
	if conn == nil || decision == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	message := strings.TrimSpace(decision.Message)
	if message == "" {
		message = "content moderation blocked this request"
	}
	payload, err := json.Marshal(gin.H{
		"event_id": "evt_content_moderation_blocked",
		"type":     "error",
		"error": gin.H{
			"type":    "invalid_request_error",
			"code":    contentModerationErrorCode(decision),
			"message": message,
		},
	})
	if err != nil {
		payload = []byte(`{"event_id":"evt_content_moderation_blocked","type":"error","error":{"type":"invalid_request_error","code":"content_policy_violation","message":"content moderation blocked this request"}}`)
	}
	writeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	_ = conn.Write(writeCtx, coderws.MessageText, payload)
}

type cyberSessionBlockWritePlan struct {
	scopeKey string
	keys     []string
}

func buildCyberSessionBlockWritePlan(apiKeyID int64, c *gin.Context, body []byte) cyberSessionBlockWritePlan {
	plan := cyberSessionBlockWritePlan{}
	if key := service.CyberSessionExplicitBlockKey(apiKeyID, c, body); key != "" {
		plan.keys = append(plan.keys, key)
	}
	transcriptKeys := service.CyberSessionTranscriptBlockKeys(apiKeyID, body)
	for _, key := range transcriptKeys {
		if len(plan.keys) == 0 || key != plan.keys[0] {
			plan.keys = append(plan.keys, key)
		}
	}
	if len(transcriptKeys) > 0 {
		plan.scopeKey = cyberSessionScopeKey(apiKeyID, c)
	}
	return plan
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
