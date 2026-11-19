package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"mime/multipart"
	"net/http"
	"net/url"
	"strings"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// live.go creates Live sessions from HTTP offers and authorizes their sideband
// WebSocket connections.
// Live creation accepts an SDP offer and a JSON session object, then applies
// Key model redirects before moderation and funding. An enabled group and an
// immediate ordinary user slot are required before upstream creation; the
// Live runtime subsequently holds its separate session lease beyond the HTTP
// response.
//
// Creation retains the client model trace and snapshots model-mapping rules
// for later control messages. Sideband lookup checks saved call, Key,
// authenticated funding subject and group identity before the HTTP layer
// accepts a control WebSocket. The HTTP owner closes that client transport
// after proxying, while the Live runtime handles upstream controller
// ownership, observers and session expiry. SDP and JSON parsing preserve
// session content until the deliberate model-rewrite stage.

// LiveAPIKey carries authenticated Key and group data consumed by Live HTTP
// admission.
type LiveAPIKey struct {
	// ID identifies the authenticated credential; UserID identifies its owner.
	// The separate LiveSubject supplies the authenticated funding subject used
	// for user admission, session lease identity and saved-call lookup.
	ID     int64 // authenticated API Key identifier
	UserID int64 // API Key owner identifier
	// TeamID and GroupID retain optional pointers from the authenticated Key view.
	// Creator reads them into scalar call-record IDs, using zero for absence.
	TeamID  *int64 // optional creator team identifier
	GroupID *int64 // optional selected group identifier
	// Group supplies Live feature permission; absence denies entry even when a
	// group identifier exists. LivePorts copies the group's flag into this view.
	Group *LiveGroup
	// ModelMapping is cloned by LivePorts and again for creation identity. Saved
	// calls therefore use their captured redirect entries for later control messages.
	ModelMapping map[string]string
}

// LiveGroup supplies the current group feature flag for Live admission.
type LiveGroup struct {
	AllowLive bool // group permits Live sessions
}

// LiveSubject identifies the authenticated funding subject and user
// concurrency allowance.
type LiveSubject struct {
	UserID      int64 // authenticated funding subject identifier
	Concurrency int   // allowed concurrent user requests
}

// LiveSubscription identifies optional funding retained in the created Live
// call.
type LiveSubscription struct {
	ID int64 // selected funding subscription identifier
}

// LiveHTTPPorts binds authentication, content checks, funding and Live
// session execution for HTTP.
type LiveHTTPPorts interface {
	// APIKey returns authenticated Key facts and their presence flag for Live
	// admission. Success requires a usable view with group policy and redirect
	// rules; missing authentication stops HTTP entry before protocol parsing.
	APIKey(*gin.Context) (*LiveAPIKey, bool)
	// Subject returns the authenticated funding subject and user concurrency
	// allowance. A false presence flag tells HTTP entry to present its
	// missing-user-context error before funding or any session resource
	// acquisition.
	Subject(*gin.Context) (LiveSubject, bool)
	// Subscription returns optional funding identity and its context presence
	// flag. Creation retains a nonnil subscription ID in saved identity; funding
	// eligibility is checked separately against the complete authenticated
	// request.
	Subscription(*gin.Context) (*LiveSubscription, bool)
	// Redirect applies a one-hop Key model mapping and returns its trace-bearing
	// context with the rewritten name. HTTP entry installs that context before
	// moderation and passes the source model separately for diagnostics.
	Redirect(context.Context, *LiveAPIKey, string) (context.Context, string)
	// Moderate records Live request metadata and checks rewritten session
	// content. False means a blocking error was presented, so creation stops
	// before funding and user admission; true permits the remaining HTTP checks.
	Moderate(*gin.Context, *LiveAPIKey, LiveSubject, string, string, []byte) bool
	// CheckBilling checks authenticated funding and presents any classified
	// error with retry metadata. False stops creation before a user slot is
	// reserved, while true permits immediate Live user admission.
	CheckBilling(*gin.Context) bool
	// TryAcquireUserSlot attempts ordinary user admission immediately and
	// reports acquisition or error. A successful result supplies usable cleanup
	// released when HTTP creation returns; the Live session later uses a
	// separate persistent lease.
	TryAcquireUserSlot(context.Context, int64, int) (func(), bool, error)
	// InboundEndpoint returns the normalized client entry used in saved Live
	// metadata.
	InboundEndpoint(*gin.Context) string
	// Create opens and stores a Live session using the supplied request and
	// authenticated identity. The caller holds an ordinary user slot during
	// creation; successful session ownership transfers to a separate Live lease.
	Create(context.Context, *session.LiveCallRequest, session.LiveCallIdentity, int) (*gatewaylive.Created, error)
	// Lookup checks saved call identity before allowing its client control
	// upgrade. Its error distinguishes mismatched Key, funding subject or group
	// from closed or missing call state, which HTTP presents separately.
	Lookup(context.Context, string, session.LiveCallIdentity) (*session.LiveCallRecord, error)
	// Proxy relays control for the looked-up record through the accepted client
	// WebSocket. HTTP retains client transport cleanup, while Live execution
	// claims controller ownership and manages upstream connection, observers and
	// session expiry.
	Proxy(context.Context, *session.LiveCallRecord, *coderws.Conn) error
	// Error presents the supplied status and classification in a Live OpenAI
	// error response.
	Error(*gin.Context, int, string, string)
	// PolicyDenied marks request diagnostics for provider-level Live client
	// policy rejection.
	PolicyDenied(*gin.Context)
	// UpstreamStatus extracts a classified upstream HTTP status or returns zero
	// when absent. Error presentation treats 4xx as a sanitized client rejection
	// and other cases as generic upstream failure.
	UpstreamStatus(error) int
}

// LiveHandler owns Live request admission, protocol parsing and client
// control transport cleanup.
type LiveHandler struct {
	RequestLifetime               // application active-request barrier binding
	ports           LiveHTTPPorts // required Live HTTP operations
}

// NewLiveHandler binds the HTTP operations used for Live creation and
// sideband control.
func NewLiveHandler(ports LiveHTTPPorts) *LiveHandler { return &LiveHandler{ports: ports} }

// Live admits a session creation request and writes its SDP answer with a
// sideband Location. Authentication and group feature checks precede parsing.
// Key model redirects and additional tool-model redirects are applied before
// moderation, and funding succeeds before immediate user-slot acquisition.
//
// The ordinary user slot is released when this HTTP call returns. Create
// receives captured creator identity, subscription and mapping rules,
// allowing its separate Live lease to account for the session afterward. The
// public Location follows the matched route family, while upstream SDP
// remains the returned answer. Creation failures are translated through
// WriteLiveCreateError before any success response.
func (h *LiveHandler) Live(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.ports.APIKey(c)
	if !ok {
		h.ports.Error(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := h.ports.Subject(c)
	if !ok {
		h.ports.Error(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	if !liveEnabledForAPIKey(apiKey) {
		h.ports.Error(c, http.StatusForbidden, "permission_error", "Live is not enabled for this group")
		return
	}
	request, err := ParseLiveCallRequest(c)
	if err != nil {
		h.ports.Error(c, http.StatusBadRequest, "invalid_request_error", err.Error())
		return
	}
	// Retain the client model before main and tool-model rewrites, so request
	// diagnostics and the saved trace still identify the caller's session choice.
	clientModel := strings.TrimSpace(gjson.GetBytes(request.Session, "model").String())
	redirectCtx, model := h.ports.Redirect(c.Request.Context(), apiKey, clientModel)
	if model != clientModel {
		request.Session, err = sjson.SetBytes(request.Session, "model", model)
		if err != nil {
			h.ports.Error(c, http.StatusBadRequest, "invalid_request_error", "session.model is invalid")
			return
		}
	}
	request.Session, err = modeltrace.RewriteAPIKeyAdditionalModels(request.Session, apiKey.ModelMapping)
	if err != nil {
		h.ports.Error(c, http.StatusBadRequest, "invalid_request_error", "session.tools contains an invalid model")
		return
	}
	c.Request = c.Request.WithContext(redirectCtx)
	if !h.ports.Moderate(c, apiKey, subject, clientModel, model, request.Session) {
		return
	}

	subscription, _ := h.ports.Subscription(c)
	if !h.ports.CheckBilling(c) {
		return
	}

	userRelease, acquired, err := h.ports.TryAcquireUserSlot(
		c.Request.Context(),
		subject.UserID,
		subject.Concurrency,
	)
	if err != nil {
		h.ports.Error(c, http.StatusServiceUnavailable, "api_error", "Live concurrency unavailable")
		return
	}
	if !acquired {
		h.ports.Error(c, http.StatusTooManyRequests, "rate_limit_error", "Live concurrency limit reached")
		return
	}
	defer userRelease()

	identity := h.liveCallIdentity(c, apiKey, subject.UserID, subscription)
	created, err := h.ports.Create(c.Request.Context(), request, identity, subject.Concurrency)
	if err != nil {
		h.WriteLiveCreateError(c, err)
		return
	}
	c.Header("Location", LiveSidebandLocation(c.FullPath(), created.CallID))
	c.Data(http.StatusOK, "application/sdp", created.SDP)
}

// ParseLiveCallRequest reads multipart sdp/session fields or one JSON
// creation object and validates the shared Live shape. Callers supply a
// usable request and any body restriction. SDP must be nonblank, session must
// be a valid nonnull JSON object, and neither outer fields nor nested objects
// may contain duplicate or case-ambiguous protocol keys.
//
// Multipart retains the supplied field strings, while JSON decoding retains
// the session RawMessage until later rewriting. A second JSON value is
// rejected by requiring one complete JSON object. Validation checks the
// values without trimming stored SDP or assigning model defaults, so
// unsupported model-specific settings remain available to the execution
// stage.
func ParseLiveCallRequest(c *gin.Context) (*session.LiveCallRequest, error) {
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		sdp := c.PostForm("sdp")
		sessionBody := json.RawMessage(c.PostForm("session"))
		if err := validateLiveMultipartForm(c.Request.MultipartForm); err != nil {
			return nil, err
		}
		request := &session.LiveCallRequest{SDP: sdp, Session: sessionBody}
		if err := wire.ValidateLiveCallRequest(request); err != nil {
			return nil, err
		}
		return request, nil
	}
	if c.Request == nil || c.Request.Body == nil {
		return nil, errors.New("request body must be valid JSON")
	}
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		return nil, errors.New("request body must be valid JSON")
	}
	if err := wirejson.ValidateRequestObject(rawBody); err != nil {
		if fieldErr, ok := errors.AsType[*wirejson.RequestFieldError](err); ok {
			return nil, fieldErr
		}
		return nil, errors.New("request body must be a valid JSON object without duplicate fields")
	}
	var request session.LiveCallRequest
	if err := json.Unmarshal(rawBody, &request); err != nil {
		return nil, errors.New("request body must be valid JSON")
	}
	if err := wire.ValidateLiveCallRequest(&request); err != nil {
		return nil, err
	}
	return &request, nil
}

// validateLiveMultipartForm rejects duplicate or case-variant sdp and session
// multipart fields so Live creation cannot select one part while dropping another.
// Nil forms return nil, and file uploads using either reserved field name are rejected.
func validateLiveMultipartForm(form *multipart.Form) error {
	if form == nil {
		return nil
	}
	for key, values := range form.Value {
		folded := strings.ToLower(strings.TrimSpace(key))
		if folded != "sdp" && folded != "session" {
			continue
		}
		if key != folded {
			return errors.New("ambiguous multipart field name")
		}
		if len(values) > 1 {
			return errors.New("duplicate multipart field")
		}
	}
	for key, files := range form.File {
		folded := strings.ToLower(strings.TrimSpace(key))
		if (folded == "sdp" || folded == "session") && len(files) > 0 {
			return errors.New("sdp and session must be text fields")
		}
	}
	return nil
}

// LiveSidebandLocation builds an escaped call route matching the creation
// route family. Codex creation paths use /backend-api/codex/, while other
// matched paths use /v1/live/; the call ID is encoded as one segment.
func LiveSidebandLocation(fullPath, callID string) string {
	prefix := "/v1/live/"
	if strings.HasPrefix(fullPath, "/backend-api/codex/") {
		prefix = "/backend-api/codex/"
	}
	// Publish a TokenRouter control route using the matched creation family and
	// one escaped call segment, so clients can reconnect through their public entry.
	return prefix + url.PathEscape(callID)
}

// liveCallIdentity captures creator and funding identity with inbound request
// metadata. Key owner and authenticated subject remain distinct, and a
// supplied subscription ID is copied as a scalar pointer. Team and group ID
// pointers retain the Key view's storage.
//
// ModelMapping receives a separate map copy before being passed to the
// Creator, which copies it into the saved call record. This keeps later
// sideband model redirects on the creation-time rules. Client user-agent,
// originator, resolved IP and inbound endpoint are read before the HTTP
// request returns, supporting session control and zero-cost usage
// observations after creation.
func (h *LiveHandler) liveCallIdentity(
	c *gin.Context,
	apiKey *LiveAPIKey,
	userID int64,
	subscription *LiveSubscription,
) session.LiveCallIdentity {
	var subscriptionID *int64
	if subscription != nil {
		value := subscription.ID
		subscriptionID = &value
	}
	return session.LiveCallIdentity{
		APIKeyID:        apiKey.ID,
		ActorUserID:     apiKey.UserID,
		UserID:          userID,
		TeamID:          apiKey.TeamID,
		GroupID:         apiKey.GroupID,
		SubscriptionID:  subscriptionID,
		UserAgent:       c.GetHeader("User-Agent"),
		Originator:      c.GetHeader("originator"),
		IPAddress:       clientip.GetClientIP(c),
		InboundEndpoint: h.ports.InboundEndpoint(c),
		ModelMapping:    cloneLiveModelMapping(apiKey.ModelMapping),
	}
}

// WriteLiveCreateError translates session admission, client policy and
// upstream creation failures into OpenAI HTTP errors. Attestation failures
// retain their supported reason; upstream 4xx statuses receive a generic
// rejection message, and remaining failures use the generic upstream error.
func (h *LiveHandler) WriteLiveCreateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, session.ErrLiveConcurrencyFull):
		h.ports.Error(c, http.StatusTooManyRequests, "rate_limit_error", "Live concurrency limit reached")
	case errors.Is(err, session.ErrLiveClientPolicyDenied):
		h.ports.PolicyDenied(c)
		h.ports.Error(c, http.StatusForbidden, "permission_error", "Live client is not allowed by the available provider policy")
	case errors.Is(err, session.ErrLiveUnavailable):
		h.ports.Error(c, http.StatusServiceUnavailable, "api_error", "Live is unavailable")
	default:
		if attestationErr, ok := errors.AsType[*session.LiveAttestationUnavailableError](err); ok {
			h.ports.Error(c, http.StatusServiceUnavailable, "api_error", attestationErr.Error())
			return
		}
		// Keep classified upstream client statuses for caller handling while the fixed
		// rejection message keeps provider response details out of this error envelope.
		if status := h.ports.UpstreamStatus(err); status >= 400 && status < 500 {
			h.ports.Error(c, status, "invalid_request_error", "Live upstream rejected the request")
			return
		}

		h.ports.Error(c, http.StatusBadGateway, "api_error", "Live upstream request failed")
	}
}

// LiveSideband authenticates a caller and proxies a saved call's control
// WebSocket. Group feature permission and saved call identity are checked
// before upgrade. Lookup distinguishes identity mismatch from absent or
// unusable call state, producing permission or not-found responses.
//
// The HTTP layer owns the accepted client transport and closes it on every
// proxy exit. Origin verification is disabled at upgrade; saved-call identity
// checking supplies the preceding application authorization. Proxy errors
// request an internal close, and successful return requests a normal close
// before immediate transport cleanup. Upstream controller and observer
// cleanup remain with Live execution.
func (h *LiveHandler) LiveSideband(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	apiKey, ok := h.ports.APIKey(c)
	if !ok {
		h.ports.Error(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := h.ports.Subject(c)
	if !ok {
		h.ports.Error(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}
	if !liveEnabledForAPIKey(apiKey) {
		h.ports.Error(c, http.StatusForbidden, "permission_error", "Live is not enabled for this group")
		return
	}
	identity := session.LiveCallIdentity{
		APIKeyID:    apiKey.ID,
		ActorUserID: apiKey.UserID,
		UserID:      subject.UserID,
		TeamID:      apiKey.TeamID,
		GroupID:     apiKey.GroupID,
	}
	record, err := h.ports.Lookup(c.Request.Context(), c.Param("call_id"), identity)
	if err != nil {
		if errors.Is(err, session.ErrLiveIdentityMismatch) {
			h.ports.Error(c, http.StatusForbidden, "permission_error", "Live call belongs to another identity")
			return
		}
		h.ports.Error(c, http.StatusNotFound, "not_found_error", "Live call not found")
		return
	}
	// Saved-call lookup enforces Key, funding subject and group identity before
	// this upgrade, whose Origin check is disabled for the authenticated control connection.
	downstream, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	// Immediate transport cleanup follows any requested close frame and also
	// covers proxy failures, while the Live runtime owns the upstream controller connection.
	defer func() { _ = downstream.CloseNow() }()
	if err := h.ports.Proxy(c.Request.Context(), record, downstream); err != nil {
		_ = downstream.Close(coderws.StatusInternalError, "live sideband closed")
		return
	}
	_ = downstream.Close(coderws.StatusNormalClosure, "")
}

// liveEnabledForAPIKey reports whether a usable Key has an enabled Live
// group. Missing Key or group returns false, so HTTP feature denial happens
// before reading creation input or accepting sideband control.
func liveEnabledForAPIKey(apiKey *LiveAPIKey) bool {
	return apiKey != nil &&
		apiKey.Group != nil &&
		apiKey.Group.AllowLive
}

// cloneLiveModelMapping copies redirect entries into a newly allocated map.
// Nil and empty input produce a present empty map, and string keys and values
// allow independent later entry replacement by the session owner.
func cloneLiveModelMapping(mapping map[string]string) map[string]string {
	result := make(map[string]string, len(mapping))
	maps.Copy(result, mapping)
	return result
}
