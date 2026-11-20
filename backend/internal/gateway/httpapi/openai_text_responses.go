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
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// openai_text_responses.go admits Responses requests while retaining canonical
// session and mapped attempt input.
// Responses admission keeps canonical session input separate from
// effort/bootstrap rewrites and group-mapped attempt bytes. Prompt replacement
// follows Compact normalization, and the resulting bytes are retained for
// affinity and cyber checks so those identities do not change when later
// forwarding controls are adjusted.
//
// Continuation ownership is checked in the effective group before moderation
// and selection. Explicit image intent chooses permission, image admission and
// capability requirements, while broad intent supplies transformation hints.
// User-slot acquisition can change request context and output state, so its
// lease is copied into the earlier selection context and eligibility is
// checked after waiting. The admitted client model, mapped attempt model and
// prepared route then reach synchronous execution with image, user and request
// cleanup held until handler return.

// Responses admits HTTP input, continuation ownership and image/capability
// requirements before text execution. Authentication and dependencies precede
// body reading; Compact normalization and heartbeat startup occur before
// structural JSON validation, letting committed wait output use a protocol
// terminal on later failure.
//
// Prompt-replaced input is retained as SessionHashBody before effort and
// bootstrap rewrites. The current effective group validates ownership, and its
// mapped body/model drives explicit image admission while client identity
// remains available for observations. Image and user resources are held
// through the configured post-wait eligibility call, cyber/session handling
// and Execute. The selection context receives the acquired lease, and later
// request-context hints reach execution separately. All acquired cleanup
// remains deferred until the handler exits.
// @project-doc docs/interfaces/openai_upstream.md#openai_protocol_dispatch
func (h *OpenAITextHandler) Responses(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	// Keep the HTTP request admitted through image/user cleanup and synchronous
	// execution so shutdown cannot release shared services before this handler finishes.
	defer done()

	// Recovery uses the mutable output state because slot waits or Compact
	// keepalives may have committed a format before a later admission callback panics.
	streamStarted := false
	defer h.recoverResponsesPanic(c, &streamStarted)
	compactStartedAt := time.Now()
	defer h.LogRemoteCompactOutcome(c, compactStartedAt)
	h.backend.TransportHTTP(c)

	requestStart := time.Now()

	// Read the key and acting subject set by authentication.
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

	// Read the body after authentication and dependency validation.
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

	h.backend.ObserveRequest(c, "", false)
	ingressValidateStart := time.Now()
	var bootstrapElapsed time.Duration
	recordIngressValidate := func() {
		timing.ObserveSubstage(c.Request.Context(), "ingress_validate_ms", time.Since(ingressValidateStart)-bootstrapElapsed)
	}
	rawClientBody := body
	body, ok = h.normalizeOpenAIResponsesCompactRequest(c, reqLog, body)
	if !ok {
		recordIngressValidate()
		return
	}
	legacyCompact, nativeCompactionV2 := IsOpenAIResponsesCompactPath(c), IsBareOpenAIResponsesPath(c) && IsOpenAIRemoteCompactionV2Request(body)
	// A normalized body-signaled Compact stream can wait through admission and
	// execution. Start its delayed heartbeat here and stop it on return; errors
	// before the first beat can still choose JSON status, while later errors need a terminal.
	stopCompactKeepalive := h.backend.StartCompact(c, h.options.CompactKeepaliveInterval)
	defer stopCompactKeepalive()

	// Validate the original client JSON before accepting any compact-rewritten
	// copy so compact normalization cannot collapse duplicate or ambiguous keys.
	if err := wirejson.ValidateRequestObject(rawClientBody); err != nil {
		recordIngressValidate()
		LogRequestBodyParseFailure(reqLog, rawClientBody, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		recordIngressValidate()
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	// Retain prompt-replaced bytes for identity before effort and bootstrap
	// edits; affinity and cyber checks later use this view of the admitted client input.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "openai_responses")
	sessionHashBody := body

	// Extract fields needed for validation using a borrowed view and cloned short string.
	modelResult := wirejson.ParseView(body).Get("model")
	if !modelResult.Exists() || modelResult.Type != gjson.String || modelResult.String() == "" {
		recordIngressValidate()
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	reqModel := strings.Clone(modelResult.String())
	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolOpenAIResponses)
	if err != nil {
		recordIngressValidate()
		writeClientGroupFallbackError(c, err, h.errorResponse)
		return
	}
	if cappedBody, changed, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		recordIngressValidate()
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else if changed {
		body = cappedBody
	}
	// Repair automation and delegation bootstrap call outputs in a single pass when
	// the HTTP backend supports combined screening; session identity retains the pre-repair bytes.
	bootstrapStart := time.Now()
	if combined, hasCombined := h.backend.(interface {
		NormalizeBootstraps([]byte) ([]byte, bool, bool)
	}); hasCombined {
		normalizedBody, automationChanged, delegationChanged := combined.NormalizeBootstraps(body)
		if automationChanged || delegationChanged {
			body = normalizedBody
		}
		if automationChanged {
			reqLog.Info("openai.codex_automation_bootstrap_normalized",
				zap.String("normalization", "call_output_to_user_message"),
			)
		}
		if delegationChanged {
			reqLog.Info("openai.codex_delegation_bootstrap_normalized",
				zap.String("normalization", "call_output_to_user_message"),
			)
		}
	} else {
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
	}
	bootstrapElapsed = time.Since(bootstrapStart)
	timing.ObserveSubstage(c.Request.Context(), "bootstrap_ms", bootstrapElapsed)

	reqStream, ok := ParseOpenAICompatibleStream(body)
	if !ok {
		recordIngressValidate()
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", InvalidStreamFieldTypeMessage)
		return
	}
	if err := h.backend.ValidateTier(body); err != nil {
		recordIngressValidate()
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))
	previousResponseID := strings.Clone(strings.TrimSpace(wirejson.ParseView(body).Get("previous_response_id").String()))
	if previousResponseID != "" {
		previousResponseIDKind := h.backend.PreviousKind(previousResponseID)
		reqLog = reqLog.With(
			zap.Bool("has_previous_response_id", true),
			zap.String("previous_response_id_kind", previousResponseIDKind),
			zap.Int("previous_response_id_len", len(previousResponseID)),
		)
		if previousResponseIDKind == "message_id" {
			recordIngressValidate()
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
			recordIngressValidate()
			reqLog.Warn("openai.request_validation_failed", zap.String("reason", "previous_response_owner_mismatch"))
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "previous_response_id is not available for this user")
			return
		}
	}
	h.backend.SetOwner(c, subject.UserID, apiKey.ID)
	recordIngressValidate()

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)
	h.backend.Snapshot(c, protocol.ProtocolOpenAIResponses, body)

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, protocol.ProtocolOpenAIResponses, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	// Capture the post-policy request effort before planning so provider
	// effort_model_mapping rules participate in route resolution and selection.
	c.Request = c.Request.WithContext(requeststate.WithRoutingReasoningEffort(
		c.Request.Context(),
		requeststate.ExtractRoutingReasoningEffort(body, protocol.ProtocolOpenAIResponses),
	))
	// Group model G controls image concurrency and endpoint capability; client model R identifies logs and
	// sessions.
	// Store the group and mapped model in the execution plan.
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)
	forwardBody, routingModel, forwardImageIntent := h.backend.ImageIntent(reqModel, body, groupMapping, h.backend.Platform(apiKey))
	forwardModel := strings.TrimSpace(routingModel)
	if forwardModel == "" {
		forwardModel = reqModel
	}
	// Client-declared image intent controls permissions, concurrency, and provider capability; broader intent
	// guides tools and billing.
	imageIntent := h.backend.ExplicitImageIntent("/v1/responses", routingModel, forwardBody)
	selectionCtx := media.WithResponsesImageSelection(c.Request.Context(), forwardBody, openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) || h.options.ForceCodexCLI)
	if imageIntent {
		// Use group-mapped client image intent for the image-family concurrency marker.
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
			// Keep image capacity until the request has completed synchronous execution;
			// later admission rejection also returns through this same acquired cleanup.
			defer imageReleaseFunc()
		}
	}

	h.backend.SeedImageIntent(c, groupMapping.Mapped, forwardImageIntent)

	// Validate function_call_output context before forwarding to prevent upstream 400 errors.
	if !h.backend.ValidateTools(c, body, reqLog) {
		return
	}

	// Bind passthrough rules for downstream non-failover error handling.
	h.backend.BindErrors(c)

	// A missing subscription uses balance funding.
	subscription, _ := SubscriptionFromContext(c)
	requestPlatform := h.backend.Platform(apiKey)

	h.backend.AuthLatency(c, time.Since(requestStart).Milliseconds())
	routingStart := time.Now()

	userReleaseFunc, acquired := h.backend.UserSlot(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted, reqLog)
	if !acquired {
		return
	}
	// The image-selection context was captured before user waiting. Carry its
	// acquired lease into selection so provider admission uses this request's current resource handle.
	if lease := scheduler.RequestLease(c.Request.Context()); lease != nil {
		selectionCtx = scheduler.WithRequestLease(selectionCtx, lease)
	}
	// The resource binding already observes cancellation; retain completion
	// cleanup here so successful or rejected synchronous handling also releases the user slot.
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// Waiting can change funding eligibility. Invoke the configured admission
	// checker here; the default text binding uses the normal funding/RPM path with afterWait false.
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("openai.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.handleStreamingAwareError(c, status, code, message, streamStarted)
		return
	}

	// Derive affinity and explicit isolation from the saved pre-policy body,
	// keeping forwarding-control rewrites out of the session and cyber identity inputs.
	sessionStart := time.Now()
	explicitSessionHash := h.backend.SessionHash(c, OpenAIExplicitSession, sessionHashBody)
	sessionHash := h.backend.SessionHash(c, OpenAISelectionSession, sessionHashBody)
	if h.backend.RejectCyber(c, apiKey, sessionHashBody, reqModel, protocol.ProtocolOpenAIResponses) {
		timing.ObserveSubstage(c.Request.Context(), "session_ms", time.Since(sessionStart))
		return
	}
	if explicitSessionHash != "" {
		c.Request = c.Request.WithContext(requeststate.WithSessionIsolation(c.Request.Context(), session.SessionIsolationSourceOpenAI, explicitSessionHash))
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, session.SessionIsolationSourceOpenAI, explicitSessionHash); h.handleOpenAISessionIsolationError(c, err, streamStarted) {
			timing.ObserveSubstage(c.Request.Context(), "session_ms", time.Since(sessionStart))
			return
		}
	}
	c.Request = c.Request.WithContext(h.backend.GuardianContext(
		c.Request.Context(), c, sessionHashBody, reqModel,
	))
	timing.ObserveSubstage(c.Request.Context(), "session_ms", time.Since(sessionStart))
	requireCompact := legacyCompact

	// OpenAI image generation requires Responses support; raw Chat cannot generate images (#4417).
	// Grok has its own Responses execution. Reuse the admitted image intent from group model G and forwardBody.
	// A passive Codex image_gen namespace leaves Chat-only providers eligible (#4476).
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
