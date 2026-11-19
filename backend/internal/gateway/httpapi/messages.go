package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// messages.go prepares Messages requests for policy checks, user admission,
// session selection, and text execution.
// Messages admission keeps the client model and rewritten body available while
// preparing provider execution. Prompt replacement precedes parsing,
// moderation and session hashing; client-group resolution and reasoning policy
// establish the route plan before the selected text executor receives the
// request.
//
// Funding is checked after the user-slot wait because eligibility can change
// while the request is queued. Explicit metadata session identity is checked
// for ownership, while content/session hashing supplies sticky selection
// hints. The original model, group mapping, provider binding and optional
// Gemini attempt body remain separate fields so conversion and completion can
// interpret each representation after provider selection.

// MessagesHTTPOptions retains body normalization, provider-switch limits, and
// the client preparation settings used when selecting compatible Responses routes.
type MessagesHTTPOptions struct {
	ForceCodexCLI bool // configured Codex client preparation
	// MaxBodyBytes limits lenient JSON normalization output, after the shared
	// body reader runs. Nonpositive selects the protocol normalizer's 64 MiB default.
	MaxBodyBytes int64
	// MaxSwitches and MaxGeminiSwitches retain configured switch counts from
	// assembly. Messages HTTP preparation stores them while execution owns attempt loops.
	// MaxSwitches: general provider switch allowance.
	// MaxGeminiSwitches: Gemini provider switch allowance.
	MaxSwitches, MaxGeminiSwitches int
}

// MessagesPrompt supplies request-body prompt replacement before admission
// reads its contents.
type MessagesPrompt interface {
	// ApplyUserPromptReplacementToBody returns request bytes with the configured
	// user-prompt policy applied for its protocol. Admission uses those bytes for
	// parsing, moderation and hashing, so callers pass the returned body onward.
	ApplyUserPromptReplacementToBody(context.Context, []byte, string) []byte
}

// MessagesCall holds identity, routing, and execution state for one request.
type MessagesCall struct {
	Key     *apikey.APIKey      // effective authenticated request key
	Subject authctx.AuthSubject // authenticated request subject
	// Subscription carries the request's optional funding choice; nil permits
	// balance-funded eligibility and completion through the corresponding services.
	Subscription *billing.UserSubscription
	Parsed       *requeststate.ParsedRequest // parsed client request
	// Body and GeminiBody retain client input and the separately prepared
	// attempt for forced Gemini execution. Group mapping can change GeminiBody
	// while Body remains available for request metadata and completion input.
	// Body: retained client request bytes.
	// GeminiBody: prepared group-mapped attempt bytes.
	Body, GeminiBody []byte
	// Model and GeminiModel distinguish client identity from the prepared mapped
	// target. Platform is empty for ordinary mixed-provider selection; SessionKey
	// retains the sticky lookup identity independently of either model name.
	// Model: original client model. GeminiModel: prepared mapped model.
	// Platform: optional forced platform. SessionKey: sticky provider key.
	Model, GeminiModel, Platform, SessionKey string
	// HasBoundSession relates SessionKey to BoundProviderID: Messages admission sets
	// it after a nonempty key and positive provider lookup, while Stream and
	// ClaudeCode retain independent client execution and detection flags.
	// Stream: client streaming intent. ClaudeCode: detected client flag.
	// HasBoundSession: observed sticky provider presence.
	Stream, ClaudeCode, HasBoundSession bool
	BoundProviderID                     int64 // cached sticky provider identity
	// StreamStarted is shared with the synchronous HTTP output adapter; slot
	// waits can update it before later error handling selects its protocol response.
	StreamStarted *bool
	Log           *zap.Logger                // request diagnostic logger
	Route         routing.RoutePlan          // resolved request routing plan
	Mapping       routing.GroupMappingResult // group model mapping
}

// MessagesBackend supplies admission, routing, and observation operations for Messages requests.
type MessagesBackend interface {
	// Access returns the effective authenticated key and presence flag. Messages
	// requires a usable nonnil key when that flag is true because it immediately
	// reads key identity and group; a type-presence flag alone cannot establish
	// that requirement for a typed nil pointer.
	Access(*gin.Context) (*apikey.APIKey, bool)
	// CompatibilityMetrics records compatibility observations when Messages
	// returns after authentication created request logging. Admission can fail
	// after that point, so this observation also covers parse, policy, funding
	// and isolation rejection paths.
	CompatibilityMetrics(*zap.Logger)
	// ObserveRequest records the current client model and streaming intent for
	// Ops.
	ObserveRequest(*gin.Context, string, bool)
	// ObserveEndpoint records the normalized request endpoint and execution
	// category for Ops.
	ObserveEndpoint(*gin.Context, bool)
	// Reasoning applies group reasoning policy and returns body, changed flag and
	// error. A changed body must replace ParsedRequest state; an error rejects
	// admission before route execution begins.
	Reasoning(*gin.Context, *apikey.APIKey, []byte) ([]byte, bool, error)
	// PolicyDenied records local policy rejection as a client business
	// limitation.
	PolicyDenied(*gin.Context)
	// Plan resolves routing from the current Key and client-requested model. Its
	// returned plan describes requested and group-mapped identities retained for
	// selection and completion, without provider-specific mapping at this
	// admission stage.
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	// BindPlan installs the resolved route plan on the active HTTP request
	// context. The handler calls it before execution so selection and completion
	// read the same group/model decision and existing request values remain
	// available.
	BindPlan(*gin.Context, routing.RoutePlan)
	// BindProbe installs the recognized one-token Haiku probe marker before
	// client detection. The detector receives that true marker to distinguish
	// already recognized probes from ordinary request validation and associated
	// client checks.
	BindProbe(*gin.Context)
	// Probe returns the one-token Haiku marker installed for this request, or
	// false when absent. The handler passes this value into client detection so
	// the marked probe can follow its applicable validation path.
	Probe(*gin.Context) bool
	// BindClient stores detected Claude identity and any usable client version.
	// Version storage is conditional on a nonempty version for a Claude client,
	// while the client flag is installed for every detection.
	BindClient(*gin.Context, ClientDetection)
	// BindThinking stores parsed thinking intent before provider execution reads
	// the request context. Antigravity model identity and health handling use
	// that flag to interpret the final target and applicable model-rate
	// observations.
	BindThinking(*gin.Context, bool)
	// ClientVersion returns the client version installed on this request context.
	// A missing value is empty, leaving version rejection to the configured bound
	// rules interpreted by the HTTP handler.
	ClientVersion(*gin.Context) string
	// ClientVersionBounds returns configured minimum and maximum Claude client
	// versions. Empty values disable their respective bounds, and both empty let
	// the HTTP handler skip version rejection for this request.
	ClientVersionBounds(context.Context) (string, string)
	// Moderate checks the rewritten Messages body with its authenticated key and
	// subject. Nil or an unblocked decision permits execution; a blocked decision
	// supplies the client status/message before provider selection.
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, string, []byte) *moderation.Decision
	// BindErrors binds final response rules when that service is configured. It
	// can leave request state unchanged when rules are absent, allowing ordinary
	// protocol error defaults to apply.
	BindErrors(*gin.Context)
	// Eligibility checks current funding after the user-slot wait with an
	// optional subscription. A returned billing error stops execution and can
	// carry Retry-After guidance for the HTTP response.
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	// ForcedPlatform returns the platform restriction installed by API-key
	// middleware, if present. An absent restriction permits the ordinary
	// cross-platform selection route; the handler treats forced Gemini as a
	// separate preparation kind.
	ForcedPlatform(*gin.Context) (string, bool)
	// Isolate checks explicit metadata-session ownership under the current key
	// and subject. Nil permits admission; conflict and other errors produce
	// different client responses, and an absent bound isolation callback can
	// permit requests.
	Isolate(context.Context, *apikey.APIKey, int64, string) error
	// CachedSession returns the provider associated with this group and sticky
	// key. Messages ignores the returned error; the returned positive identity
	// controls whether provider/group selection hints are installed.
	CachedSession(context.Context, *int64, string) (int64, error)
	// Prefetch installs a returned sticky provider/group pair on this request
	// context before execution starts. Selection reads that pair as request-local
	// hints while the resolved route and caller identity stay available.
	Prefetch(*gin.Context, int64, int64)
	// PrepareGemini builds the separately group-mapped attempt for forced Gemini
	// Messages. It receives retained client body/model and key, returning parsed
	// attempt data or an admission error before the shared executor starts.
	PrepareGemini(context.Context, MessagesCall) (*requeststate.ParsedRequest, error)
	// MarkStream records protocol-stream error metadata after HTTP output has
	// begun.
	MarkStream(*gin.Context, string, string, int)
	// FailoverObservation records provider-switch diagnostics using request
	// context and supplied values.
	FailoverObservation(context.Context, string, map[string]any)
}

// MessagesHandler uses application-bound dependencies and allocates execution state per request.
type MessagesHandler struct {
	executor        execution.Executor // shared text execution
	RequestLifetime                    // application request barrier

	options     MessagesHTTPOptions // retained HTTP admission settings
	backend     MessagesBackend     // bound admission and observation
	prompt      MessagesPrompt      // request prompt replacement
	concurrency *ConcurrencyHelper  // shared user concurrency support
}

// NewMessagesHandler retains HTTP admission and execution dependencies for
// Messages requests. Callers supply usable backend, prompt, concurrency and
// executor services; the handler allocates request-specific state while
// retaining the bound shared services.
func NewMessagesHandler(options MessagesHTTPOptions, backend MessagesBackend, prompt MessagesPrompt, concurrency *ConcurrencyHelper, executor execution.Executor) *MessagesHandler {
	return &MessagesHandler{options: options, backend: backend, prompt: prompt, concurrency: concurrency, executor: executor}
}

// Messages prepares a Claude-compatible request after middleware
// authentication and subject binding. Prompt replacement precedes parsing,
// client detection, reasoning policy and moderation. The user slot is acquired
// before the final funding check and released on request exit or cancellation.
//
// Metadata identity supplies session isolation, while the generated session
// key and advisory provider lookup establish sticky hints. Forced Gemini
// retains its own prepared model/body beside the client request; other routes
// pass parsed Messages input. The shared executor owns provider attempts and
// completion using the captured route, funding and client state.
func (h *MessagesHandler) Messages(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "anthropic")
	if !accepted {
		return
	}
	defer done()

	// Authentication middleware supplies the key and user context.
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
		"handler.gateway.messages",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	defer h.backend.CompatibilityMetrics(reqLog)

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.options.MaxBodyBytes)
	if err != nil {
		if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
			h.errorResponse(c, http.StatusRequestEntityTooLarge, "invalid_request_error", BodyTooLargeMessage(maxErr.Limit))
			return
		}
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyReadErrorMessage(err))
		return
	}

	if len(body) == 0 {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Request body is empty")
		return
	}

	h.backend.ObserveRequest(c, "", false)
	ingressValidateStart := time.Now()
	recordIngressValidate := func() {
		timing.ObserveSubstage(c.Request.Context(), "ingress_validate_ms", time.Since(ingressValidateStart))
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		recordIngressValidate()
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}

	// Apply prompt replacement before constructing parsed state so moderation and
	// sticky identity both describe the content later forwarded for this request.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")

	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		recordIngressValidate()
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	reqModel := parsedReq.Model
	reqStream := parsedReq.Stream
	reqLog = reqLog.With(zap.String("model", reqModel), zap.Bool("stream", reqStream))

	// Mark max_tokens=1 Haiku probes before client detection.
	// The Claude Code validator uses this marker to skip probe validation.
	if clientmeta.IsHaikuProbe(reqModel, parsedReq.MaxTokens) {
		h.backend.BindProbe(c)
	}

	detection := DetectClaudeCodeRequest(c, body, parsedReq, h.backend.Probe(c))
	h.backend.BindClient(c, detection)
	isClaudeCodeClient := detection.ClaudeCode

	// Apply configured version bounds to Claude Code clients.
	if !h.checkClientVersion(c, detection) {
		recordIngressValidate()
		return
	}

	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolAnthropicMessages)
	if err != nil {
		recordIngressValidate()
		writeClientGroupFallbackError(c, err, h.errorResponse)
		return
	}
	if policyBody, changed, policyErr := h.backend.Reasoning(c, apiKey, body); policyErr != nil {
		recordIngressValidate()
		h.backend.PolicyDenied(c)
		h.errorResponse(c, http.StatusForbidden, "permission_error", policyErr.Error())
		return
	} else if changed {
		if err := parsedReq.ReplaceBody(policyBody); err != nil {
			recordIngressValidate()
			h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to apply reasoning effort policy")
			return
		}
		body = parsedReq.Body.Bytes()
	}
	// Capture the group and model mapping in this request's route plan.
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, reqModel)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)

	// Antigravity uses thinking state to derive the final model key and model rate limits.
	h.backend.BindThinking(c, parsedReq.ThinkingEnabled)

	h.backend.ObserveRequest(c, reqModel, reqStream)
	h.backend.ObserveEndpoint(c, reqStream)

	if reqModel == "" {
		recordIngressValidate()
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}
	recordIngressValidate()

	if decision := h.backend.Moderate(c, reqLog, apiKey, subject, reqModel, body); decision != nil && decision.Blocked {
		h.errorResponse(c, ModerationHTTPStatus(decision), "content_policy_violation", decision.Message)
		return
	}

	streamStarted := false

	// Bind error rules for failures that do not trigger provider failover.
	h.backend.BindErrors(c)

	// The subscription may be nil for balance-funded requests.
	subscription, _ := SubscriptionFromContext(c)

	userReleaseFunc, err := h.concurrency.AcquireUserSlotWithWait(c, subject.UserID, subject.Concurrency, reqStream, &streamStarted)
	if err != nil {
		reqLog.Warn("gateway.user_slot_acquire_failed", zap.Error(err))
		h.concurrencyError(c, err, "user", streamStarted)
		return
	}
	// Request exit and client cancellation can both release this user slot;
	// the wrapped callback makes those cleanup paths share one release operation.
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// Check funding after admission to the user slot so a balance or subscription
	// change during that wait can stop the request before provider execution.
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("gateway.billing_eligibility_check_failed", zap.Error(err))
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.streamingError(c, status, code, message, streamStarted)
		return
	}

	// Group features such as web search emulation read this ID; publish parsedReq
	// on Gin context so downstream native Messages execution clones in O(1) when
	// body bytes remain unchanged.
	parsedReq.GroupID = apiKey.GroupID
	c.Set("parsed_request", parsedReq)

	sessionStart := time.Now()
	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)

	reqLog.Info("sticky.session_hash_generated",
		zap.String("session_hash", sessionHash),
		zap.String("metadata_user_id_raw", parsedReq.MetadataUserID),
	)

	// Dedicated endpoints can force a platform; ordinary requests select across platforms.
	platform := ""
	if forcePlatform, ok := h.backend.ForcedPlatform(c); ok {
		platform = forcePlatform
	} else if apiKey.Group != nil {
		platform = ""
	}
	sessionKey := sessionHash
	// Separate forced Gemini sticky identities from the ordinary Messages cache
	// namespace while retaining the same underlying conversation hash for this route.
	if platform == capability.PlatformGemini && sessionHash != "" {
		sessionKey = "gemini:" + sessionHash
	}
	if isolationSessionID := MetadataSessionID(parsedReq.MetadataUserID); isolationSessionID != "" {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, subject.UserID, isolationSessionID); h.isolationError(c, err, streamStarted) {
			timing.ObserveSubstage(c.Request.Context(), "session_ms", time.Since(sessionStart))
			return
		}
	}

	var sessionBoundProviderID int64
	if sessionKey != "" {
		// Sticky lookup is advisory: discard its error and use the returned identity
		// to prefetch a bound provider when one is available for this request.
		sessionBoundProviderID, _ = h.backend.CachedSession(c.Request.Context(), apiKey.GroupID, sessionKey)
		reqLog.Info("sticky.cache_lookup",
			zap.String("session_key", sessionKey),
			zap.Int64("bound_provider_id", sessionBoundProviderID),
		)
		if sessionBoundProviderID > 0 {
			prefetchedGroupID := int64(0)
			if apiKey.GroupID != nil {
				prefetchedGroupID = *apiKey.GroupID
			}
			h.backend.Prefetch(c, sessionBoundProviderID, prefetchedGroupID)
		}
	} else {
		reqLog.Info("sticky.no_session_key", zap.String("session_hash", sessionHash))
	}
	timing.ObserveSubstage(c.Request.Context(), "session_ms", time.Since(sessionStart))
	hasBoundSession := sessionKey != "" && sessionBoundProviderID > 0

	call := MessagesCall{Key: apiKey, Subject: subject, Subscription: subscription, Parsed: parsedReq, Body: body, Model: reqModel, Stream: reqStream, ClaudeCode: isClaudeCodeClient, Platform: platform, SessionKey: sessionKey, BoundProviderID: sessionBoundProviderID, HasBoundSession: hasBoundSession, StreamStarted: &streamStarted, Log: reqLog, Route: groupMappingRoutePlan, Mapping: groupMapping}
	kind := execution.TextMessages
	// Forced Gemini forwards a separately prepared group-mapped representation;
	// retaining the client body and model lets execution record the original request.
	if platform == capability.PlatformGemini {
		attempt, err := h.backend.PrepareGemini(c.Request.Context(), call)
		if err != nil {
			reqLog.Warn("gateway.prepare_gemini_group_mapping_failed", zap.Error(err))
			h.streamingError(c, http.StatusBadRequest, "invalid_request_error", "Failed to apply group model mapping", streamStarted)
			return
		}
		call.GeminiBody = attempt.Body.Bytes()
		call.GeminiModel = attempt.Model
		kind = execution.TextGeminiMessages
	}
	request := execution.Request{
		Hints:   requeststate.ExecutionHintsFromContext(c.Request.Context()),
		Routing: requeststate.RoutingStateFromContext(c.Request.Context()),
		Route:   call.Route, UserID: subject.UserID, Concurrency: subject.Concurrency, Stream: reqStream, Body: body, Model: reqModel,
		Funding: execution.FundingState{Key: apiKey, Subscription: subscription}, SessionHash: sessionKey,
		Metadata: execution.RequestMetadata{ClaudeCode: isClaudeCodeClient},
		Text:     execution.TextState{Kind: kind, Parsed: parsedReq, Platform: platform, BoundProviderID: sessionBoundProviderID, HasBoundSession: hasBoundSession, GeminiBody: call.GeminiBody, GeminiModel: call.GeminiModel},
	}
	output := &MessagesOutput{ResponseSink: NewResponseSink(c), HTTP: c, Log: reqLog, StreamStarted: &streamStarted}
	// The bound executor is responsible for completion and terminal protocol
	// output. This admission adapter supplies active HTTP output and discards
	// the returned result and error after execution finishes.
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}

// errorResponse writes the supplied Anthropic JSON error through shared
// presentation. The caller supplies client status and category, while the
// shared writer applies any model-attribute error override before delivery.
func (h *MessagesHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}

// streamingError delegates protocol error output using the supplied
// stream-started flag. The shared writer chooses JSON or route-appropriate SSE
// and records stream error observation through the bound backend callback.
func (h *MessagesHandler) streamingError(c *gin.Context, status int, kind, message string, started bool) {
	WriteAnthropicStreamError(c, status, kind, "", message, started, h.backend.MarkStream)
}

// concurrencyError maps a slot-acquisition failure and delegates
// Anthropic-compatible error output. It retains the slot-specific code and
// stream-started state so queue/admission errors use the client's current
// response format.
func (h *MessagesHandler) concurrencyError(c *gin.Context, err error, slot string, started bool) {
	status, kind, code, message := ConcurrencyErrorResponse(err, slot)
	WriteAnthropicStreamError(c, status, kind, code, message, started, h.backend.MarkStream)
}

// isolationError reports whether a supplied isolation failure was presented.
// Nil permits admission, a known session conflict writes 403 and other
// failures write 503, using current streaming state for output.
func (h *MessagesHandler) isolationError(c *gin.Context, err error, started bool) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		h.streamingError(c, http.StatusForbidden, "permission_error", session.SessionIsolationConflictMessage, started)
	} else {
		h.streamingError(c, http.StatusServiceUnavailable, "api_error", "Service temporarily unavailable", started)
	}
	return true
}

// checkClientVersion permits non-Claude clients, count_tokens paths and absent
// configured bounds. Otherwise the stored version is checked against current
// bounds; rejection writes a 400 error and returns false before further
// admission.
func (h *MessagesHandler) checkClientVersion(c *gin.Context, detected ClientDetection) bool {
	if !detected.ClaudeCode || strings.HasSuffix(c.Request.URL.Path, "/count_tokens") {
		return true
	}
	min, max := h.backend.ClientVersionBounds(c.Request.Context())
	if min == "" && max == "" {
		return true
	}
	message := clientmeta.ClaudeVersionRejection(h.backend.ClientVersion(c), min, max)
	if message == "" {
		return true
	}
	h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", message)
	return false
}

// SubscriptionFromContext returns the stored subscription pointer and whether
// its type matches. Missing or differently typed values return false; a stored
// typed-nil pointer returns nil with true, allowing callers to retain optional
// funding.
func SubscriptionFromContext(c *gin.Context) (*billing.UserSubscription, bool) {
	value, exists := c.Get("subscription")
	if !exists {
		return nil, false
	}
	sub, ok := value.(*billing.UserSubscription)
	return sub, ok
}

// MetadataSessionID returns the trimmed session identity from recognized
// Anthropic metadata text. Empty, malformed or unrecognized metadata returns
// empty, so the handler can skip explicit-session ownership checks for that
// input.
func MetadataSessionID(raw string) string {
	parsed := anthropic.ParseMetadataUserID(raw)
	// Unrecognized metadata supplies no explicit session identity, leaving this
	// handler to rely on ordinary sticky hashing without an ownership check.
	if parsed == nil {
		return ""
	}
	return strings.TrimSpace(parsed.SessionID)
}

// ModerationHTTPStatus returns the decision's HTTP error status when it is
// between 400 and 599. Nil or out-of-range status uses 403; callers separately
// decide whether the decision blocks the request.
func ModerationHTTPStatus(decision *moderation.Decision) int {
	// Accept only HTTP error statuses from moderation decisions.
	if decision == nil || decision.StatusCode < 400 || decision.StatusCode > 599 {
		return http.StatusForbidden
	}
	return decision.StatusCode
}
