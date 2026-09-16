package httpapi

import (
	"context"
	"errors"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// MediaAccess 仅包含 HTTP 准入、日志和复合资源查找所需字段。
type MediaAccess struct {
	HasGroup                 bool
	Platform                 string
	ID                       int64
	GroupID                  *int64
	Composite, ImagesAllowed bool
}
type MediaSubject struct {
	UserID      int64
	Concurrency int
}
type GrokMediaInput struct {
	Model          string
	HasInputImage  bool
	ModerationBody []byte
}
type MediaHTTPFailure struct {
	Status        int
	Code, Message string
	RetryAfter    int
	Err           error
}

// GenerationHTTPInput 把已解析的输入交给固定依赖工厂，不携带旧身份或账号对象。
type GenerationHTTPInput struct {
	Grok                                           bool
	Subject                                        MediaSubject
	Parsed                                         *media.ImageRequest
	Body                                           []byte
	RequestModel, RoutingModel, SessionHash        string
	Mapping                                        routing.ChannelMappingResult
	Endpoint, RequestID, ContentType, VideoCreated string
	BoundAccountID                                 int64
}
type MediaHTTPPorts interface {
	Access(*gin.Context) (*MediaAccess, bool)
	Subject(*gin.Context) (MediaSubject, bool)
	Logger(*gin.Context, string, ...zap.Field) *zap.Logger
	Dependencies(*gin.Context, *zap.Logger) bool
	Error(*gin.Context, int, string, string)
	StreamingError(*gin.Context, int, string, string, bool)
	EnsureForwardError(*gin.Context, bool) bool
	ObserveRequest(*gin.Context, string, bool, bool)
	AuthLatency(*gin.Context, time.Duration)
	Plan(*gin.Context, string, bool) (context.Context, routing.ChannelMappingResult)
	ImagePermissionMessage() string
	ImagePolicyDenied(*gin.Context)
	Moderate(*gin.Context, *zap.Logger, MediaSubject, string, []byte) bool
	CyberSnapshot(*gin.Context, []byte)
	AcquireImage(*gin.Context, bool) (func(), bool)
	AcquireUser(*gin.Context, MediaSubject, bool, *bool, *zap.Logger) (func(), bool)
	BindErrors(*gin.Context)
	Billing(*gin.Context) *MediaHTTPFailure
	ExplicitSession(*gin.Context, []byte) string
	Isolate(*gin.Context, int64, string, bool) bool
	ImageContext(*gin.Context) context.Context
	NewGenerationPorts(*gin.Context, GenerationHTTPInput, *zap.Logger, *bool) media.GenerationPorts
	MaxSwitches() int
	ParseGrok(string, []byte) GrokMediaInput
	NormalizeGrok(string, string, bool) string
	ResolveCompositeVideo(*gin.Context, string, int64) (*MediaAccess, int64, error)
	ResolveVideoAccount(context.Context, *int64, string, int64, int64) (int64, error)
	RewriteGrok([]byte, string, string) ([]byte, string, error)
}

// MediaHandler 独占媒体路由的 HTTP 准入、错误响应和同步输出；业务尝试由 media 执行。
type MediaHandler struct {
	requestLifetime
	ports MediaHTTPPorts
}

func NewMediaHandler(ports MediaHTTPPorts) *MediaHandler { return &MediaHandler{ports: ports} }

func (h *MediaHandler) readBody(c *gin.Context) ([]byte, bool) {
	body, err := ReadRequestBodyWithPrealloc(c.Request)
	if err != nil {
		var exceeded *http.MaxBytesError
		if errors.As(err, &exceeded) {
			h.ports.Error(c, 413, "invalid_request_error", BodyTooLargeMessage(exceeded.Limit))
		} else {
			h.ports.Error(c, 400, "invalid_request_error", "Failed to read request body")
		}
		return nil, false
	}
	if len(body) == 0 {
		h.ports.Error(c, 400, "invalid_request_error", "Request body is empty")
		return nil, false
	}
	return body, true
}
func (h *MediaHandler) Images(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
	if err := parsed.ValidateRoutingModel(routingModel); err != nil {
		h.ports.Error(c, 400, "invalid_request_error", err.Error())
		return
	}
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
	input := GenerationHTTPInput{Subject: subject, Parsed: parsed, Body: body, RequestModel: requestModel, RoutingModel: routingModel, SessionHash: sessionHash, Mapping: mapping}
	media.RunImages(ctx, media.GenerationRequest{Body: body, Stream: parsed.Stream, MaxSwitches: h.ports.MaxSwitches(), RoutingStarted: routingStarted}, h.ports.NewGenerationPorts(c, input, log, &streamStarted))
}

func (h *MediaHandler) GrokImages(c *gin.Context) {
	endpoint := "images_generations"
	if strings.Contains(c.Request.URL.Path, "/images/edits") {
		endpoint = "images_edits"
	}
	h.GrokMedia(c, endpoint, "")
}
func (h *MediaHandler) GrokVideoGeneration(c *gin.Context) { h.GrokMedia(c, "videos_generations", "") }
func (h *MediaHandler) GrokVideoEdit(c *gin.Context)       { h.GrokMedia(c, "videos_edits", "") }
func (h *MediaHandler) GrokVideoExtension(c *gin.Context)  { h.GrokMedia(c, "videos_extensions", "") }
func (h *MediaHandler) GrokVideoStatus(c *gin.Context) {
	h.GrokMedia(c, "video_status", c.Param("request_id"))
}
func (h *MediaHandler) GrokVideoContent(c *gin.Context) {
	h.GrokMedia(c, "video_content", c.Param("request_id"))
}
func (h *MediaHandler) GrokMedia(c *gin.Context, endpoint, requestID string) {
	done, accepted := h.beginRequest(c, "openai")
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
	if !lookup {
		body, ok = h.readBody(c)
		if !ok {
			return
		}
	}
	contentType := c.GetHeader("Content-Type")
	parsed := h.ports.ParseGrok(contentType, body)
	requestModel := parsed.Model
	routingModel := h.ports.NormalizeGrok(endpoint, requestModel, parsed.HasInputImage)
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
	if lookup {
		sessionHash = media.GrokMediaVideoRequestSessionHash(requestID, subject.UserID, access.ID)
		if bound <= 0 {
			var err error
			bound, err = h.ports.ResolveVideoAccount(c.Request.Context(), access.GroupID, requestID, subject.UserID, access.ID)
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
	input := GenerationHTTPInput{Grok: true, Subject: subject, Body: body, RequestModel: requestModel, RoutingModel: routingModel, SessionHash: sessionHash, Mapping: mapping, Endpoint: endpoint, RequestID: requestID, ContentType: forwardType, BoundAccountID: bound, VideoCreated: created}
	media.RunGrokMedia(ctx, media.GenerationRequest{Body: forwardBody, MaxSwitches: h.ports.MaxSwitches(), RoutingStarted: time.Now(), Generation: generation, VideoLookup: lookup, BoundAccountID: bound}, h.ports.NewGenerationPorts(c, input, log, &streamStarted))
}

// recoverMedia 在直接 defer 中读取 panic，保留旧 fallback 与诊断字段。
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
