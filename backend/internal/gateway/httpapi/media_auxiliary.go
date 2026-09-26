package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// AuxiliaryHTTPInput 固化分组映射、原报文与客户端模型；实际认证实体留在调用方 Adapter。
type AuxiliaryHTTPInput struct {
	Subject                      MediaSubject
	Model, SessionHash, Endpoint string
	Body                         []byte
	Mapping                      routing.GroupMappingResult
}
type RealtimeHTTPExecution interface {
	media.RealtimePorts
	Relay(context.Context, upstream.FrameConn, upstream.FrameConn) (bool, error)
	CompleteRealtime(context.Context, account.AccountSnapshot, string, time.Duration)
}

// AuxiliaryHTTPPorts 只补充辅助入口独有的装配，不暴露供应商或存储实现。
type AuxiliaryHTTPPorts interface {
	MediaHTTPPorts
	HTTPTransport(*gin.Context)
	ParseFailure(*zap.Logger, []byte)
	RewriteModel([]byte, string) []byte
	FallbackSession(*gin.Context, string) string
	NewEmbeddings(*gin.Context, AuxiliaryHTTPInput, *zap.Logger, *bool) EmbeddingHTTPExecution
	NewAlphaSearch(*gin.Context, AuxiliaryHTTPInput, *zap.Logger, *bool) AlphaHTTPExecution
	ModerateVoice(*gin.Context, *zap.Logger, MediaSubject, []byte) bool
	NewVoice(*gin.Context, AuxiliaryHTTPInput, *zap.Logger) media.VoicePorts
	EndVoice(*gin.Context, *media.VoiceFailure)
	NewRealtime(*gin.Context, *zap.Logger) RealtimeHTTPExecution
	RealtimeDialTimeout() time.Duration
}

// AuxiliaryHandler 直接拥有 Embeddings、AlphaSearch、Voice 与 Realtime 的 HTTP 契约。
type AuxiliaryHandler struct {
	requestLifetime

	base  *MediaHandler
	ports AuxiliaryHTTPPorts
}

func NewAuxiliaryHandler(ports AuxiliaryHTTPPorts) *AuxiliaryHandler {
	return &AuxiliaryHandler{base: NewMediaHandler(ports), ports: ports}
}

func (h *AuxiliaryHandler) Embeddings(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
	if !gjson.ValidBytes(body) {
		h.ports.ParseFailure(log, body)
		h.ports.Error(c, 400, "invalid_request_error", "Failed to parse request body")
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

func (h *AuxiliaryHandler) AlphaSearch(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
	if access.Platform != "openai" {
		h.ports.Error(c, 404, "not_found_error", "Codex alpha search is only available for OpenAI groups")
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
	if !gjson.ValidBytes(body) {
		h.ports.ParseFailure(log, body)
		h.ports.Error(c, 400, "invalid_request_error", "Failed to parse request body")
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

func (h *AuxiliaryHandler) GrokVoice(c *gin.Context, endpoint string) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	access, ok := h.ports.Access(c)
	if !ok || !access.HasGroup || access.Platform != "grok" {
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
	if endpoint == "tts" {
		subject, _ := h.ports.Subject(c)
		log := h.ports.Logger(c, "handler.openai_gateway.grok_voice", zap.String("endpoint", endpoint))
		auditBody := body
		if input := media.TTSInputText(body); input != "" {
			if b, err := json.Marshal(struct {
				Messages []map[string]string `json:"messages"`
			}{Messages: []map[string]string{{"role": "user", "content": input}}}); err == nil {
				auditBody = b
			}
		}
		if h.ports.ModerateVoice(c, log, subject, auditBody) {
			return
		}
	}
	contentType := c.GetHeader("Content-Type")
	if strings.TrimSpace(contentType) == "" {
		contentType = "application/json"
	}
	log := h.ports.Logger(c, "handler.openai_gateway.grok_voice", zap.String("endpoint", endpoint))
	p := h.ports.NewVoice(c, AuxiliaryHTTPInput{Endpoint: endpoint}, log)
	h.ports.EndVoice(c, media.RunVoice(c.Request.Context(), media.VoiceRequest{Endpoint: endpoint, Body: body, ContentType: contentType}, p))
}

func (h *AuxiliaryHandler) GrokRealtime(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	if c == nil || c.Request == nil || !mediaWebsocketUpgrade(c.Request) {
		h.ports.Error(c, 426, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	access, ok := h.ports.Access(c)
	if !ok || !access.HasGroup || access.Platform != "grok" {
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
			h.ports.Error(c, 503, "api_error", "No available Grok accounts")
		} else {
			h.ports.Error(c, 502, "upstream_error", "Grok realtime upstream unavailable")
		}
		return
	}
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
		if !IsExpectedGrokRealtimeClose(err) {
			_ = conn.Close(websocket.StatusInternalError, "upstream realtime websocket failed")
			return
		}
	}
	if media.RealtimeAudioUsage(elapsed, observed) != nil {
		p.CompleteRealtime(c.Request.Context(), lease.Account, model, elapsed)
	}
}

type mediaClientFrames struct{ conn *websocket.Conn }

func (c mediaClientFrames) ReadFrame(ctx context.Context) (upstream.FrameKind, []byte, error) {
	kind, data, err := c.conn.Read(ctx)
	return upstream.FrameKind(kind), data, err
}

func (c mediaClientFrames) WriteFrame(ctx context.Context, kind upstream.FrameKind, data []byte) error {
	return c.conn.Write(ctx, websocket.MessageType(kind), data)
}
func (c mediaClientFrames) Close() error { return c.conn.CloseNow() }

type EmbeddingHTTPExecution interface {
	media.EmbeddingsPorts
	EndEmbeddingFailure(*media.EmbeddingFailure)
}
type AlphaHTTPExecution interface {
	media.AlphaPorts
	EndAlphaFailure(*media.AlphaFailure)
}

func mediaWebsocketUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Connection"))), "upgrade")
}
