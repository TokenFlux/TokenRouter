package httpapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// count_tokens.go prepares Messages token-count requests and starts the
// bounded provider count loop.
// Messages counting performs request parsing and funding preflight before
// provider selection, while auxiliary execution owns its own count result and
// failure handling. Parsed client identity and group fallback are established
// before the route is built, so model mapping and observations use the selected
// request group even though this handler acquires no generation slot.

// CountTokensBackend provides admission, observation, and auxiliary count execution to HTTP preparation.
type CountTokensBackend interface {
	// Access returns the request key view and its authentication lookup flag.
	// A successful result supplies a usable key for logging and group preparation;
	// failed lookup ends the request before any body or funding work.
	Access(*gin.Context) (*apikey.APIKey, bool)
	// CompatibilityMetrics samples session-compatibility observations after count request handling finishes.
	CompatibilityMetrics(*zap.Logger)
	// ObserveRequest records the current model and stream fields for request diagnostics.
	ObserveRequest(*gin.Context, string, bool)
	// ObserveEndpoint records the parsed count request's streaming type for HTTP diagnostics.
	ObserveEndpoint(*gin.Context, bool)
	// BindClient publishes client recognition for group fallback and execution preparation.
	// Detection runs on the parsed Messages body. Implementations choose version
	// publication behavior, including whether an empty detection leaves earlier context data intact.
	BindClient(*gin.Context, ClientDetection)
	// BindThinking records the parsed thinking flag for later model resolution consumers.
	BindThinking(*gin.Context, bool)
	// Eligibility checks the key and selected subscription before count execution begins.
	// Failures supply the HTTP billing classification and optional retry header,
	// so the caller stops before building its provider-attempt state.
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	// Execution builds request-specific count attempts from parsed input and session identity.
	// Callers establish the selected group and funding first, then pass the
	// returned operations to RunCountTokens for selection, forwarding, and failure cleanup.
	Execution(*gin.Context, *apikey.APIKey, *requeststate.ParsedRequest, string, *zap.Logger) textflow.CountPorts
	// FailoverObservation records the count loop's event and supplied diagnostic values.
	FailoverObservation(context.Context, string, map[string]any)
}

// CountTokensHandler prepares Messages token-count requests for the bounded count loop.
type CountTokensHandler struct {
	// RequestLifetime optionally holds application activity across preparation and
	// the count loop; a bound shutdown barrier can reject before those phases begin.
	RequestLifetime

	maxBodyBytes int64              // normalized request bytes
	maxSwitches  int                // provider-switch budget input
	backend      CountTokensBackend // admission and count operations
	prompt       MessagesPrompt     // user prompt replacement
}

// NewCountTokensHandler binds the body limit, switch budget, and count preparation dependencies.
// It passes limits unchanged to body reading and RunCountTokens. Assembly provides
// usable backend and prompt operations and can bind an activity barrier before serving requests.
func NewCountTokensHandler(limit int64, switches int, backend CountTokensBackend, prompt MessagesPrompt) *CountTokensHandler {
	return &CountTokensHandler{maxBodyBytes: limit, maxSwitches: switches, backend: backend, prompt: prompt}
}

// errorResponse writes the local failure in the Messages count endpoint's error shape.
func (h *CountTokensHandler) errorResponse(c *gin.Context, status int, kind, message string) {
	WriteAnthropicError(c, status, kind, "", message)
}

// countMaxBytesError extracts an HTTP body-size error for count rejection messages.
// Its flag reports errors.As matching, including typed nil values. Body readers
// supply concrete size failures when their configured normalized limit is exceeded.
func countMaxBytesError(err error) (*http.MaxBytesError, bool) {
	var exceeded *http.MaxBytesError
	ok := errors.As(err, &exceeded)
	return exceeded, ok
}

// CountTokens handles POST /v1/messages/count_tokens with funding preflight and bounded attempts.
// Callers arrive with authenticated key and subject data. The handler replaces
// prompts, parses input, records client and thinking state, and checks the
// selected group's funding before starting auxiliary execution.
//
// Client-group fallback runs after client detection but before route planning.
// Session hashing uses the prepared body with request-source metadata, and the
// backend publishes the route when creating attempt operations. RunCountTokens
// rebuilds mapped input per attempt and owns failure-session release; this HTTP
// entry itself obtains no generation slot or usage submission.
func (h *CountTokensHandler) CountTokens(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "anthropic")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.errorResponse(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}

	_, ok = authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.errorResponse(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gateway.count_tokens",
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)
	defer h.backend.CompatibilityMetrics(reqLog)

	body, err := ReadLenientJSONRequestBodyWithPrealloc(c.Request, h.maxBodyBytes)
	if err != nil {
		if maxErr, ok := countMaxBytesError(err); ok {
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
	if err := wirejson.ValidateRequestObject(body); err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}

	// Apply prompt replacement before parsing so count forwarding and session
	// hashing receive the same prepared bytes, rather than mixing client and replaced input.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "anthropic_messages")

	bodyRef := requeststate.NewRequestBodyRef(body)
	parsedReq, err := requeststate.ParseGatewayRequest(bodyRef, capability.PlatformAnthropic)
	if err != nil {
		LogRequestBodyParseFailure(reqLog, body, err)
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "Failed to parse request body")
		return
	}
	body = parsedReq.Body.Bytes()
	// Counting can omit ordinary generation fields. Reuse the parsed body for
	// the validator's count-path rules before deciding whether client-group fallback is needed.
	h.backend.BindClient(c, DetectClaudeCodeRequest(c, body, parsedReq, false))
	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolAnthropicMessages)
	if err != nil {
		writeClientGroupFallbackError(c, err, h.errorResponse)
		return
	}

	reqLog = reqLog.With(zap.String("model", parsedReq.Model), zap.Bool("stream", parsedReq.Stream))
	// Save thinking state for Antigravity model resolution and model-level rate limits.
	h.backend.BindThinking(c, parsedReq.ThinkingEnabled)

	if parsedReq.Model == "" {
		h.errorResponse(c, http.StatusBadRequest, "invalid_request_error", "model is required")
		return
	}

	h.backend.ObserveRequest(c, parsedReq.Model, parsedReq.Stream)
	h.backend.ObserveEndpoint(c, parsedReq.Stream)

	subscription, _ := SubscriptionFromContext(c)

	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		status, code, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		h.errorResponse(c, status, code, message)
		return
	}

	parsedReq.SessionContext = &requeststate.SessionContext{
		ClientIP:  clientip.GetClientIP(c),
		UserAgent: c.GetHeader("User-Agent"),
		APIKeyID:  apiKey.ID,
	}
	sessionHash := session.GenerateSessionHash(parsedReq, slog.Info)

	textflow.RunCountTokens(h.backend.Execution(c, apiKey, parsedReq, sessionHash, reqLog), h.maxSwitches, h.backend.FailoverObservation)
}
