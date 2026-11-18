package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/execution"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	protocolgemini "github.com/TokenFlux/TokenRouter/internal/protocol/gemini"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// gemini_native.go prepares native Gemini model actions, resolves session
// affinity, and invokes selected text execution.
// Native Gemini requests carry their model in the URL while conversation
// identity comes from request content and client metadata. Prompt replacement
// precedes moderation, digest matching and forwarding so those consumers
// examine the same body. Group mapping resolves the execution model, and the
// original URL model remains available for response and billing state.
//
// An explicit CLI directory digest is checked for session ownership before
// scheduling. A cached provider association bypasses conversation-chain
// matching; otherwise a matching digest chain can recover a prior session and
// seed sticky selection. User concurrency is acquired before the final funding
// check, and native requests disable Messages heartbeat output so waiting does
// not inject another protocol's frames.

// GeminiCLITmpDirRegex extracts the first sixty-four hexadecimal characters
// after a Gemini CLI temporary-directory prefix for request session identity.
var GeminiCLITmpDirRegex = regexp.MustCompile(`/\.gemini/tmp/([A-Fa-f0-9]{64})`)

// GeminiNativeOptions retains native HTTP configuration supplied during
// application assembly.
type GeminiNativeOptions struct {
	// MaxSwitches records the configured Gemini switch count. The HTTP handler
	// retains it while the supplied execution.Executor owns provider attempts.
	MaxSwitches int
}

// GeminiNativeBackend supplies admission and session operations for native
// Gemini requests.
type GeminiNativeBackend interface {
	// Access returns the effective API key and whether authentication supplied
	// it. Native admission rejects a false result or nil key before reading
	// caller or group state.
	Access(*gin.Context) (*apikey.APIKey, bool)
	// HasForcedPlatform reports whether request authentication installed a forced
	// upstream platform.
	HasForcedPlatform(*gin.Context) bool
	// SafeModelSegment reports whether a model can safely occupy one URL segment.
	SafeModelSegment(string) bool
	// ObserveRequest records the client model and stream intent for request
	// diagnostics.
	ObserveRequest(*gin.Context, string, bool)
	// ObserveEndpoint records the request endpoint and its streaming execution
	// category.
	ObserveEndpoint(*gin.Context, bool)
	// Moderate checks the replaced Gemini body using the authenticated caller and
	// key. Nil or an unblocked decision permits admission; a blocked decision
	// supplies the client rejection message and status.
	Moderate(*gin.Context, *zap.Logger, *apikey.APIKey, authctx.AuthSubject, string, []byte) *moderation.Decision
	// Plan resolves the effective group and model mapping for this request.
	Plan(context.Context, *apikey.APIKey, string) routing.RoutePlan
	// BindPlan installs the resolved routing plan in the active request context.
	// Call it after group/model planning so execution receives the same mapping
	// that admission retained for completion.
	BindPlan(*gin.Context, routing.RoutePlan)
	// BindErrors makes final upstream error rules available to this HTTP request.
	// Call it before waiting and execution so their final output can apply the
	// configured service or use default messages when unbound.
	BindErrors(*gin.Context)
	// Eligibility rechecks the key's funding after user concurrency admission has
	// waited. The optional subscription identifies subscription-funded requests,
	// and a returned error stops execution with billing-specific status and retry
	// guidance.
	Eligibility(context.Context, *apikey.APIKey, *billing.UserSubscription) error
	// Isolate checks ownership of an explicit Gemini CLI session identity. A
	// conflict rejects admission, while other errors become temporary
	// unavailability; the handler invokes this before upstream provider
	// selection.
	Isolate(context.Context, *apikey.APIKey, int64, string) error
	// CachedSession returns a sticky provider association for the group and
	// session key. Native admission discards its lookup error; the returned
	// provider identity alone determines whether digest recovery can run.
	CachedSession(context.Context, *int64, string) (int64, error)
	// Prefetch installs provider and group hints for subsequent execution
	// selection. The pair describes this request's sticky association and is
	// installed before the executor receives its request-state snapshot.
	Prefetch(*gin.Context, int64, int64)
	// DigestChain derives ordered content digests from the parsed Gemini
	// conversation.
	DigestChain(*protocolgemini.GeminiRequest) string
	// PrefixHash scopes conversation matching by caller, key, client metadata and
	// model.
	PrefixHash(int64, int64, string, string, string, string) string
	// FindSession searches group-scoped conversation history using the prefix and
	// digest chain. When found is true, the handler uses the returned UUID,
	// provider and matched chain to recover sticky execution.
	FindSession(context.Context, int64, string, string) (string, int64, string, bool)
	// DigestSessionKey forms a sticky identity from a prefix and session UUID.
	DigestSessionKey(string, string) string
	// BindSticky saves the selected provider against the group and session key.
	// Native admission uses the result as a best-effort association; a failed
	// write does not terminate this request.
	BindSticky(context.Context, *int64, string, int64) error
	// FailoverObservation records request-level provider-switch diagnostics using
	// the supplied context and values.
	FailoverObservation(context.Context, string, map[string]any)
}

// GeminiNativeHandler prepares native HTTP identity and admission before
// delegated execution.
type GeminiNativeHandler struct {
	executor        execution.Executor // selected request execution
	RequestLifetime                    // application request barrier

	options     GeminiNativeOptions // retained HTTP configuration
	backend     GeminiNativeBackend // admission and session services
	prompt      MessagesPrompt      // request prompt replacement
	concurrency *ConcurrencyHelper  // shared concurrency service
	newID       func() string       // new digest-session UUID
}

// GeminiPathParseError carries the client-visible native model-action path
// failure.
type GeminiPathParseError struct {
	message string // client path error
}

// NewGeminiNativeHandler retains the supplied HTTP dependencies for native
// requests. Callers bind usable admission, prompt, concurrency and execution
// services; newID supplies a UUID when digest recovery creates a session.
func NewGeminiNativeHandler(options GeminiNativeOptions, backend GeminiNativeBackend, prompt MessagesPrompt, concurrency *ConcurrencyHelper, newID func() string, executor execution.Executor) *GeminiNativeHandler {
	return &GeminiNativeHandler{executor: executor, options: options, backend: backend, prompt: prompt, concurrency: concurrency, newID: newID}
}

// WriteGoogleError writes a Google JSON error with the selected HTTP status.
// Model-attribute failure state can replace status and message before the
// shared HTTP-to-Google status mapping chooses the error name.
func WriteGoogleError(c *gin.Context, status int, message string) {
	status, message = modelAttributeError(c, status, message)
	body, _ := json.Marshal(gin.H{"error": gin.H{"code": status, "message": message, "status": HTTPStatusToGoogleStatus(status)}})
	writeGoogleErrorBody(c, status, body)
}

// geminiNativeIsolationError reports whether an isolation error was presented
// to the client. Nil returns false; a session conflict writes 403 and other
// errors write 503 using the native Google envelope.
func geminiNativeIsolationError(c *gin.Context, err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, session.ErrSessionIsolationConflict) {
		WriteGoogleError(c, 403, session.SessionIsolationConflictMessage)
	} else {
		WriteGoogleError(c, 503, "Service temporarily unavailable")
	}
	return true
}

// Error returns the client-facing message stored in this path parse error.
func (e *GeminiPathParseError) Error() string { return e.message }

// GeminiV1BetaModels prepares the URL model action and invokes native Gemini
// execution after authentication, group permission, prompt replacement and
// moderation. Callers supply authenticated key and subject state through
// middleware. The user slot is released on completion or client cancellation,
// and funding is checked again after waiting.
//
// CLI identity is checked for ownership before provider execution. A cached
// association supplies the provider hint; when absent, digest matching can
// recover a conversation and bind its provider. The execution request keeps
// the original model beside its group-mapped Gemini model and signature state
// so forwarding and completion can interpret each identity at its owning
// layer.
// @project-doc docs/interfaces/gemini_upstream.md#gemini_protocol_dispatch
func (h *GeminiNativeHandler) GeminiV1BetaModels(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "google")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.backend.Access(c)
	if !ok || apiKey == nil {
		WriteGoogleError(c, http.StatusUnauthorized, "Invalid API key")
		return
	}
	authSubject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		WriteGoogleError(c, http.StatusInternalServerError, "User context not found")
		return
	}
	reqLog := RequestLogger(
		c,
		"handler.gemini_v1beta.models",
		zap.Int64("user_id", authSubject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
	)

	// Admission checks the group protocol; provider selection determines the upstream platform.
	if apiKey.Group == nil || !apiKey.Group.AllowsClientProtocol(protocol.ProtocolGeminiGenerateContent) {
		WriteGoogleError(c, http.StatusForbidden, "This group does not allow Gemini GenerateContent requests")
		return
	}

	modelName, action, err := ParseGeminiModelAction(strings.TrimPrefix(c.Param("modelAction"), "/"))
	if err != nil {
		WriteGoogleError(c, http.StatusNotFound, err.Error())
		return
	}
	// The model becomes part of /v1beta/models/{model}:{action}, so validate it as a
	// URL path segment before forwarding.
	if !h.backend.SafeModelSegment(modelName) {
		WriteGoogleError(c, http.StatusBadRequest, "Invalid model in URL")
		return
	}

	stream := action == "streamGenerateContent"
	reqLog = reqLog.With(zap.String("model", modelName), zap.String("action", action), zap.Bool("stream", stream))

	body, err := ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if maxErr, ok := errors.AsType[*http.MaxBytesError](err); ok {
			WriteGoogleError(c, http.StatusRequestEntityTooLarge, BodyTooLargeMessage(maxErr.Limit))
			return
		}
		WriteGoogleError(c, http.StatusBadRequest, "Failed to read request body")
		return
	}
	if len(body) == 0 {
		WriteGoogleError(c, http.StatusBadRequest, "Request body is empty")
		return
	}

	apiKey, err = resolveClientGroupForRequest(c, h.backend, apiKey, protocol.ProtocolGeminiGenerateContent)
	if err != nil {
		writeClientGroupFallbackError(c, err, func(c *gin.Context, status int, _, message string) { WriteGoogleError(c, status, message) })
		return
	}
	h.backend.ObserveRequest(c, modelName, stream)
	h.backend.ObserveEndpoint(c, stream)
	// Apply prompt replacement before moderation, session hashing, and forwarding so they use the same body.
	body = h.prompt.ApplyUserPromptReplacementToBody(c.Request.Context(), body, "gemini")

	if decision := h.backend.Moderate(c, reqLog, apiKey, authSubject, modelName, body); decision != nil && decision.Blocked {
		WriteGoogleError(c, ModerationHTTPStatus(decision), decision.Message)
		return
	}

	// Retain the URL model beside the group mapping, so execution can use the
	// mapped Gemini target while completion still identifies the client's requested model.
	groupMappingRoutePlan := h.backend.Plan(c.Request.Context(), apiKey, modelName)
	groupMapping := groupMappingRoutePlan.Mapping()
	h.backend.BindPlan(c, groupMappingRoutePlan)
	reqModel := modelName // Client model before group mapping.
	if groupMapping.Mapped {
		modelName = groupMapping.MappedModel
	}

	subscription, _ := SubscriptionFromContext(c)

	// Native Google SDKs reject comment lines as JSON. Empty SSE events keep
	// admission waits active without inventing model output or closing retry eligibility.
	geminiConcurrency := NewConcurrencyHelper(h.concurrency.Service(), SSEPingFormatGemini, 0)

	streamStarted := false
	h.backend.BindErrors(c)
	userReleaseFunc, err := geminiConcurrency.AcquireUserSlotWithWait(c, authSubject.UserID, authSubject.Concurrency, stream, &streamStarted)
	if err != nil {
		reqLog.Warn("gemini.user_slot_acquire_failed", zap.Error(err))
		WriteGoogleError(c, http.StatusTooManyRequests, err.Error())
		return
	}
	// Return this user slot on either cancellation or request exit. The wrapped
	// callback allows disconnect cleanup and the deferred path to race safely.
	userReleaseFunc = scheduler.WrapRelease(c.Request.Context(), scheduler.ReleaseOnCancel, userReleaseFunc)
	if userReleaseFunc != nil {
		defer userReleaseFunc()
	}

	// Recheck funding eligibility after waiting for the user slot.
	if err := h.backend.Eligibility(c.Request.Context(), apiKey, subscription); err != nil {
		reqLog.Info("gemini.billing_eligibility_check_failed", zap.Error(err))
		status, _, message, retryAfter := BillingErrorDetails(err)
		if retryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(retryAfter))
		}
		WriteGoogleError(c, status, message)
		return
	}

	// Prefer Gemini CLI identity from the privileged user ID and temporary-directory hash.
	sessionHash := ExtractGeminiCLISessionHash(c, body)
	explicitGeminiCLISession := sessionHash != ""
	if sessionHash == "" {
		// Without a CLI directory identity, shared detection uses parsed content and
		// caller metadata so ordinary native clients can still obtain sticky routing.
		// Session parsing is advisory: invalid input can leave no content seed while
		// execution still receives the request body for its selected native action.
		parsedReq, _ := requeststate.ParseGatewayRequest(requeststate.NewRequestBodyRef(body), capability.PlatformGemini)
		if parsedReq != nil {
			parsedReq.SessionContext = &requeststate.SessionContext{
				ClientIP:  clientip.GetClientIP(c),
				UserAgent: c.GetHeader("User-Agent"),
				APIKeyID:  apiKey.ID,
			}
		}
		sessionHash = session.GenerateSessionHash(parsedReq, slog.Info)
	}
	sessionKey := sessionHash
	if sessionHash != "" {
		sessionKey = "gemini:" + sessionHash
	}
	if explicitGeminiCLISession {
		if err := h.backend.Isolate(c.Request.Context(), apiKey, authSubject.UserID, sessionKey); geminiNativeIsolationError(c, err) {
			return
		}
	}

	// A sticky lookup is advisory: its error is discarded and the returned identity
	// controls whether the following digest path searches for a conversation association.
	var sessionBoundProviderID int64
	if sessionKey != "" {
		sessionBoundProviderID, _ = h.backend.CachedSession(c.Request.Context(), apiKey.GroupID, sessionKey)
		if sessionBoundProviderID > 0 {
			prefetchedGroupID := int64(0)
			if apiKey.GroupID != nil {
				prefetchedGroupID = *apiKey.GroupID
			}
			h.backend.Prefetch(c, sessionBoundProviderID, prefetchedGroupID)
		}
	}

	// Digest recovery runs only after the sticky lookup found no provider. It
	// compares the mapped model and content chain within this caller's prefix.
	var geminiDigestChain string
	var geminiPrefixHash string
	var geminiSessionUUID string
	var matchedDigestChain string
	useDigestFallback := sessionBoundProviderID == 0

	if useDigestFallback {
		// Malformed JSON or an empty conversation skips digest recovery; forwarding
		// still uses the supplied body, so identity discovery does not replace payload validation.
		var geminiReq protocolgemini.GeminiRequest
		if err := json.Unmarshal(body, &geminiReq); err == nil && len(geminiReq.Contents) > 0 {
			geminiDigestChain = h.backend.DigestChain(&geminiReq)
			if geminiDigestChain != "" {
				userAgent := c.GetHeader("User-Agent")
				clientIP := clientip.GetClientIP(c)
				geminiPrefixHash = h.backend.PrefixHash(
					authSubject.UserID,
					apiKey.ID,
					clientIP,
					userAgent,
					"",
					modelName,
				)

				foundUUID, foundProviderID, foundMatchedChain, found := h.backend.FindSession(
					c.Request.Context(),
					geminiGroupID(apiKey.GroupID),
					geminiPrefixHash,
					geminiDigestChain,
				)
				if found {
					matchedDigestChain = foundMatchedChain
					sessionBoundProviderID = foundProviderID
					geminiSessionUUID = foundUUID
					reqLog.Info("gemini.digest_fallback_matched",
						zap.String("session_uuid_prefix", GeminiShortPrefix(foundUUID, 8)),
						zap.Int64("provider_id", foundProviderID),
						zap.String("digest_chain", TruncateGeminiDigestChain(geminiDigestChain)),
					)

					// Create a key from the prefix hash and matched UUID when no explicit key exists,
					// so provider selection can reuse the matched provider through the sticky binding.
					if sessionKey == "" {
						sessionKey = h.backend.DigestSessionKey(geminiPrefixHash, foundUUID)
					}
					// Recovery can proceed with its matched provider even if persisting the
					// advisory sticky association fails; the attempt already carries that identity.
					_ = h.backend.BindSticky(c.Request.Context(), apiKey.GroupID, sessionKey, foundProviderID)
				} else {
					geminiSessionUUID = h.newID()
					// A newly assigned UUID also seeds sticky selection, allowing later
					// digest-matched requests to continue with the provider chosen for this session.
					if sessionKey == "" {
						sessionKey = h.backend.DigestSessionKey(geminiPrefixHash, geminiSessionUUID)
					}
				}
			}
		}
	}

	hasBoundSession := sessionKey != "" && sessionBoundProviderID > 0

	request := execution.Request{
		Hints:       requeststate.ExecutionHintsFromContext(c.Request.Context()),
		Routing:     requeststate.RoutingStateFromContext(c.Request.Context()),
		Route:       groupMappingRoutePlan,
		UserID:      authSubject.UserID,
		Concurrency: authSubject.Concurrency,
		Stream:      stream,
		Body:        body,
		Model:       reqModel,
		Funding: execution.FundingState{
			Key:          apiKey,
			Subscription: subscription,
		},
		SessionHash: sessionKey,
		Text: execution.TextState{
			Kind:               execution.TextNativeGemini,
			BoundProviderID:    sessionBoundProviderID,
			HasBoundSession:    hasBoundSession,
			GeminiModel:        modelName,
			Action:             action,
			UseDigestFallback:  useDigestFallback,
			DigestChain:        geminiDigestChain,
			PrefixHash:         geminiPrefixHash,
			SessionUUID:        geminiSessionUUID,
			MatchedDigestChain: matchedDigestChain,
			SignatureState: requeststate.GeminiSignatureState{
				BoundProviderID: sessionBoundProviderID,
			},
			Mapping: groupMapping,
		},
	}
	output := &MessagesOutput{
		ResponseSink:  NewResponseSink(c),
		HTTP:          c,
		Log:           reqLog,
		StreamStarted: &streamStarted,
		Concurrency:   geminiConcurrency,
	}
	// The bound executor is responsible for terminal client output. This admission
	// adapter discards its result and error after supplying synchronous HTTP output.
	_, _ = h.executor.Execute(c.Request.Context(), request, output)
}

// ParseGeminiModelAction separates the model from its action after trimming
// the path. Colon syntax wins over the compatible slash form; missing or
// malformed separators return a path error, while action validity is left to
// downstream execution.
func ParseGeminiModelAction(rest string) (model string, action string, err error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", "", &GeminiPathParseError{"missing path"}
	}

	// Standard syntax: {model}:{action}.
	if i := strings.Index(rest, ":"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], nil
	}

	// Slash syntax accepts clients using a path action. Keep it after the colon
	// branch so an input containing both separators follows the native delimiter.
	if i := strings.Index(rest, "/"); i > 0 && i < len(rest)-1 {
		return rest[:i], rest[i+1:], nil
	}

	return "", "", &GeminiPathParseError{"invalid model action path"}
}

// ExtractGeminiCLISessionHash extracts a session seed from the first Gemini
// CLI temporary-directory pattern in the raw body. No match returns an empty
// string so admission can use shared content identity. A matched body requires
// a usable Gin context for the privileged-user header.
//
// When that trimmed header is present, SHA-256 combines it with the directory
// digest and returns hexadecimal text. Without it, the directory digest itself
// is returned. This distinguishes the same directory signal used by different
// privileged users while keeping clients that supply no such header eligible
// for native session detection.
func ExtractGeminiCLISessionHash(c *gin.Context, body []byte) string {
	match := GeminiCLITmpDirRegex.FindSubmatch(body)
	if len(match) < 2 {
		return "" // No CLI-specific session identity; the caller can use shared session detection.
	}
	tmpDirHash := string(match[1])

	privilegedUserID := strings.TrimSpace(c.GetHeader("x-gemini-api-privileged-user-id"))

	if privilegedUserID != "" {
		// Separate the user identity from the directory digest before hashing, so
		// another privileged user presenting that directory signal gets a different key.
		combined := privilegedUserID + ":" + tmpDirHash
		hash := sha256.Sum256([]byte(combined))
		return hex.EncodeToString(hash[:])
	}

	// Directory-only clients retain their existing seed; admission adds the
	// Gemini namespace and performs ownership isolation when that seed is explicit.
	return tmpDirHash
}

// TruncateGeminiDigestChain limits a diagnostic chain to its first fifty bytes
// plus an ellipsis. Shorter values pass through unchanged; the result is a log
// label and is not used for session matching.
func TruncateGeminiDigestChain(chain string) string {
	if len(chain) <= 50 {
		return chain
	}
	// Retain a bounded diagnostic prefix while lookup continues using the full chain.
	return chain[:50] + "..."
}

// GeminiShortPrefix returns at most n leading bytes for a diagnostic label.
// Nonpositive n or a short value returns the original string; truncation does
// not validate UTF-8 character boundaries.
func GeminiShortPrefix(value string, n int) string {
	if n <= 0 || len(value) <= n {
		return value
	}
	// This diagnostic limit counts bytes, so callers choose ASCII identity labels.
	return value[:n]
}

// geminiGroupID dereferences a group identity, using zero when no group is
// bound. Digest lookup receives that zero value for an ungrouped key rather
// than dereferencing a missing pointer.
func geminiGroupID(groupID *int64) int64 {
	if groupID == nil {
		return 0
	}
	return *groupID
}
