// OpenAI 兼容入口保留协议各自的读取、准入和等待顺序。
package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// Responses 保留 HTTP Responses 的 compact、归属和等待后资金检查顺序。
// @project-doc docs/interfaces/openai_upstream.md#openai_protocol_dispatch
func (h *OpenAITextHandler) Responses(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	// 局部兜底：确保该 handler 内部任何 panic 都不会击穿到进程级。
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	compactStartedAt := time.Now()
	defer h.LogRemoteCompactOutcome(c, compactStartedAt)
	h.backend.TransportHTTP(c)

	requestStart := time.Now()

	// 从认证中间件读取 Key 与行为主体。
	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.openai_gateway.responses",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	if !h.backend.Dependencies(c, reqLog) {
		return
	}

	// 在认证及依赖校验后读取请求体。
	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.backend.ReadFailure(reqLog, c.Request, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to read request body")
		return
	}

	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)
	body, ok = h.normalizeOpenAIResponsesCompactRequest(c, reqLog, body)
	if !ok {
		return
	}
	legacyCompact, nativeCompactionV2 := IsOpenAIResponsesCompactPath(c), IsBareOpenAIResponsesPath(c) && IsOpenAIRemoteCompactionV2Request(body)
	// body-signal compact：上游 unary 等待期间向下游发 SSE 注释行心跳，防止
	// 反向代理空闲超时掐断长压缩连接（#3887）。首拍延迟一个心跳间隔，快速
	// 失败仍走 JSON+状态码链路；未标记客户端流式或间隔为 0 时是 no-op。
	stopCompactKeepalive := h.backend.StartCompact(c, h.options.CompactKeepaliveInterval)
	defer stopCompactKeepalive()

	// 校验请求体 JSON 合法性
	if !gjson.ValidBytes(body) {
		LogRequestBodyParseFailure(reqLog, body, nil)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	// 用户提示词替换必须在 compact 归一化之后、模型解析之前执行。
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "openai_responses")
	sessionHashBody := body

	// 使用 gjson 只读提取字段做校验，避免完整 Unmarshal
	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	if cappedBody, changed, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else if changed {
		body = cappedBody
	}
	if normalizedBody, changed := h.backend.NormalizeBootstrap(body, false); changed {
		body = normalizedBody
		reqLog.Info("openai.codex_automation_bootstrap_normalized",
			zap.String("normalization", "call_output_to_user_message"),
		)
	}
	if normalizedBody, changed := h.backend.NormalizeBootstrap(body, true); changed {
		body = normalizedBody
		reqLog.Info("openai.codex_delegation_bootstrap_normalized",
			zap.String("normalization", "call_output_to_user_message"),
		)
	}

	reqStream, ok := ParseOpenAICompatibleStream(body)
	if !ok {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", InvalidStreamFieldTypeMessage)
		return
	}
	if err := h.backend.ValidateTier(body); err != nil {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	previousResponseID := strings.TrimSpace(gjson.GetBytes(body, "previous_response_id").String())
	if previousResponseID != "" {
		previousResponseIDKind := h.backend.PreviousKind(previousResponseID)
		reqLog = reqLog.With(
			zap.Bool("has_previous_response_id", true),
			zap.String("previous_response_id_kind", previousResponseIDKind),
			zap.Int("previous_response_id_len", len(previousResponseID)),
		)
		if previousResponseIDKind == "message_id" {
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
		owned, ownershipErr := h.backend.ValidateOwner(
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
	h.backend.SetOwner(c, subject.UserID, apiKey.ID)

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	h.backend.Snapshot(c, protocol.ProtocolOpenAIResponses, body)

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, protocol.ProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// 分组映射模型 G 决定生图并发和账号端点能力，客户端模型 R 继续用于日志与会话语义。
	// 当前分组和分组映射结果进入独立计划，不改变原解析位置。
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)
	forwardBody, routingModel, forwardImageIntent := h.backend.ImageIntent(reqModel, body, groupMapping, h.backend.Platform(apiKey))
	forwardModel := strings.TrimSpace(routingModel)
	if forwardModel == "" {
		forwardModel = reqModel
	}
	// 权限、并发和账号能力只看显式意图；宽泛意图继续供转发层处理工具与计费。
	imageIntent := h.backend.ExplicitImageIntent("/v1/responses", routingModel, forwardBody)
	// 只有 HTTP Responses 入口会按账号开关进入自动透传，供 upstream 限制计算真实模型。
	selectionCtx := h.backend.PassthroughContext(c.Request.Context())
	// 错误诊断也必须看到相同入口语义，避免把可透传模型误报为 model_not_found。
	c.Request = c.Request.WithContext(selectionCtx)
	if imageIntent {
		// 生图家族限流依赖上下文标记，必须使用分组映射后的显式意图结果。
		selectionCtx = h.backend.ImageContext(selectionCtx)
	}
	if imageIntent && !h.backend.AllowsImages(apiKey) {
		h.backend.FeatureDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", h.backend.ImagePermissionMessage())
		return
	}
	var imageReleaseFunc func()
	if imageIntent {
		var imageAcquired bool
		imageReleaseFunc, imageAcquired = h.backend.ImageSlot(c, streamStarted)
		if !imageAcquired {
			return
		}
		if imageReleaseFunc != nil {
			defer imageReleaseFunc()
		}
	}

	h.backend.SeedImageIntent(c, groupMapping.Mapped, forwardImageIntent)

	// 提前校验 function_call_output 是否具备可关联上下文，避免上游 400。
	if !h.backend.ValidateTools(c, body, reqLog) {
		return
	}

	// 绑定错误透传服务，允许 service 层在非 failover 错误场景复用规则。
	h.backend.BindErrors(c)

	// 订阅为空时保持原余额路径。
	subscription, _ := SubscriptionFromContext(c)
	requestPlatform := h.backend.Platform(apiKey)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.backend.UserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	// 将已取得用户租约加入保留 HTTP passthrough 标记的选择上下文。
	if lease := scheduler.RequestLease(c.Request.Context()); lease != nil {
		selectionCtx = scheduler.WithRequestLease(selectionCtx, lease)
	}
	// 确保请求取消时也会释放槽位，避免长连接被动中断造成泄漏
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// 等待成功后按原时点复查资金权益。
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	// 会话标识仍优先读取 Header，再回退 prompt_cache_key。
	explicitSessionHash := h.backend.SessionHash(c, OpenAIExplicitSession, sessionHashBody)
	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, sessionHashBody)
	if h.backend.RejectCyber(c, apiKey, sessionHashBody, reqModel, protocol.ProtocolOpenAIResponses) {
		return
	}
	if explicitSessionHash != "" {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, session.SessionIsolationSourceOpenAI, explicitSessionHash); h.handleOpenAISessionIsolationError(c, err, streamStarted) {
			return
		}
	}
	c.Request = c.Request.WithContext(h.backend.GuardianContext(
		c.Request.Context(), c, sessionHashBody, reqModel,
	))
	requireCompact := legacyCompact

	// 生图意图的 /v1/responses 请求必须调度到确实支持 Responses API 的账号，否则
	// 会在 forward 阶段被静默降级为无法生图的 Chat Completions 直转（#4417）。
	// 仅对 OpenAI 平台生效：Grok 生图走独立的 forwardGrokResponses 路径，不应被过滤。
	// 复用前置权限与并发阶段按分组映射模型 G 和未再修改的 forwardBody 确认的显式生图意图，
	// 避免大 tools 请求重复扫描。
	// 该判断已排除 Codex 被动 image_gen namespace，避免 CC-only 账号被误过滤（#4476）。
	requiredCapability := textflow.RequiredResponsesCapability(
		imageIntent,
		nativeCompactionV2,
		legacyCompact,
		requestPlatform,
	)

	call := OpenAITextCall{
		Route:    groupMappingRoutePlan,
		Protocol: protocol.ProtocolOpenAIResponses, Key: apiKey, Subject: subject, Subscription: subscription,
		Body: body, ForwardBody: forwardBody, SessionHashBody: sessionHashBody,
		Model: reqModel, ForwardModel: forwardModel, SessionHash: sessionHash, PreviousResponseID: previousResponseID,
		Platform: requestPlatform, Stream: reqStream, NativeCompactionV2: nativeCompactionV2, LegacyCompact: legacyCompact,
		RequireCompact: requireCompact, StreamStarted: &streamStarted, SelectionContext: selectionCtx,
		Mapping: groupMapping, RoutingStart: routingStart, RequiredCapability: requiredCapability, Log: reqLog,
	}
	h.executeText(c, call, execution.TextOpenAIResponses)
}
