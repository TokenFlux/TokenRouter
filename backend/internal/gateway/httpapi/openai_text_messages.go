package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
)

// openai_text_messages.go admits Messages requests and prepares group mapping
// and session isolation.
// Messages protocol permission is checked before body reading and dependency
// processing in this entry. Admission reads streaming preference through gjson
// coercion, while Chat uses a stricter field parser; later Messages conversion
// still owns its own protocol decoding and compatibility decisions.
//
// Moderation precedes group route planning here, and the normalized group
// model is retained for provider-layer mapping while client model identity
// stays available for observations. User-slot admission precedes the
// configured eligibility call. Selection and prompt-cache seeds can use Claude
// Code metadata, but explicit session isolation follows its own
// header/metadata precedence. The handler then passes this admitted state and
// live output flag into synchronous execution, retaining request and slot
// cleanup until return.

// Messages admits HTTP Messages input for compatible text execution. It
// validates key, acting subject and group protocol permission before body
// reading, then prepares client identity and binds group effort policy for
// later conversion. At this admission step, stream preference uses gjson Bool
// coercion.
//
// Moderation runs before group model mapping and provider-layer model
// preparation. After user-slot waiting, the configured Eligibility call checks
// admission before cyber/session handling and Execute. Claude Code metadata
// can refine affinity/cache identity, and missing explicit header identity can
// use metadata session isolation under its gateway source. Client model,
// mapped provider model and prompt-cache seed remain separate execution
// values, with request and slot cleanup deferred through all exits.
func (h *OpenAITextHandler) Messages(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "anthropic")
	if !accepted {
		return
	}
	// Request lifetime covers all admission callbacks and synchronous execution;
	// deferred user cleanup runs before the entry leaves the application request barrier.
	defer done()

	streamStarted := false
	defer h.recoverAnthropicMessagesPanic(c, &streamStarted)

	requestStart := time.Now()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.anthropicErrorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.openai_gateway.messages",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// Group protocol permission precedes reading so a denied Messages entry
	// cannot consume or validate the caller's body before its policy rejection.
	if !h.backend.AllowsMessages(apiKey) {
		h.backend.PolicyDenied(c)
		h.anthropicErrorResponse(c, http.StatusForbidden, "permission_error",
			"This group does not allow Anthropic Messages requests")
		return
	}

	if !h.backend.Dependencies(c, reqLog) {
		return
	}

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := openAITextMaxBytesError(err); ok {
			h.anthropicErrorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}
	if len(body) == 0 {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	if err := wirejson.ValidateRequestObject(body); err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	// Replace user prompts before model resolution, moderation, and session hashing so later steps share the
	// rewritten body.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")

	modelResult := gjson.GetBytes(body, "model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := modelResult.String()
	// Custom test/backends can omit the optional preparer. Use the shared
	// client-context validation so this entry still binds identity before group selection.
	if preparer, ok := h.backend.(interface {
		// PrepareMessages binds and validates client identity before group fallback
		// and provider selection. A returned error produces this entry's Messages
		// validation rejection before moderation or the execution loop can run.
		PrepareMessages(*gin.Context, []byte) error
	}); ok {
		if err := preparer.PrepareMessages(c, body); err != nil {
			h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
	} else if err := PrepareMessageClientContext(c, body, nil); err != nil {
		h.anthropicErrorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolAnthropicMessages)
	if err != nil {
		writeClientGroupFallbackError(c, err, h.anthropicErrorResponse)
		return
	}
	h.backend.MessageReasoning(c, apiKey, body)
	reqStream := gjson.GetBytes(body, "stream").Bool()

	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	h.backend.Snapshot(c, protocol.ProtocolAnthropicMessages, body)

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, protocol.ProtocolAnthropicMessages, reqModel, body); decision != nil && decision.Blocked {
		h.anthropicErrorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// Plan from admitted canonical input after moderation. Retain the group
	// model for provider rules while the original model remains available for client attribution.
	groupMappingMsgRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMappingMsg := groupMappingMsgRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingMsgRoutePlan)
	groupMappedModel := strings.TrimSpace(groupMappingMsg.MappedModel)
	if groupMappedModel == "" {
		groupMappedModel = reqModel
	}
	providerLayerModel := h.backend.MessageProviderModel(c.Request.Context(), apiKey, groupMappedModel)

	// Bind passthrough rules for downstream non-failover error handling.
	h.backend.BindErrors(c)

	subscription, _ := SubscriptionFromContext(c)
	requestPlatform := h.backend.Platform(apiKey)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.backend.UserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	// Hold the acquired user slot through eligibility and execution; resource
	// cleanup also observes cancellation, and the returned callback tolerates completion cleanup.
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// Admission values may change while waiting for user capacity. Run the
	// configured funding/RPM checker before session handling and the provider loop begins.
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai_messages.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.anthropicStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, body)
	promptCacheKey := h.backend.SessionHash(c, OpenAIPromptCacheSession, body)
	sessionHash, promptCacheKey = h.backend.MetadataSession(c, sessionHash, promptCacheKey, reqModel, body)
	if h.backend.RejectCyber(c, apiKey, body, reqModel, protocol.ProtocolAnthropicMessages) {
		return
	}
	isolationSource := session.SessionIsolationSourceOpenAI
	explicitSessionHash := h.backend.SessionHash(c, OpenAIExplicitSession, body)
	// Header-based explicit identity takes precedence for isolation; Messages
	// metadata can supply a gateway-source identity when that header seed is absent.
	if explicitSessionHash == "" {
		if isolationSessionID := MetadataSessionID(gjson.GetBytes(body, "metadata.user_id").String()); isolationSessionID != "" {
			explicitSessionHash = isolationSessionID
			isolationSource = session.SessionIsolationSourceGateway
		}
	}
	if explicitSessionHash != "" {
		c.Request = c.Request.WithContext(requeststate.WithSessionIsolation(c.Request.Context(), isolationSource, explicitSessionHash))
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, isolationSource, explicitSessionHash); h.handleAnthropicSessionIsolationError(c, err, streamStarted) {
			return
		}
	}

	call := OpenAITextCall{
		Route:    groupMappingMsgRoutePlan,
		Protocol: protocol.ProtocolAnthropicMessages, Key: apiKey, Subject: subject, Subscription: subscription,
		Body: body, Model: reqModel, SessionHash: sessionHash, Platform: requestPlatform, Stream: reqStream,
		StreamStarted: &streamStarted, RoutingStart: routingStart, Log: reqLog, Mapping: groupMappingMsg,
		ProviderLayerModel: providerLayerModel, PromptCacheKey: promptCacheKey,
	}
	h.executeText(c, call, execution.TextOpenAIMessages)
}
