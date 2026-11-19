package httpapi

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// media_handler.go prepares image and Grok video requests, including admission
// and saved video ownership resolution.
// Image generation keeps its parsed model and original body beside the group
// route used for provider selection. Permission and moderation run before
// local image admission; user admission then precedes the funding check and
// optional explicit-session isolation. The media loop receives those request
// facts and owns subsequent provider attempts.
//
// Grok video lookup starts from a saved task identifier, so it reads no
// generation body and restores the task's provider ownership before
// forwarding. An unresolved composite Key can regain its saved group through
// that lookup, while generation requests use current image permission and
// moderation. Rewritten forwarding bytes remain separate from the original
// body retained for completion, and video creation carries one creation start
// timestamp across its attempts.

// MediaAccess supplies authenticated Key and group facts used by media HTTP
// entry.
type MediaAccess struct {
	HasGroup bool  // hydrated group view present
	ID       int64 // authenticated API Key identifier
	// GroupID identifies the current group. It can be nil for unresolved
	// composite video lookup until the saved task owner restores a bound
	// Key view.
	GroupID *int64
	// Composite: Key has composite bindings. ImagesAllowed: group permits
	// generation.
	Composite, ImagesAllowed bool
}

// MediaSubject identifies the authenticated subject and its user concurrency
// allowance.
type MediaSubject struct {
	UserID      int64 // authenticated concurrency subject identifier
	Concurrency int   // allowed concurrent user requests
}

// GrokMediaInput carries the parsed model, video label, and moderation content.
type GrokMediaInput struct {
	VideoResolution string // normalized video billing label
	Model           string // parsed media request model
	ModerationBody  []byte // normalized moderation request bytes
}

// MediaHTTPFailure carries a funding error's client classification and
// diagnostic cause.
type MediaHTTPFailure struct {
	// Status, Code and Message come from the same funding error
	// classification and are presented together. Err retains the underlying
	// cause for request logs.
	Status int // classified HTTP response status
	// Code: client error category. Message: sanitized client error text.
	Code, Message string
	RetryAfter    int   // retry delay in seconds
	Err           error // underlying funding failure cause
}

// GenerationHTTPInput retains entry facts used to construct request-local
// media attempt adapters.
type GenerationHTTPInput struct {
	Grok    bool         // explicit Grok entry flag
	Subject MediaSubject // authenticated user admission facts
	// Parsed and Body retain the parsed image input and original request
	// bytes. The request adapter shares that storage across serial attempts
	// and captures completion before request exit; callers keep it stable
	// during execution.
	Parsed *media.ImageRequest // parsed image request state
	Body   []byte              // original media request bytes
	// RequestModel keeps the parsed model; RoutingModel is the entry's
	// selection model, which Images group-maps before construction.
	// SessionHash retains an explicit client identity or a saved-video
	// correlation identity.
	// RequestModel: parsed request model. RoutingModel: provider selection
	// model.
	// SessionHash: retained selection session hash.
	RequestModel, RoutingModel, SessionHash string
	// group model mapping result
	Mapping routing.GroupMappingResult
	// Endpoint selects Grok execution and RequestID identifies an existing
	// video task. ContentType describes forwarding bytes; VideoCreated is
	// the UTC RFC3339Nano creation start timestamp carried across
	// video-generation attempts.
	// Endpoint: Grok media operation. RequestID: existing video task
	// identifier.
	// ContentType: forwarding media content type.
	// VideoCreated: video creation start time.
	Endpoint, RequestID, ContentType, VideoCreated string
	// BoundProviderID pins video lookup to its saved provider when
	// positive. Ordinary generation leaves it zero and selects from current
	// eligible providers.
	BoundProviderID int64
}

// MediaHTTPPorts binds admission, routing and request-local attempt creation
// for media entry.
type MediaHTTPPorts interface {
	// Access returns authenticated media Key facts and their presence flag.
	// Successful entry paths require a usable view, including the current
	// group or unresolved composite state used to restore saved video
	// ownership.
	Access(*gin.Context) (*MediaAccess, bool)
	// Subject returns authenticated user identity, concurrency and a
	// presence flag. A false flag tells media entry to present its
	// missing-user-context error before any resource admission or provider
	// selection.
	Subject(*gin.Context) (MediaSubject, bool)
	// Logger constructs a request logger with the supplied component and
	// fields.
	Logger(*gin.Context, string, ...zap.Field) *zap.Logger
	// Dependencies checks the bound services needed by media entry before
	// body reading. A false result means the implementation has presented
	// its unavailable-service response, so the caller stops admission.
	Dependencies(*gin.Context, *zap.Logger) bool
	// Error writes the current entry's client error using the supplied
	// classification. Implementations choose the endpoint's error envelope,
	// including SystemOne formatting when the request path selects that
	// API.
	Error(*gin.Context, int, string, string)
	// StreamingError presents a classified error using the current stream
	// startup flag. A started stream requires its stream error
	// representation; an unstarted request can still use an ordinary HTTP
	// error.
	StreamingError(*gin.Context, int, string, string, bool)
	// EnsureForwardError attempts a terminal generic error when existing
	// output permits presentation. It reports the attempt for panic
	// logging; the result does not certify accepted bytes at the client.
	EnsureForwardError(*gin.Context, bool) bool
	// ObserveRequest records model and stream metadata for Ops request
	// diagnostics. A true final flag also records endpoint request type
	// from the stream flag; false updates request metadata alone.
	ObserveRequest(*gin.Context, string, bool, bool)
	// AuthLatency records elapsed entry admission time as milliseconds in
	// Ops metadata.
	AuthLatency(*gin.Context, time.Duration)
	// Plan resolves group mapping and returns a context carrying the route
	// plan. When bind is true, it installs that context on c.Request; false
	// leaves installation to the caller using the returned context.
	Plan(*gin.Context, string, bool) (context.Context, routing.GroupMappingResult)
	// ImagePermissionMessage returns the client message for denied image
	// generation permission.
	ImagePermissionMessage() string
	// ImagePolicyDenied marks request telemetry for the local image feature
	// gate.
	ImagePolicyDenied(*gin.Context)
	// Moderate checks parsed image content and reports whether entry should
	// stop. A blocked decision has its client error presented before true
	// is returned, so the caller skips slots and execution.
	Moderate(*gin.Context, *zap.Logger, MediaSubject, string, []byte) bool
	// CyberSnapshot retains media request content for later upstream Cyber
	// warning handling.
	CyberSnapshot(*gin.Context, []byte)
	// AcquireImage reserves local image capacity and returns cleanup with
	// its acceptance flag. Disabled admission can return nil and true;
	// callers release nonnil reservations at request exit and stop on
	// rejection.
	AcquireImage(*gin.Context, bool) (func(), bool)
	// AcquireUser obtains user capacity, possibly updating the shared
	// stream startup flag during waits. Success returns request cleanup;
	// callers release it at exit and check funding after admission before
	// selecting a provider.
	AcquireUser(*gin.Context, MediaSubject, bool, *bool, *zap.Logger) (func(), bool)
	// BindErrors installs optional upstream error-presentation rules on the
	// current request. A missing rules service leaves existing presentation
	// configuration usable, allowing media admission to continue with the
	// shared error writer.
	BindErrors(*gin.Context)
	// Billing checks the current authenticated funding state and returns
	// its classified failure. Nil means admission may continue; the caller
	// presents a nonnil failure and its positive retry delay before
	// returning.
	Billing(*gin.Context) *MediaHTTPFailure
	// ExplicitSession derives a selection hash from explicit OpenAI request
	// hints and supplied body. Missing explicit identity returns an empty
	// hash, so Images skips the session-isolation check for that request.
	ExplicitSession(*gin.Context, []byte) string
	// Isolate checks the explicit session's allowed user ownership and
	// presents any failure. True means the caller stops; false permits
	// execution with the same retained session hash and request identity.
	Isolate(*gin.Context, int64, string, bool) bool
	// ImageContext returns request context marked for the Images endpoint
	// and image intent.
	ImageContext(*gin.Context) context.Context
	// NewGenerationPorts constructs one request-local adapter retaining
	// authentication and input facts for serial media attempts. The stream
	// flag remains shared with HTTP admission, and successful completion is
	// captured before request teardown.
	NewGenerationPorts(*gin.Context, GenerationHTTPInput, *zap.Logger, *bool) media.GenerationPorts
	// MaxSwitches returns the configured media provider transition
	// allowance for this request.
	MaxSwitches() int
	// ParseGrok extracts a model and moderation content through the Grok
	// media codec. Generation entry checks the returned model before
	// execution, while saved-video lookup can continue with empty
	// body-derived model data.
	ParseGrok(string, []byte) GrokMediaInput
	// ResolveCompositeVideo restores a composite Key view and provider from
	// saved video ownership. A successful result republishes that view in
	// request context; missing ownership is presented by the caller as not
	// found.
	ResolveCompositeVideo(*gin.Context, string, int64) (*MediaAccess, int64, error)
	// ResolveVideoProvider reads the saved provider for a video, user and
	// Key identity. Callers require a positive returned ID for lookup; an
	// error or absent identity produces the same client not-found response.
	ResolveVideoProvider(context.Context, *int64, string, int64, int64) (int64, error)
	// RewriteGrok replaces the selected media model while retaining request
	// content encoding. It returns forwarding bytes and their content type,
	// so callers pass both together to the current platform attempt.
	RewriteGrok([]byte, string, string) ([]byte, string, error)
}

// MediaHandler orders media HTTP admission before delegating provider
// attempts and completion.
type MediaHandler struct {
	RequestLifetime                // application active-request barrier binding
	ports           MediaHTTPPorts // required HTTP entry operations
}

// NewMediaHandler binds the HTTP admission operations used by media
// endpoints.
func NewMediaHandler(ports MediaHTTPPorts) *MediaHandler { return &MediaHandler{ports: ports} }

// readBody reads a nonempty media request through the shared bounded body
// reader. Oversized input receives status 413, other read failures and empty
// input receive 400, and false tells the entry to return.
func (h *MediaHandler) readBody(c *gin.Context) ([]byte, bool) {
	body, err := ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		if exceeded, ok := errors.AsType[*http.MaxBytesError](err); ok {
			h.ports.Error(c, 413, "invalid_request_error", BodyTooLargeMessage(exceeded.Limit))
		} else {
			h.ports.Error(c, 400, "invalid_request_error", RequestBodyReadErrorMessage(err))
		}
		return nil, false
	}
	if len(body) == 0 {
		h.ports.Error(c, 400, "invalid_request_error", "Request body is empty")
		return nil, false
	}
	return body, true
}

// Images admits a parsed image request and delegates serial provider attempts
// to media.RunImages. Authentication and required services precede body
// reading; group mapping determines selection capability before image
// permission and moderation. Entry registers image and user cleanup at request
// exit; shared user admission also releases its reservation on cancellation.
//
// Funding is checked after user admission, then a supplied explicit session
// is checked for ownership. Parsed input, original bytes, group mapping and
// the selection model remain separate in GenerationHTTPInput, allowing
// forwarding and completion to use their respective identities. ImageContext
// marks endpoint intent before the attempt adapter is constructed.
func (h *MediaHandler) Images(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false
	defer h.recoverMedia(c, &streamStarted)
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
	log := h.ports.Logger(c, "handler.openai_gateway.images", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", access.ID), zap.Any("group_id", access.GroupID))
	if !h.ports.Dependencies(c, log) {
		return
	}
	body, ok := h.readBody(c)
	if !ok {
		return
	}
	h.ports.ObserveRequest(c, "", false, false)
	parsed, err := media.ParseImageRequest(c.Request.URL.Path, c.GetHeader("Content-Type"), body, false)
	if err != nil {
		h.ports.Error(c, 400, "invalid_request_error", err.Error())
		return
	}
	requestModel := parsed.Model
	_, mapping := h.ports.Plan(c, requestModel, true)
	routingModel := strings.TrimSpace(mapping.MappedModel)
	if routingModel == "" {
		routingModel = strings.TrimSpace(requestModel)
	}
	// The group-mapped name may still be a provider alias. Image
	// eligibility is checked against each candidate's final upstream model.
	routed := *parsed
	routed.Model = routingModel
	parsed.RequiredCapability = media.ClassifyImageCapability(&routed)
	log = log.With(zap.String("model", requestModel), zap.Bool("stream", parsed.Stream), zap.Bool("multipart", parsed.Multipart), zap.String("capability", string(parsed.RequiredCapability)), zap.String("img_quality", parsed.Quality), zap.String("img_size", parsed.Size))
	if !access.ImagesAllowed {
		h.ports.ImagePolicyDenied(c)
		h.ports.Error(c, 403, "permission_error", h.ports.ImagePermissionMessage())
		return
	}
	if h.ports.Moderate(c, log, subject, requestModel, parsed.ModerationBody()) {
		return
	}
	h.ports.CyberSnapshot(c, parsed.ModerationBody())
	release, ok := h.ports.AcquireImage(c, streamStarted)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	h.ports.ObserveRequest(c, requestModel, parsed.Stream, true)
	h.ports.BindErrors(c)
	h.ports.AuthLatency(c, time.Since(started))
	routingStarted := time.Now()
	userRelease, ok := h.ports.AcquireUser(c, subject, parsed.Stream, &streamStarted, log)
	if !ok {
		return
	}
	if userRelease != nil {
		defer userRelease()
	}
	if failure := h.ports.Billing(c); failure != nil {
		log.Info("openai.images.billing_eligibility_check_failed", zap.Error(failure.Err))
		if failure.RetryAfter > 0 {
			c.Header("Retry-After", strconv.Itoa(failure.RetryAfter))
		}
		h.ports.StreamingError(c, failure.Status, failure.Code, failure.Message, streamStarted)
		return
	}
	sessionHash := h.ports.ExplicitSession(c, body)
	if sessionHash != "" && h.ports.Isolate(c, subject.UserID, sessionHash, streamStarted) {
		return
	}
	ctx := h.ports.ImageContext(c)
	input := GenerationHTTPInput{Subject: subject, ContentType: c.GetHeader("Content-Type"), Parsed: parsed, Body: body, RequestModel: requestModel, RoutingModel: routingModel, SessionHash: sessionHash, Mapping: mapping}
	media.RunImages(ctx, media.GenerationRequest{Body: body, Stream: parsed.Stream, MaxSwitches: h.ports.MaxSwitches(), RoutingStarted: routingStarted}, h.ports.NewGenerationPorts(c, input, log, &streamStarted))
}

// GrokVideoGeneration dispatches the request to Grok video creation
// admission.
func (h *MediaHandler) GrokVideoGeneration(c *gin.Context) { h.GrokMedia(c, "videos_generations", "") }

// GrokVideoEdit dispatches the request to Grok video editing admission.
func (h *MediaHandler) GrokVideoEdit(c *gin.Context) { h.GrokMedia(c, "videos_edits", "") }

// GrokVideoExtension dispatches the request to Grok video extension
// admission.
func (h *MediaHandler) GrokVideoExtension(c *gin.Context) { h.GrokMedia(c, "videos_extensions", "") }

// GrokVideoStatus looks up the video task identified by the route request_id.
func (h *MediaHandler) GrokVideoStatus(c *gin.Context) {
	h.GrokMedia(c, "video_status", c.Param("request_id"))
}

// GrokVideoContent retrieves content for the video task identified by route
// request_id.
func (h *MediaHandler) GrokVideoContent(c *gin.Context) {
	h.GrokMedia(c, "video_content", c.Param("request_id"))
}

// GrokMedia admits generation or saved-video lookup before delegating
// provider attempts. Lookup reads no body and requires a task ID; generation
// requires a parsed model and applies image permission, moderation and local
// image admission. Both paths reserve user capacity for the HTTP request.
//
// An unresolved composite lookup restores the saved group and provider before
// execution, and that case skips the subsequent funding check. Other requests
// check funding after user admission. Lookup replaces explicit-session
// selection with the task's user-and-Key correlation hash and positive saved
// provider ID. Group mapping rewrites forwarding bytes while original input
// stays retained, and one video creation timestamp is carried through the
// attempt adapter. New video requests also validate the resolution before
// admission so their eventual usage label fits persistent storage.
func (h *MediaHandler) GrokMedia(c *gin.Context, endpoint, requestID string) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	streamStarted := false
	defer h.recoverMedia(c, &streamStarted)
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
	log := h.ports.Logger(c, "handler.openai_gateway.grok_media", zap.Int64("user_id", subject.UserID), zap.Int64("api_key_id", access.ID), zap.Any("group_id", access.GroupID), zap.String("endpoint", endpoint))
	if !h.ports.Dependencies(c, log) {
		return
	}
	lookup := endpoint == "video_status" || endpoint == "video_content"
	generation := media.IsVideoCreate(endpoint) || endpoint == "images_generations" || endpoint == "images_edits"
	var body []byte
	contentType := c.GetHeader("Content-Type")
	if !lookup {
		body, ok = h.readBody(c)
		if !ok {
			return
		}
		if err := grok.ValidateGrokMediaRequest(contentType, body); err != nil {
			h.ports.Error(c, 400, "invalid_request_error", RequestBodyParseErrorMessage(err))
			return
		}
	}
	parsed := h.ports.ParseGrok(contentType, body)
	if media.IsVideoCreate(endpoint) {
		if err := pricing.ValidateVideoBillingResolution(parsed.VideoResolution); err != nil {
			h.ports.Error(c, 400, "invalid_request_error", err.Error())
			return
		}
	}
	requestModel := parsed.Model
	routingModel := strings.TrimSpace(requestModel)
	if generation && strings.TrimSpace(requestModel) == "" {
		h.ports.Error(c, 400, "invalid_request_error", "model is required")
		return
	}
	if lookup && strings.TrimSpace(requestID) == "" {
		h.ports.Error(c, 400, "invalid_request_error", "request_id is required")
		return
	}
	log = log.With(zap.String("model", requestModel))
	h.ports.ObserveRequest(c, requestModel, false, true)
	bound := int64(0)
	compositeLookup := lookup && access.Composite && access.GroupID == nil
	// A model-free composite lookup restores the original video owner
	// before current request routing, so later selection remains tied to
	// that saved provider.
	if compositeLookup {
		var err error
		access, bound, err = h.ports.ResolveCompositeVideo(c, requestID, subject.UserID)
		if err != nil || access == nil || bound <= 0 {
			log.Info("grok_media.video_lookup_owner_binding_missing", zap.Error(err))
			h.ports.Error(c, 404, "not_found_error", "Video request not found")
			return
		}
		log = log.With(zap.Any("resolved_group_id", access.GroupID))
	}
	if generation {
		if !access.ImagesAllowed {
			h.ports.Error(c, 403, "permission_error", h.ports.ImagePermissionMessage())
			return
		}
		if len(parsed.ModerationBody) > 0 && h.ports.Moderate(c, log, subject, requestModel, parsed.ModerationBody) {
			return
		}
		release, acquired := h.ports.AcquireImage(c, streamStarted)
		if !acquired {
			return
		}
		if release != nil {
			defer release()
		}
	}
	h.ports.BindErrors(c)
	h.ports.AuthLatency(c, time.Since(started))
	release, ok := h.ports.AcquireUser(c, subject, false, &streamStarted, log)
	if !ok {
		return
	}
	if release != nil {
		defer release()
	}
	// Saved composite task lookup retains its restored ownership and skips
	// this funding check; generation and ordinary lookup still check after
	// user admission.
	if !compositeLookup {
		if failure := h.ports.Billing(c); failure != nil {
			log.Info("grok_media.billing_eligibility_check_failed", zap.Error(failure.Err))
			if failure.RetryAfter > 0 {
				c.Header("Retry-After", strconv.Itoa(failure.RetryAfter))
			}
			h.ports.Error(c, failure.Status, failure.Code, failure.Message)
			return
		}
	}
	seed := body
	if len(seed) == 0 && strings.TrimSpace(requestID) != "" {
		seed = []byte(requestID)
	}
	sessionHash := h.ports.ExplicitSession(c, seed)
	// Use task, user and Key correlation for lookup rather than a
	// caller-supplied session hint, so retrieval cannot migrate a saved
	// task to another provider.
	if lookup {
		sessionHash = media.GrokMediaVideoRequestSessionHash(requestID, subject.UserID, access.ID)
		if bound <= 0 {
			var err error
			bound, err = h.ports.ResolveVideoProvider(c.Request.Context(), access.GroupID, requestID, subject.UserID, access.ID)
			if err != nil || bound <= 0 {
				log.Info("grok_media.video_lookup_owner_binding_missing", zap.Error(err))
				h.ports.Error(c, 404, "not_found_error", "Video request not found")
				return
			}
		}
	}
	ctx, mapping := h.ports.Plan(c, routingModel, false)
	forwardBody, forwardType, err := media.RewriteMappedMediaBody(body, contentType, mapping.Mapped, mapping.MappedModel, h.ports.RewriteGrok)
	if err != nil {
		h.ports.Error(c, 400, "invalid_request_error", "Failed to rewrite request model")
		return
	}
	created := ""
	if media.IsVideoCreate(endpoint) {
		created = media.GrokVideoPendingCreatedAtNow()
	}
	input := GenerationHTTPInput{Grok: true, Subject: subject, Body: body, RequestModel: requestModel, RoutingModel: routingModel, SessionHash: sessionHash, Mapping: mapping, Endpoint: endpoint, RequestID: requestID, ContentType: forwardType, BoundProviderID: bound, VideoCreated: created}
	media.RunGrokMedia(ctx, media.GenerationRequest{Body: forwardBody, MaxSwitches: h.ports.MaxSwitches(), RoutingStarted: time.Now(), Generation: generation, VideoLookup: lookup, BoundProviderID: bound}, h.ports.NewGenerationPorts(c, input, log, &streamStarted))
}

// recoverMedia intercepts a panic when deferred directly by a media entry. It
// uses the shared stream flag to attempt terminal error presentation and logs
// the recovered value with its stack and presentation result.
func (h *MediaHandler) recoverMedia(c *gin.Context, stream *bool) {
	recovered := recover()
	if recovered == nil {
		return
	}
	started := false
	if stream != nil {
		started = *stream
	}
	wrote := h.ports.EnsureForwardError(c, started)
	h.ports.Logger(c, "handler.openai_gateway.responses").Error("openai.responses_panic_recovered", zap.Bool("fallback_error_response_written", wrote), zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
}
