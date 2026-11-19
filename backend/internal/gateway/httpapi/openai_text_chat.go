package httpapi

import (
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// openai_text_chat.go admits Chat Completions requests and prepares their
// routing and session identities.
// Chat admission validates stream and tier before provider execution. Prompt
// replacement occurs before model policy and session derivation so later
// checks use the rewritten client body, while logs and client errors retain
// the original requested model. Image-only eligibility uses the final provider
// model during selection.
//
// Group route mapping and function-tool use are captured before moderation. User
// admission can wait and start output, so eligibility is checked afterward and
// its rejection uses the resulting started flag. Provider affinity, explicit
// session isolation and upstream prompt-cache seeds are then derived
// separately. Only explicit identity enters session isolation, while the
// admitted body, route and output-state pointer reach one synchronous
// execution call with deferred request and slot cleanup.

// ChatCompletions admits a Chat HTTP request and invokes the bound text
// executor. Authentication and dependency checks precede lenient body reading;
// prompt replacement, model/group resolution, effort policy and strict
// stream/tier validation prepare the canonical request.
//
// Request context retains function-tool use for candidate checks after each
// provider's model mapping. Observations and execution attribution keep the
// client model. After user-slot waiting, Eligibility runs the configured
// funding admission and its normal RPM path. Session affinity and cache seeds
// are distinct from explicit isolation identity, and cyber/session rejection
// stops before Execute. Returned request and slot cleanup remain deferred
// until synchronous execution or an earlier rejection ends the handler.
func (h *OpenAITextHandler) ChatCompletions(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	// Keep request lifetime active through admission, execution and deferred slot
	// release so application shutdown waits for the complete HTTP handling interval.
	defer done()

	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)

	requestStart := time.Now()

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
		"handler.openai_gateway.chat_completions",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	if !h.backend.Dependencies(c, reqLog) {
		return
	}

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.backend.ReadFailure(reqLog, c.Request, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}
	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if err := wirejson.ValidateRequestObject(body); err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	// Replace user prompts before model resolution, moderation, and session hashing so later steps share the
	// rewritten body.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "chat_completions")

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolOpenAIChatCompletions)
	if err != nil {
		writeClientGroupFallbackError(c, err, h.errorResponse)
		return
	}
	if cappedBody, changed, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else if changed {
		body = cappedBody
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
	// Capture function-tool use once so candidate filtering can check the final
	// provider route without parsing the body again after model mapping.
	if protocolopenai.ChatUsesFunctionTools(body) {
		c.Request = c.Request.WithContext(requeststate.WithChatFunctionTools(c.Request.Context()))
	}
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)

	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	h.backend.Snapshot(c, protocol.ProtocolOpenAIChatCompletions, body)

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, protocol.ProtocolOpenAIChatCompletions, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	h.backend.BindErrors(c)

	subscription, _ := SubscriptionFromContext(c)
	requestPlatform := h.backend.Platform(apiKey)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.backend.UserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	// Retain returned user cleanup through eligibility and execution; the resource
	// binding also observes cancellation, so completion and disconnect can both invoke it.
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// Balance, subscription and normal admission can change while the user slot
	// waits. Invoke the configured checker after acquisition before provider execution.
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai_chat_completions.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, body)
	promptCacheKey := h.backend.SessionHash(c, OpenAIPromptCacheSession, body)
	explicitSessionHash := h.backend.SessionHash(c, OpenAIExplicitSession, body)
	if h.backend.RejectCyber(c, apiKey, body, reqModel, protocol.ProtocolOpenAIChatCompletions) {
		return
	}
	// Explicit client identity is the isolation input; general affinity can use
	// derived content and must not substitute for that separate session admission.
	if explicitSessionHash != "" {
		c.Request = c.Request.WithContext(requeststate.WithSessionIsolation(c.Request.Context(), session.SessionIsolationSourceOpenAI, explicitSessionHash))
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, session.SessionIsolationSourceOpenAI, explicitSessionHash); h.handleOpenAISessionIsolationError(c, err, streamStarted) {
			return
		}
	}

	call := OpenAITextCall{
		Route:    groupMappingRoutePlan,
		Protocol: protocol.ProtocolOpenAIChatCompletions, Key: apiKey, Subject: subject, Subscription: subscription,
		Body: body, Model: reqModel, SessionHash: sessionHash, Platform: requestPlatform, Stream: reqStream,
		StreamStarted: &streamStarted, RoutingStart: routingStart, Mapping: groupMapping, Log: reqLog, PromptCacheKey: promptCacheKey,
	}
	h.executeText(c, call, execution.TextOpenAIChat)
}
