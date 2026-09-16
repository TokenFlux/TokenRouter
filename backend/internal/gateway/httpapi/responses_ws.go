package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"
)

// ResponsesWSOptions 只保留 HTTP 连接与首帧预算，不接收完整配置。
type ResponsesWSOptions struct {
	MaxIngressConnectionsPerAPIKey int
	ReadLimit                      int64
	FirstMessageTimeout            time.Duration
	MaxAccountSwitches             int
}

// ResponsesWSCall 是完成升级后交给依赖工厂的明确请求投影。
type ResponsesWSCall struct {
	Key                 *gatewayws.EntryKey
	Subject             gatewayws.EntrySubject
	Conn                *coderws.Conn
	Logger              *zap.Logger
	ClientIP, UserAgent string
}

// ResponsesWSBackend 提供认证投影及分解的单步端口，不以单一回调执行旧 handler。
type ResponsesWSBackend interface {
	Access(*gin.Context) (*gatewayws.EntryKey, bool)
	Transport(*gin.Context)
	Error(*gin.Context, int, string, string)
	Dependencies(*gin.Context, *zap.Logger) bool
	SummarizeRead(error) (string, string)
	Entry(*gin.Context, ResponsesWSCall) gatewayws.EntryPorts
}
type ResponsesWSHandler struct {
	requestLifetime

	options     ResponsesWSOptions
	backend     ResponsesWSBackend
	concurrency *ConcurrencyHelper
}

func NewResponsesWSHandler(options ResponsesWSOptions, backend ResponsesWSBackend, concurrency *ConcurrencyHelper) *ResponsesWSHandler {
	return &ResponsesWSHandler{options: options, backend: backend, concurrency: concurrency}
}

// ResponsesWebSocket 拥有前置认证、入站连接租约、升级及首帧读取，之后调用唯一核心编排。
func (h *ResponsesWSHandler) ResponsesWebSocket(c *gin.Context) {
	done, accepted := h.beginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	if !IsResponsesWSUpgrade(c.Request) {
		h.backend.Error(c, http.StatusUpgradeRequired, "invalid_request_error", "WebSocket upgrade required (Upgrade: websocket)")
		return
	}
	h.backend.Transport(c)

	apiKey, ok := h.backend.Access(c)
	if !ok {
		h.backend.Error(c, http.StatusUnauthorized, "authentication_error", "Invalid API key")
		return
	}
	subject, ok := authctx.GetAuthSubjectFromContext(c)
	if !ok {
		h.backend.Error(c, http.StatusInternalServerError, "api_error", "User context not found")
		return
	}

	reqLog := RequestLogger(
		c,
		"handler.openai_gateway.responses_ws",
		zap.Int64("user_id", subject.UserID),
		zap.Int64("api_key_id", apiKey.ID),
		zap.Any("group_id", apiKey.GroupID),
		zap.Bool("openai_ws_mode", true),
	)
	if !h.backend.Dependencies(c, reqLog) {
		return
	}
	reqLog.Info("openai.websocket_ingress_started")
	clientIP := clientip.GetClientIP(c)
	userAgent := strings.TrimSpace(c.GetHeader("User-Agent"))
	clientLifecycleCtx := c.Request.Context()
	ctx := clientLifecycleCtx
	maxIngressConnections := h.options.MaxIngressConnectionsPerAPIKey
	ingressLease, ingressLeaseAcquired, ingressLeaseErr := h.concurrency.AcquireOpenAIWSIngressLease(ctx, apiKey.ID, maxIngressConnections)
	if ingressLeaseErr != nil {
		reqLog.Error("openai.websocket_ingress_lease_acquire_failed", zap.Error(ingressLeaseErr))
		h.backend.Error(c, http.StatusServiceUnavailable, "service_unavailable", "WebSocket ingress capacity is temporarily unavailable")
		return
	}
	if !ingressLeaseAcquired {
		reqLog.Info("openai.websocket_ingress_capacity_rejected", zap.Int("max_ingress_connections_per_api_key", maxIngressConnections))
		c.Header("Retry-After", "5")
		h.backend.Error(c, http.StatusTooManyRequests, "rate_limit_error", "Too many open WebSocket connections, please retry later")
		return
	}
	if ingressLease != nil {
		defer ingressLease.Release()
		ctx = ingressLease.Context()
		c.Request = c.Request.WithContext(ctx)
	}

	wsConn, err := coderws.Accept(c.Writer, c.Request, &coderws.AcceptOptions{
		CompressionMode: coderws.CompressionContextTakeover,
	})
	if err != nil {
		reqLog.Warn("openai.websocket_accept_failed",
			zap.Error(err),
			zap.String("client_ip", clientIP),
			zap.String("request_user_agent", userAgent),
			zap.String("upgrade_header", strings.TrimSpace(c.GetHeader("Upgrade"))),
			zap.String("connection_header", strings.TrimSpace(c.GetHeader("Connection"))),
			zap.String("sec_websocket_version", strings.TrimSpace(c.GetHeader("Sec-WebSocket-Version"))),
			zap.Bool("has_sec_websocket_key", strings.TrimSpace(c.GetHeader("Sec-WebSocket-Key")) != ""),
		)
		return
	}
	defer func() {
		_ = wsConn.CloseNow()
	}()
	wsConn.SetReadLimit(h.options.ReadLimit)

	firstMessageTimeout := h.options.FirstMessageTimeout
	msgType, firstMessage, err := gatewayws.ReadClientMessage(
		ctx,
		WSClientFrames{Conn: wsConn},
		firstMessageTimeout,
		int(coderws.StatusPolicyViolation),
		"missing first response.create message",
	)
	if err != nil {
		if errors.Is(context.Cause(ctx), scheduler.ErrOpenAIWSIngressLeaseLost) {
			reqLog.Warn("openai.websocket_ingress_lease_lost_before_first_message", zap.Error(err))
			CloseResponsesWS(wsConn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
			return
		}
		closeStatus, closeReason := h.backend.SummarizeRead(err)
		reqLog.Warn("openai.websocket_read_first_message_failed",
			zap.Error(err),
			zap.String("client_ip", clientIP),
			zap.String("close_status", closeStatus),
			zap.String("close_reason", closeReason),
			zap.Duration("read_timeout", firstMessageTimeout),
		)
		CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "missing first response.create message")
		return
	}
	firstTurnStartedAt := time.Now()
	if msgType != int(coderws.MessageText) && msgType != int(coderws.MessageBinary) {
		CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "unsupported websocket message type")
		return
	}
	if !gjson.ValidBytes(firstMessage) {
		CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "invalid JSON payload")
		return
	}

	subjectView := gatewayws.EntrySubject{UserID: subject.UserID, Concurrency: subject.Concurrency}
	ports := h.backend.Entry(c, ResponsesWSCall{Key: apiKey, Subject: subjectView, Conn: wsConn, Logger: reqLog, ClientIP: clientIP, UserAgent: userAgent})
	gatewayws.RunEntry(ctx, ports, gatewayws.EntryInput{Key: apiKey, Subject: subjectView, ClientLifecycleContext: clientLifecycleCtx, FirstTurnStartedAt: firstTurnStartedAt, ClientIP: clientIP, UserAgent: userAgent, MaxAccountSwitches: h.options.MaxAccountSwitches}, WSClientFrames{Conn: wsConn}, firstMessage)
}

// IsResponsesWSUpgrade 保留原 Upgrade 和 Connection 判断。
func IsResponsesWSUpgrade(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	return strings.Contains(strings.ToLower(strings.TrimSpace(r.Header.Get("Connection"))), "upgrade")
}

// CloseResponsesWS 保留原字节截断和关闭顺序，不顺带修改关闭码或文本行为。
func CloseResponsesWS(conn *coderws.Conn, status coderws.StatusCode, reason string) {
	if conn == nil {
		return
	}
	reason = strings.TrimSpace(reason)
	if len(reason) > 120 {
		reason = reason[:120]
	}
	_ = conn.Close(status, reason)
	_ = conn.CloseNow()
}
