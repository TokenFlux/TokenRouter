package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
)

// media_auxiliary.go prepares Embeddings, AlphaSearch, Voice, and Realtime
// requests for their media execution loops.
// Embeddings and AlphaSearch reserve user capacity before their funding
// check, then delegate provider attempts through request-local adapters.
// AlphaSearch retains original input beside its mapped forwarding body, so
// completion hashes the client payload while selection and execution use the
// configured route. Voice endpoints perform funding admission before reading
// their optional method-dependent bodies.
//
// Grok Realtime opens an upstream lease before accepting the client
// WebSocket, retaining that connection and provider slot until relay
// finishes. The client transport is closed separately at HTTP exit. Observed
// audio and positive elapsed duration permit completion after a successful
// relay or one of the accepted close codes; another relay error closes with
// an internal failure and skips that completion path.

// AuxiliaryHTTPInput retains parsed identity and original input for auxiliary
// request adapters.
type AuxiliaryHTTPInput struct {
	Subject MediaSubject // authenticated user admission facts
	// Model: parsed request model. SessionHash: AlphaSearch selection hash.
	// Endpoint: selected Voice operation.
	Model, SessionHash, Endpoint string
	// Body retains original request bytes for AlphaSearch completion hashing.
	// The request adapter shares those bytes until capture, so callers keep them stable.
	Body    []byte
	Mapping routing.GroupMappingResult // group model mapping result
}

// RealtimeHTTPExecution supplies admitted frame relay and measured-audio
// completion for HTTP Realtime.
type RealtimeHTTPExecution interface {
	media.RealtimePorts // upstream admission and dial operations
	// Relay exchanges frames between client and admitted upstream connection,
	// returning whether audio was observed. The HTTP caller owns both connection
	// lifetimes and uses the returned error to decide completion eligibility.
	Relay(context.Context, upstream.FrameConn, upstream.FrameConn) (bool, error)
	// CompleteRealtime captures audio usage for a selected provider and measured
	// elapsed duration. HTTP invokes it only after observed audio and an
	// accepted relay outcome; request-local authentication and subscription
	// support completion.
	CompleteRealtime(context.Context, provider.ProviderSnapshot, string, time.Duration)
}

// AuxiliaryHTTPPorts extends media admission with auxiliary parsing and
// platform adapter factories.
type AuxiliaryHTTPPorts interface {
	MediaHTTPPorts // shared media HTTP admission operations
	// HTTPTransport records ordinary HTTP transport for AlphaSearch request
	// diagnostics.
	HTTPTransport(*gin.Context)
	// ParseFailure logs rejected JSON input through the shared redacted body
	// diagnostics.
	ParseFailure(*zap.Logger, []byte)
	// RewriteModel returns request bytes with the selected top-level model
	// replacement.
	RewriteModel([]byte, string) []byte
	// FallbackSession derives AlphaSearch selection identity from explicit hints
	// or its supplied ID. The HTTP caller trims the request ID before passing it,
	// while the implementation supplies the shared hash policy.
	FallbackSession(*gin.Context, string) string
	// NewEmbeddings creates request-local Embeddings operations with retained
	// authentication and group mapping. The shared stream flag follows admission
	// output, and completion captures request facts before asynchronous
	// recording begins.
	NewEmbeddings(*gin.Context, AuxiliaryHTTPInput, *zap.Logger, *bool) EmbeddingHTTPExecution
	// NewAlphaSearch creates request-local search operations retaining original
	// payload and session identity. Its shared stream flag describes admission
	// output, while mapped forwarding bytes are supplied separately by the HTTP
	// caller.
	NewAlphaSearch(*gin.Context, AuxiliaryHTTPInput, *zap.Logger, *bool) AlphaHTTPExecution
	// ModerateVoice checks extracted TTS text as OpenAI Chat content and
	// presents blocking errors. True stops Voice execution, while false permits
	// the already funded request to continue toward provider selection.
	ModerateVoice(*gin.Context, *zap.Logger, MediaSubject, []byte) bool
	// NewVoice creates request-local Grok Voice operations with retained Key and
	// subscription.
	NewVoice(*gin.Context, AuxiliaryHTTPInput, *zap.Logger) media.VoicePorts
	// EndVoice presents terminal Voice selection or failover exhaustion when the
	// result requires it. Nil failure leaves the successful response intact;
	// unclassified forwarding output is owned by its platform adapter.
	EndVoice(*gin.Context, *media.VoiceFailure)
	// NewRealtime creates request-local Grok admission, relay and completion
	// operations.
	NewRealtime(*gin.Context, *zap.Logger) RealtimeHTTPExecution
	// RealtimeDialTimeout returns the timeout used for each upstream Realtime
	// handshake attempt.
	RealtimeDialTimeout() time.Duration
}

// AuxiliaryHandler admits Embeddings, AlphaSearch, Voice and Realtime HTTP
// requests through bound operations.
type AuxiliaryHandler struct {
	RequestLifetime // application active-request barrier binding

	base  *MediaHandler      // shared body and panic handling
	ports AuxiliaryHTTPPorts // required auxiliary HTTP operations
}

// mediaClientFrames adapts the accepted client WebSocket to the upstream
// frame interface.
type mediaClientFrames struct {
	conn *websocket.Conn // accepted client WebSocket transport
}

// EmbeddingHTTPExecution adds terminal HTTP presentation to the media
// Embeddings attempt operations.
type EmbeddingHTTPExecution interface {
	media.EmbeddingsPorts // media Embeddings attempt operations
	// EndEmbeddingFailure presents the terminal Embeddings stage after the media
	// loop returns. A nil value leaves successful output intact, while failed
	// selection and forwarding use their recorded output state.
	EndEmbeddingFailure(*media.EmbeddingFailure)
}

// AlphaHTTPExecution adds terminal HTTP presentation to the media AlphaSearch
// attempt operations.
type AlphaHTTPExecution interface {
	media.AlphaPorts // media AlphaSearch attempt operations
	// EndAlphaFailure presents terminal AlphaSearch selection, forwarding or
	// exhaustion state. A nil value leaves successful output intact, and
	// existing response bytes constrain any generic error appended for a
	// failure.
	EndAlphaFailure(*media.AlphaFailure)
}

// NewAuxiliaryHandler binds auxiliary operations and shared media
// body-reading support.
func NewAuxiliaryHandler(ports AuxiliaryHTTPPorts) *AuxiliaryHandler {
	return &AuxiliaryHandler{base: NewMediaHandler(ports), ports: ports}
}

// Embeddings admits a JSON request with a nonempty model before delegating
// media attempts. Authentication and dependencies precede body reading; model
// observation and group planning precede user admission. Funding is checked
// after obtaining user capacity, and failures retain the optional retry
// delay.
//
// The request-local execution adapter receives the parsed model and group
// mapping while media.RunEmbeddings receives the original bytes. That
// division lets each selected attempt rebuild forwarding input from the same
// request identity. The media loop returns terminal failure state for HTTP
// presentation, and request exit releases the user reservation.
func (h *AuxiliaryHandler) Embeddings(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false
	started := time.Now()
	access, ok := h.ports.Access(c)
	if !ok {
		h.ports.Error(c, 401, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := h.ports.Subject(c)
	if !ok {
		h.ports.Error(c, 500, "api_error", "User context not found")
		return
	}
	log := h.ports.Logger(c, "handler.openai_gateway.embeddings", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", access.ID), zap.Any("group_id", access.GroupID))
	if !h.ports.Dependencies(c, log) {
		return
	}
	body, ok := h.base.readBody(c)
	if !ok {
		return
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		h.ports.ParseFailure(log, body)
		h.ports.Error(c, 400, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	model, ok := media.RequiredModel(body, false)
	if !ok {
		h.ports.Error(c, 400, "invalid_request_error", "model is required")
		return
	}
	log = log.With(zap.String("model", model))
	h.ports.ObserveRequest(c, model, false, true)
	_, mapping := h.ports.Plan(c, model, true)
	h.ports.AuthLatency(c, time.Since(started))
	release, ok := h.ports.AcquireUser(c, subject, false, &streamStarted, log)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	if f := h.ports.Billing(c); f != nil {
		log.Info("openai_embeddings.billing_check_failed", zap.Error(f.Err))
		if f.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		h.ports.Error(c, f.Status, f.Code, f.Message)
		return
	}
	p := h.ports.NewEmbeddings(c, AuxiliaryHTTPInput{Subject: subject, Model: model, Body: body, Mapping: mapping}, log, &streamStarted)
	p.EndEmbeddingFailure(media.RunEmbeddings(c.Request.Context(), body, h.ports.MaxSwitches(), p))
}

// AlphaSearch admits a JSON search request, retains original input and
// forwards its group-mapped body. It requires a hydrated group and valid
// nonblank model before user admission. Funding runs after the user slot, and
// the search selection hash uses explicit session hints or the request ID
// field.
//
// The adapter retains original bytes for completion while
// media.RunAlphaSearch receives mapped forwarding bytes. Both forms refer to
// the same admitted request, so retries keep the selection identity and
// successful completion can hash its source payload. Directly deferred media
// recovery attempts a terminal protocol error on panic before logging the
// recovered stack.
func (h *AuxiliaryHandler) AlphaSearch(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false
	defer h.base.recoverMedia(c, &streamStarted)
	h.ports.HTTPTransport(c)
	started := time.Now()
	access, ok := h.ports.Access(c)
	if !ok || !access.HasGroup {
		h.ports.Error(c, 401, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := h.ports.Subject(c)
	if !ok {
		h.ports.Error(c, 500, "api_error", "User context not found")
		return
	}
	log := h.ports.Logger(c, "handler.openai_gateway.alpha_search", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", access.ID), zap.Any("group_id", access.GroupID))
	if !h.ports.Dependencies(c, log) {
		return
	}
	body, ok := h.base.readBody(c)
	if !ok {
		return
	}
	if err := wirejson.ValidateRequestObject(body); err != nil {
		h.ports.ParseFailure(log, body)
		h.ports.Error(c, 400, "invalid_request_error", RequestBodyParseErrorMessage(err))
		return
	}
	model, ok := media.RequiredModel(body, true)
	if !ok {
		h.ports.Error(c, 400, "invalid_request_error", "model is required")
		return
	}
	log = log.With(zap.String("model", model))
	h.ports.ObserveRequest(c, model, false, true)
	_, mapping := h.ports.Plan(c, model, true)
	forward := body
	if mapping.Mapped {
		forward = h.ports.RewriteModel(body, mapping.MappedModel)
	}
	h.ports.AuthLatency(c, time.Since(started))
	release, ok := h.ports.AcquireUser(c, subject, false, &streamStarted, log)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	if f := h.ports.Billing(c); f != nil {
		if f.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		h.ports.Error(c, f.Status, f.Code, f.Message)
		return
	}
	hash := h.ports.FallbackSession(c, strings.TrimSpace(gjson.GetBytes(body, "id").String()))
	p := h.ports.NewAlphaSearch(c, AuxiliaryHTTPInput{Subject: subject, Model: model, Body: body, Mapping: mapping, SessionHash: hash}, log, &streamStarted)
	p.EndAlphaFailure(media.RunAlphaSearch(c.Request.Context(), forward, h.ports.MaxSwitches(), p))
}

// GrokVoice checks group availability and funding before reading its
// method-dependent request body. The tts operation extracts text for
// moderation in a Chat-shaped audit object; other Voice operations proceed
// with their original body. Empty Content-Type uses application/json.
func (h *AuxiliaryHandler) GrokVoice(c *gin.Context, endpoint string) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	access, ok := h.ports.Access(c)
	if !ok || !access.HasGroup {
		h.ports.Error(c, http.StatusNotFound, "not_found_error", "Voice API is not supported for this platform")
		return
	}
	if !h.ports.Dependencies(c, nil) {
		return
	}
	if f := h.ports.Billing(c); f != nil {
		if f.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		h.ports.Error(c, f.Status, f.Code, f.Message)
		return
	}
	body, err := ReadGrokVoiceBody(c)
	if err != nil {
		h.ports.Error(c, 400, "invalid_request_error", err.Error())
		return
	}
	contentType := c.GetHeader("Content-Type")
	trimmedType := strings.ToLower(strings.TrimSpace(contentType))
	if len(body) > 0 && (endpoint == "tts" || trimmedType == "" || strings.HasPrefix(trimmedType, "application/json")) {
		if err := wirejson.ValidateRequestObject(body); err != nil {
			h.ports.Error(c, 400, "invalid_request_error", RequestBodyParseErrorMessage(err))
			return
		}
	}
	if endpoint == "tts" {
		subject, _ := h.ports.Subject(c)
		log := h.ports.Logger(c, "handler.openai_gateway.grok_voice", zap.String("endpoint", endpoint))
		auditBody := body
		// Present extracted TTS text to the Chat moderation protocol. If extraction
		// or object encoding supplies no replacement, moderation receives the original bytes.
		if input := media.TTSInputText(body); input != "" {
			if b, err := json.Marshal(struct {
				Messages []map[string]string `json:"messages"` // extracted TTS audit message
			}{Messages: []map[string]string{{"role": "user", "content": input}}}); err == nil {
				auditBody = b
			}
		}
		if h.ports.ModerateVoice(c, log, subject, auditBody) {
			return
		}
	}
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	log := h.ports.Logger(c, "handler.openai_gateway.grok_voice", zap.String("endpoint", endpoint))
	p := h.ports.NewVoice(c, AuxiliaryHTTPInput{Endpoint: endpoint}, log)
	h.ports.EndVoice(c, media.RunVoice(c.Request.Context(), media.VoiceRequest{Endpoint: endpoint, Body: body, ContentType: contentType}, p))
}

// GrokRealtime admits a WebSocket request and opens a Grok upstream before
// accepting the client connection. It requires group, dependencies and
// funding; a blank model selects grok-voice-latest. Upstream admission
// distinguishes wait rejection, absent candidates and failed dialing,
// returning a lease that owns connection and provider cleanup.
//
// The HTTP path keeps that lease until relay returns and closes the client
// transport separately. Elapsed time begins after the client upgrade.
// Completion requires observed audio and positive duration after nil or
// accepted close errors; another error sends an internal close and returns
// before recording audio usage. The completion adapter captures
// authentication and subscription before request teardown.
func (h *AuxiliaryHandler) GrokRealtime(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	if c == nil || c.Request == nil || !mediaWebsocketUpgrade(c.Request) {
		h.ports.Error(c, 426, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	access, ok := h.ports.Access(c)
	if !ok || !access.HasGroup {
		h.ports.Error(c, 404, "not_found_error", "Realtime API is not supported for this platform")
		return
	}
	if !h.ports.Dependencies(c, nil) {
		return
	}
	if f := h.ports.Billing(c); f != nil {
		if f.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(f.RetryAfter))
		}
		h.ports.Error(c, f.Status, f.Code, f.Message)
		return
	}
	log := h.ports.Logger(c, "handler.openai_gateway.grok_realtime")
	model := c.Query("model")
	if strings.TrimSpace(model) == "" {
		model = "grok-voice-latest"
	}
	p := h.ports.NewRealtime(c, log)
	admitted := media.OpenRealtime(c.Request.Context(), model, h.ports.RealtimeDialTimeout(), p)
	if admitted.WaitRejected {
		return
	}
	lease := admitted.Lease
	if lease == nil {
		if !admitted.CandidateSeen {
			h.ports.Error(c, 503, "api_error", "No available Grok providers")
		} else {
			h.ports.Error(c, 502, "upstream_error", "Grok realtime upstream unavailable")
		}
		return
	}
	// Retain upstream connection and provider ownership through relay and completion.
	// Request-exit cleanup releases that lease even when client upgrade or copying fails.
	defer func() { _ = lease.Close() }()
	conn, err := websocket.Accept(c.Writer, c.Request, &websocket.AcceptOptions{CompressionMode: websocket.CompressionContextTakeover})
	if err != nil {
		return
	}
	defer func() { _ = conn.CloseNow() }()
	started := time.Now()
	observed, err := p.Relay(c.Request.Context(), mediaClientFrames{conn}, lease.Conn)
	elapsed := time.Since(started)
	if err != nil {
		log.Info("grok_realtime.proxy_failed", zap.Error(err))
		// An unexpected relay failure is terminal before completion. Accepted departure
		// codes still permit recording observed audio over the measured session interval.
		if !IsExpectedGrokRealtimeClose(err) {
			_ = conn.Close(websocket.StatusInternalError, "upstream realtime websocket failed")
			return
		}
	}
	if media.RealtimeAudioUsage(elapsed, observed) != nil {
		p.CompleteRealtime(c.Request.Context(), lease.Provider, model, elapsed)
	}
}

// ReadFrame returns the next client WebSocket payload with its upstream frame
// kind.
func (c mediaClientFrames) ReadFrame(ctx context.Context) (upstream.FrameKind, []byte, error) {
	kind, data, err := c.conn.Read(ctx)
	return upstream.FrameKind(kind), data, err
}

// WriteFrame sends an upstream frame kind as the corresponding WebSocket
// message.
func (c mediaClientFrames) WriteFrame(ctx context.Context, kind upstream.FrameKind, data []byte) error {
	return c.conn.Write(ctx, websocket.MessageType(kind), data)
}

// Close immediately tears down the accepted client WebSocket transport.
func (c mediaClientFrames) Close() error { return c.conn.CloseNow() }

// mediaWebsocketUpgrade checks Upgrade and Connection headers for the
// Realtime entry. Nil requests reject; Upgrade is trimmed and compared
// case-insensitively, while Connection uses a lowercase substring check for
// upgrade.
func mediaWebsocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Connection"))), "upgrade")
}
