package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"

	gatewaylive "github.com/TokenFlux/TokenRouter/internal/gateway/live"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	wire "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	clientip "github.com/TokenFlux/TokenRouter/internal/server/clientip"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// Live 创建 ChatGPT Frameless Live 会话并返回 SDP 应答。
func (h *LiveHandler) Live(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
	if apiKey.Group == nil || apiKey.Group.Platform != "openai" {
		h.ports.Error(c, http.StatusNotFound, "not_found_error", "Live is not supported for this platform")
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

func ParseLiveCallRequest(c *gin.Context) (*session.LiveCallRequest, error) {
	contentType := strings.ToLower(c.GetHeader("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		sdp := c.PostForm("sdp")
		sessionBody := json.RawMessage(c.PostForm("session"))
		request := &session.LiveCallRequest{SDP: sdp, Session: sessionBody}
		if err := wire.ValidateLiveCallRequest(request); err != nil {
			return nil, err
		}
		return request, nil
	}
	var request session.LiveCallRequest
	decoder := json.NewDecoder(c.Request.Body)
	if err := decoder.Decode(&request); err != nil {
		return nil, errors.New("request body must be valid JSON")
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return nil, errors.New("request body must contain one JSON object")
	}
	if err := wire.ValidateLiveCallRequest(&request); err != nil {
		return nil, err
	}
	return &request, nil
}

func LiveSidebandLocation(fullPath, callID string) string {
	prefix := "/v1/live/"
	if strings.HasPrefix(fullPath, "/backend-api/codex/") {
		prefix = "/backend-api/codex/"
	}
	return prefix + url.PathEscape(callID)
}

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

func (h *LiveHandler) WriteLiveCreateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, session.ErrLiveConcurrencyFull):
		h.ports.Error(c, http.StatusTooManyRequests, "rate_limit_error", "Live concurrency limit reached")
	case errors.Is(err, session.ErrLiveClientPolicyDenied):
		h.ports.PolicyDenied(c)
		h.ports.Error(c, http.StatusForbidden, "permission_error", "Live client is not allowed by the available account policy")
	case errors.Is(err, session.ErrLiveUnavailable):
		h.ports.Error(c, http.StatusServiceUnavailable, "api_error", "Live is unavailable")
	default:
		var attestationErr *session.LiveAttestationUnavailableError
		if errors.As(err, &attestationErr) {
			h.ports.Error(c, http.StatusServiceUnavailable, "api_error", attestationErr.Error())
			return
		}
		if status := h.ports.UpstreamStatus(err); status >= 400 && status < 500 {
			h.ports.Error(c, status, "invalid_request_error", "Live upstream rejected the request")
			return
		}

		h.ports.Error(c, http.StatusBadGateway, "api_error", "Live upstream request failed")
	}
}

// LiveSideband 为已认证调用方代理 Live 控制 WebSocket。
func (h *LiveHandler) LiveSideband(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
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
	downstream, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer func() { _ = downstream.CloseNow() }()
	if err := h.ports.Proxy(c.Request.Context(), record, downstream); err != nil {
		_ = downstream.Close(coderws.StatusInternalError, "live sideband closed")
		return
	}
	_ = downstream.Close(coderws.StatusNormalClosure, "")
}

func liveEnabledForAPIKey(apiKey *LiveAPIKey) bool {
	return apiKey != nil &&
		apiKey.Group != nil &&
		apiKey.Group.Platform == "openai" &&
		apiKey.Group.AllowLive
}

// LiveAPIKey 是 HTTP 准入需要的只读投影，不持有旧身份或路由实体。
type LiveAPIKey struct {
	ID           int64
	UserID       int64
	TeamID       *int64
	GroupID      *int64
	Group        *LiveGroup
	ModelMapping map[string]string
}
type LiveGroup struct {
	Platform  string
	AllowLive bool
}
type LiveSubject struct {
	UserID      int64
	Concurrency int
}
type LiveSubscription struct{ ID int64 }

// LiveHTTPPorts 将认证、审核和资金准入绑定到应用唯一实例。
// 方法不会保存 Gin Context，异步执行只接收已固化的会话记录。
type LiveHTTPPorts interface {
	APIKey(*gin.Context) (*LiveAPIKey, bool)
	Subject(*gin.Context) (LiveSubject, bool)
	Subscription(*gin.Context) (*LiveSubscription, bool)
	Redirect(context.Context, *LiveAPIKey, string) (context.Context, string)
	Moderate(*gin.Context, *LiveAPIKey, LiveSubject, string, string, []byte) bool
	CheckBilling(*gin.Context) bool
	TryAcquireUserSlot(context.Context, int64, int) (func(), bool, error)
	InboundEndpoint(*gin.Context) string
	Create(context.Context, *session.LiveCallRequest, session.LiveCallIdentity, int) (*gatewaylive.Created, error)
	Lookup(context.Context, string, session.LiveCallIdentity) (*session.LiveCallRecord, error)
	Proxy(context.Context, *session.LiveCallRecord, *coderws.Conn) error
	Error(*gin.Context, int, string, string)
	PolicyDenied(*gin.Context)
	UpstreamStatus(error) int
}

// LiveHandler 独占 SDP、JSON、WebSocket 升级及错误响应。
type LiveHandler struct {
	requestLifetime
	ports LiveHTTPPorts
}

func NewLiveHandler(ports LiveHTTPPorts) *LiveHandler { return &LiveHandler{ports: ports} }

// cloneLiveModelMapping 保留旧 HTTP 身份投影的空对象语义。
func cloneLiveModelMapping(mapping map[string]string) map[string]string {
	result := make(map[string]string, len(mapping))
	maps.Copy(result, mapping)
	return result
}
