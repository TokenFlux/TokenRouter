package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"go.uber.org/zap"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/protocol/wirejson"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
)

// responses_ws.go upgrades authenticated Responses requests and hands
// validated first frames to session entry.
// An authenticated HTTP request reserves an ingress connection allowance
// before upgrading to Responses WebSocket. The allowance can renew
// independently of the client transport and cancel the control context when
// ownership is lost. Runtime settings are captured at entry so connection
// limits, read limits, and the first-message budget remain fixed for that
// connection.
//
// The original client context is retained separately from the lease context
// and passed to the turn loop for downstream delivery. First-frame validation
// finishes before RunEntry selects a provider. Deferred transport cleanup and
// lease release cover every return after acquisition, while the request
// lifetime barrier stays active through the complete session.

// ResponsesWSOptions supplies entry limits and the provider-switch budget.
// Runtime, when supplied, replaces connection and first-frame values at upgrade.
type ResponsesWSOptions struct {
	ForceCodexCLI bool // configured Codex client preparation
	// Runtime is optional; its snapshot overrides MaxIngressConnectionsPerAPIKey,
	// ReadLimit, and FirstMessageTimeout before capacity acquisition. The static
	// MaxProviderSwitches value continues into RunEntry independently of Runtime.
	Runtime                        *gatewayws.Runtime // entry parameter snapshot source
	MaxIngressConnectionsPerAPIKey int                // maximum accepted connections per Key
	ReadLimit                      int64              // bytes
	FirstMessageTimeout            time.Duration      // initial client frame budget
	MaxProviderSwitches            int                // entry provider switch budget
}

// ResponsesWSCall passes authenticated entry metadata and the upgraded transport
// to the backend that constructs per-session operations for RunEntry.
type ResponsesWSCall struct {
	Key     *gatewayws.EntryKey    // authenticated entry Key view
	Subject gatewayws.EntrySubject // authenticated user concurrency data
	// Conn remains owned by ResponsesWebSocket, which closes it when RunEntry
	// returns. Entry operations may read, write, or send close instructions meanwhile.
	Conn   *coderws.Conn
	Logger *zap.Logger // request-scoped session diagnostic logger
	// ClientIP: resolved client IP. UserAgent: trimmed User-Agent header.
	ClientIP, UserAgent string
}

// ResponsesWSBackend connects HTTP authentication, diagnostics, and entry construction
// to the handler before and after the WebSocket upgrade.
type ResponsesWSBackend interface {
	// Access reads the Key already authenticated by request middleware. False
	// rejects the request before upgrade; the returned entry view supplies the Key
	// ID, group, and policy data used by RunEntry.
	Access(*gin.Context) (*gatewayws.EntryKey, bool)
	// Transport records the inbound WebSocket transport for request diagnostics
	// before upgrade.
	Transport(*gin.Context)
	// Error writes an OpenAI HTTP error before the WebSocket upgrade. Calls use
	// the supplied status, kind, and message; once upgraded, the handler presents
	// failures through close frames instead.
	Error(*gin.Context, int, string, string)
	// Dependencies checks whether the injected services needed by this entry are
	// available. False means it has already handled the HTTP failure, so the
	// handler returns before capacity acquisition and upgrade.
	Dependencies(*gin.Context, *zap.Logger) bool
	// SummarizeRead extracts printable close status and reason labels from a
	// first-frame failure.
	SummarizeRead(error) (string, string)
	// Entry constructs operations for the authenticated, upgraded session and its
	// captured client metadata. The handler retains connection cleanup ownership
	// and invokes RunEntry with the resulting operations and first frame.
	Entry(*gin.Context, ResponsesWSCall) gatewayws.EntryPorts
}

// ResponsesWSHandler owns upgrade and ingress-lease cleanup around the gateway
// entry loop for each accepted HTTP request.
type ResponsesWSHandler struct {
	gatewayhttp.RequestLifetime // application request lifetime barrier

	options     ResponsesWSOptions             // constructor supplied entry limits
	backend     ResponsesWSBackend             // HTTP and session entry operations
	concurrency *gatewayhttp.ConcurrencyHelper // ingress capacity lease provider
}

// NewResponsesWSHandler retains options and the required backend and
// concurrency services. It constructs no transports or leases; callers bind
// RequestLifetime before serving requests when shutdown tracking is required.
func NewResponsesWSHandler(options ResponsesWSOptions, backend ResponsesWSBackend, concurrency *gatewayhttp.ConcurrencyHelper) *ResponsesWSHandler {
	return &ResponsesWSHandler{options: options, backend: backend, concurrency: concurrency}
}

// ResponsesWebSocket upgrades an authenticated request and runs its first
// validated frame through RunEntry. Upgrade headers, Key and subject data,
// dependencies, and ingress capacity are checked in that order before the
// transport is accepted. The request barrier, lease, and client transport are
// released when the session returns.
//
// The lease context controls session admission and closure, while
// ClientLifecycleContext retains the original request lifetime for downstream
// writes. A lost lease before the first message sends a reconnect close; other
// first-read or malformed-frame failures use policy closure. Provider
// switching begins only after a valid text or binary JSON frame has been
// received.
func (h *ResponsesWSHandler) ResponsesWebSocket(c *gin.Context) {
	done, accepted := h.BeginRequest(c, "openai")
	if !accepted {
		return
	}
	defer done()

	if !gatewayhttp.IsResponsesWSUpgrade(c.Request) {
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

	reqLog := gatewayhttp.RequestLogger(
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
	options := h.options
	if options.Runtime != nil {
		value := options.Runtime.Snapshot()
		options.MaxIngressConnectionsPerAPIKey = value.MaxIngressConnectionsPerAPIKey
		options.ReadLimit = value.ClientReadLimitBytes
		// Freeze the configured seconds as this connection's initial read timer,
		// before upgrade so later runtime updates apply to newly accepted connections.
		options.FirstMessageTimeout = time.Duration(value.ClientFirstMessageTimeoutSeconds) * time.Second
	}
	maxIngressConnections := options.MaxIngressConnectionsPerAPIKey
	ingressLease, ingressLeaseAcquired, ingressLeaseErr := h.concurrency.AcquireOpenAIWSIngressLease(ctx, apiKey.ID, maxIngressConnections)
	if ingressLeaseErr != nil {
		reqLog.Error("openai.websocket_ingress_lease_acquire_failed", zap.Error(ingressLeaseErr))
		h.backend.Error(c, http.StatusServiceUnavailable, "service_unavailable", "WebSocket ingress capacity is temporarily unavailable")
		return
	}
	if !ingressLeaseAcquired {
		reqLog.Info("openai.websocket_ingress_capacity_rejected", zap.Int("max_ingress_connections_per_api_key", maxIngressConnections))
		// Tell capacity-rejected clients to wait five seconds before reconnecting.
		c.Header("Retry-After", "5")
		h.backend.Error(c, http.StatusTooManyRequests, "rate_limit_error", "Too many open WebSocket connections, please retry later")
		return
	}
	if ingressLease != nil {
		// Keep capacity renewal active for the whole session and release it on every
		// exit; a lost lease cancels admission through its replacement request context.
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
	// Transport teardown follows any explicit close presentation and also covers
	// entry-loop failures, ensuring the ingress lease can return after this session.
	defer func() {
		_ = wsConn.CloseNow()
	}()
	wsConn.SetReadLimit(options.ReadLimit)

	firstMessageTimeout := options.FirstMessageTimeout
	msgType, firstMessage, err := gatewayws.ReadClientMessage(
		ctx,
		gatewayhttp.WSClientFrames{Conn: wsConn},
		firstMessageTimeout,
		int(coderws.StatusPolicyViolation),
		"missing first response.create message",
	)
	if err != nil {
		if errors.Is(context.Cause(ctx), scheduler.ErrOpenAIWSIngressLeaseLost) {
			reqLog.Warn("openai.websocket_ingress_lease_lost_before_first_message", zap.Error(err))
			gatewayhttp.CloseResponsesWS(wsConn, coderws.StatusTryAgainLater, "websocket ingress capacity lease lost; please reconnect")
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
		gatewayhttp.CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "missing first response.create message")
		return
	}
	firstTurnStartedAt := time.Now()
	if msgType != int(coderws.MessageText) && msgType != int(coderws.MessageBinary) {
		gatewayhttp.CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "unsupported websocket message type")
		return
	}
	if !gjson.ValidBytes(firstMessage) || wirejson.ValidateRequestObject(firstMessage) != nil {
		gatewayhttp.CloseResponsesWS(wsConn, coderws.StatusPolicyViolation, "invalid JSON payload")
		return
	}

	subjectView := gatewayws.EntrySubject{UserID: subject.UserID, Concurrency: subject.Concurrency}
	ports := h.backend.Entry(c, ResponsesWSCall{Key: apiKey, Subject: subjectView, Conn: wsConn, Logger: reqLog, ClientIP: clientIP, UserAgent: userAgent})
	gatewayws.RunEntry(ctx, ports, gatewayws.EntryInput{Key: apiKey, Subject: subjectView, ClientLifecycleContext: clientLifecycleCtx, FirstTurnStartedAt: firstTurnStartedAt, ClientIP: clientIP, UserAgent: userAgent, Codex: openai.IsCodexOfficialClientByHeaders(c.GetHeader("User-Agent"), c.GetHeader("originator")) || h.options.ForceCodexCLI, MaxProviderSwitches: h.options.MaxProviderSwitches}, gatewayhttp.WSClientFrames{Conn: wsConn}, firstMessage)
}
