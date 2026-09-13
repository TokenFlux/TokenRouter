// HTTP 负责握手、Origin 与帧，实时运行资源由 Ops 拥有。
package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

type OpsWSProxyConfig struct {
	TrustProxy     bool
	TrustedProxies []netip.Prefix
	OriginPolicy   string
}

const (
	envOpsWSTrustProxy     = "OPS_WS_TRUST_PROXY"
	envOpsWSTrustedProxies = "OPS_WS_TRUSTED_PROXIES"
	envOpsWSOriginPolicy   = "OPS_WS_ORIGIN_POLICY"
	envOpsWSMaxConns       = "OPS_WS_MAX_CONNS"
	envOpsWSMaxConnsPerIP  = "OPS_WS_MAX_CONNS_PER_IP"
)

const (
	OriginPolicyStrict     = "strict"
	OriginPolicyPermissive = "permissive"
)

var opsWSProxyConfig = loadOpsWSProxyConfigFromEnv()

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return isAllowedOpsWSOrigin(r)
	},
	// Subprotocol negotiation:
	// - The frontend passes ["sub2api-admin", "jwt.<token>"].
	// - We always select "sub2api-admin" so the token is never echoed back in the handshake response.
	Subprotocols: []string{"sub2api-admin"},
}

const (
	qpsWSPushInterval       = 2 * time.Second
	qpsWSRefreshInterval    = 5 * time.Second
	qpsWSRequestCountWindow = 1 * time.Minute

	defaultMaxWSConns      = 100
	defaultMaxWSConnsPerIP = 20
)

const (
	opsWSCloseRealtimeDisabled = 4001
)

type opsWSRuntimeLimits struct {
	MaxConns      int32
	MaxConnsPerIP int32
}

var opsWSLimits = loadOpsWSRuntimeLimitsFromEnv()

const (
	qpsWSWriteTimeout = 10 * time.Second
	qpsWSPongWait     = 60 * time.Second
	qpsWSPingInterval = 30 * time.Second

	// We don't expect clients to send application messages; we only read to process control frames (Pong/Close).
	qpsWSMaxReadBytes = 1024
)

func closeWS(conn *websocket.Conn, code int, reason string) {
	if conn == nil {
		return
	}
	msg := websocket.FormatCloseMessage(code, reason)
	_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(qpsWSWriteTimeout))
	_ = conn.Close()
}

// QPSWSHandler handles realtime QPS push via WebSocket.
// GET /api/v1/admin/ops/ws/qps
func (h *OpsHandler) QPSWSHandler(c *gin.Context) {
	clientIP := requestClientIP(c.Request)

	if h == nil || h.opsService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ops service not initialized"})
		return
	}

	// If realtime monitoring is disabled, prefer a successful WS upgrade followed by a clean close
	// with a deterministic close code. This prevents clients from spinning on 404/1006 reconnect loops.
	if !h.opsService.IsRealtimeMonitoringEnabled(c.Request.Context()) {
		conn, err := upgrader.Upgrade(c.Writer, c.Request, servermiddleware.ServerTimingResponseHeader(c))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "ops realtime monitoring is disabled"})
			return
		}
		closeWS(conn, opsWSCloseRealtimeDisabled, "realtime_disabled")
		return
	}

	rt := h.opsService.Realtime()
	// Lazily start the background refresh loop so unit tests that never hit the
	// websocket route don't spawn goroutines that depend on DB/Redis stubs.
	rt.Prepare(h.opsService)

	// Reserve a global slot before upgrading the connection to keep the limit strict.
	if !rt.AcquireTotal(opsWSLimits.MaxConns) {
		logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] connection limit reached: %d/%d", rt.Total(), opsWSLimits.MaxConns)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many connections"})
		return
	}
	defer func() {
		rt.ReleaseTotal()
	}()

	if opsWSLimits.MaxConnsPerIP > 0 && clientIP != "" {
		if !rt.AcquireIP(clientIP, opsWSLimits.MaxConnsPerIP) {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] per-ip connection limit reached: ip=%s limit=%d", clientIP, opsWSLimits.MaxConnsPerIP)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many connections"})
			return
		}
		defer rt.ReleaseIP(clientIP)
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, servermiddleware.ServerTimingResponseHeader(c))
	if err != nil {
		logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] upgrade failed: %v", err)
		return
	}

	defer func() {
		_ = conn.Close()
	}()

	handleQPSWebSocket(c.Request.Context(), conn, rt)
}

func handleQPSWebSocket(parentCtx context.Context, conn *websocket.Conn, rt *ops.RealtimeRuntime) {
	if conn == nil {
		return
	}

	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	var closeOnce sync.Once
	closeConn := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
		})
	}

	closeFrameCh := make(chan []byte, 1)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer cancel()

		conn.SetReadLimit(qpsWSMaxReadBytes)
		if err := conn.SetReadDeadline(time.Now().Add(qpsWSPongWait)); err != nil {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] set read deadline failed: %v", err)
			return
		}
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(qpsWSPongWait))
		})
		conn.SetCloseHandler(func(code int, text string) error {
			select {
			case closeFrameCh <- websocket.FormatCloseMessage(code, text):
			default:
			}
			cancel()
			return nil
		})

		for {
			_, _, err := conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] read failed: %v", err)
				}
				return
			}
		}
	}()

	// Push QPS data every 2 seconds (values are globally cached and refreshed at most once per qpsWSRefreshInterval).
	pushTicker := time.NewTicker(qpsWSPushInterval)
	defer pushTicker.Stop()

	// Heartbeat ping every 30 seconds.
	pingTicker := time.NewTicker(qpsWSPingInterval)
	defer pingTicker.Stop()

	writeWithTimeout := func(messageType int, data []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(qpsWSWriteTimeout)); err != nil {
			return err
		}
		return conn.WriteMessage(messageType, data)
	}

	sendClose := func(closeFrame []byte) {
		if closeFrame == nil {
			closeFrame = websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")
		}
		_ = writeWithTimeout(websocket.CloseMessage, closeFrame)
	}

	for {
		select {
		case <-pushTicker.C:
			msg := rt.Payload()
			if msg == nil {
				continue
			}
			if err := writeWithTimeout(websocket.TextMessage, msg); err != nil {
				logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] write failed: %v", err)
				cancel()
				closeConn()
				wg.Wait()
				return
			}

		case <-pingTicker.C:
			if err := writeWithTimeout(websocket.PingMessage, nil); err != nil {
				logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] ping failed: %v", err)
				cancel()
				closeConn()
				wg.Wait()
				return
			}

		case closeFrame := <-closeFrameCh:
			sendClose(closeFrame)
			closeConn()
			wg.Wait()
			return

		case <-ctx.Done():
			var closeFrame []byte
			select {
			case closeFrame = <-closeFrameCh:
			default:
			}
			sendClose(closeFrame)

			closeConn()
			wg.Wait()
			return
		}
	}
}

func isAllowedOpsWSOrigin(r *http.Request) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		switch strings.ToLower(strings.TrimSpace(opsWSProxyConfig.OriginPolicy)) {
		case OriginPolicyStrict:
			return false
		case OriginPolicyPermissive, "":
			return true
		default:
			return true
		}
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Hostname() == "" {
		return false
	}
	originHost := strings.ToLower(parsed.Hostname())

	trustProxyHeaders := shouldTrustOpsWSProxyHeaders(r)
	reqHost := hostWithoutPort(r.Host)
	if trustProxyHeaders {
		xfHost := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
		if xfHost != "" {
			xfHost = strings.TrimSpace(strings.Split(xfHost, ",")[0])
			if xfHost != "" {
				reqHost = hostWithoutPort(xfHost)
			}
		}
	}
	reqHost = strings.ToLower(reqHost)
	if reqHost == "" {
		return false
	}
	return originHost == reqHost
}

func shouldTrustOpsWSProxyHeaders(r *http.Request) bool {
	if r == nil {
		return false
	}
	if !opsWSProxyConfig.TrustProxy {
		return false
	}
	peerIP, ok := requestPeerIP(r)
	if !ok {
		return false
	}
	return isAddrInTrustedProxies(peerIP, opsWSProxyConfig.TrustedProxies)
}

func requestPeerIP(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	if err != nil {
		host = strings.TrimSpace(r.RemoteAddr)
	}
	host = strings.TrimPrefix(host, "[")
	host = strings.TrimSuffix(host, "]")
	if host == "" {
		return netip.Addr{}, false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.Unmap(), true
}

func requestClientIP(r *http.Request) string {
	if r == nil {
		return ""
	}

	trustProxyHeaders := shouldTrustOpsWSProxyHeaders(r)
	if trustProxyHeaders {
		xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if xff != "" {
			// Use the left-most entry (original client). If multiple proxies add values, they are comma-separated.
			xff = strings.TrimSpace(strings.Split(xff, ",")[0])
			xff = strings.TrimPrefix(xff, "[")
			xff = strings.TrimSuffix(xff, "]")
			if addr, err := netip.ParseAddr(xff); err == nil && addr.IsValid() {
				return addr.Unmap().String()
			}
		}
	}

	if peer, ok := requestPeerIP(r); ok && peer.IsValid() {
		return peer.String()
	}
	return ""
}

func isAddrInTrustedProxies(addr netip.Addr, trusted []netip.Prefix) bool {
	if !addr.IsValid() {
		return false
	}
	for _, p := range trusted {
		if p.Contains(addr) {
			return true
		}
	}
	return false
}

func loadOpsWSProxyConfigFromEnv() OpsWSProxyConfig {
	cfg := OpsWSProxyConfig{
		TrustProxy:     true,
		TrustedProxies: defaultTrustedProxies(),
		OriginPolicy:   OriginPolicyPermissive,
	}

	if v := strings.TrimSpace(os.Getenv(envOpsWSTrustProxy)); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			cfg.TrustProxy = parsed
		} else {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] invalid %s=%q (expected bool); using default=%v", envOpsWSTrustProxy, v, cfg.TrustProxy)
		}
	}

	if raw := strings.TrimSpace(os.Getenv(envOpsWSTrustedProxies)); raw != "" {
		prefixes, invalid := parseTrustedProxyList(raw)
		if len(invalid) > 0 {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] invalid %s entries ignored: %s", envOpsWSTrustedProxies, strings.Join(invalid, ", "))
		}
		cfg.TrustedProxies = prefixes
	}

	if v := strings.TrimSpace(os.Getenv(envOpsWSOriginPolicy)); v != "" {
		normalized := strings.ToLower(v)
		switch normalized {
		case OriginPolicyStrict, OriginPolicyPermissive:
			cfg.OriginPolicy = normalized
		default:
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] invalid %s=%q (expected %q or %q); using default=%q", envOpsWSOriginPolicy, v, OriginPolicyStrict, OriginPolicyPermissive, cfg.OriginPolicy)
		}
	}

	return cfg
}

func loadOpsWSRuntimeLimitsFromEnv() opsWSRuntimeLimits {
	cfg := opsWSRuntimeLimits{
		MaxConns:      defaultMaxWSConns,
		MaxConnsPerIP: defaultMaxWSConnsPerIP,
	}

	if v := strings.TrimSpace(os.Getenv(envOpsWSMaxConns)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			cfg.MaxConns = int32(parsed)
		} else {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] invalid %s=%q (expected int>0); using default=%d", envOpsWSMaxConns, v, cfg.MaxConns)
		}
	}
	if v := strings.TrimSpace(os.Getenv(envOpsWSMaxConnsPerIP)); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed >= 0 {
			cfg.MaxConnsPerIP = int32(parsed)
		} else {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] invalid %s=%q (expected int>=0); using default=%d", envOpsWSMaxConnsPerIP, v, cfg.MaxConnsPerIP)
		}
	}
	return cfg
}

func defaultTrustedProxies() []netip.Prefix {
	prefixes, _ := parseTrustedProxyList("127.0.0.0/8,::1/128")
	return prefixes
}

func parseTrustedProxyList(raw string) (prefixes []netip.Prefix, invalid []string) {
	for _, token := range strings.Split(raw, ",") {
		item := strings.TrimSpace(token)
		if item == "" {
			continue
		}

		var (
			p   netip.Prefix
			err error
		)
		if strings.Contains(item, "/") {
			p, err = netip.ParsePrefix(item)
		} else {
			var addr netip.Addr
			addr, err = netip.ParseAddr(item)
			if err == nil {
				addr = addr.Unmap()
				bits := 128
				if addr.Is4() {
					bits = 32
				}
				p = netip.PrefixFrom(addr, bits)
			}
		}

		if err != nil || !p.IsValid() {
			invalid = append(invalid, item)
			continue
		}

		prefixes = append(prefixes, p.Masked())
	}
	return prefixes, invalid
}

func hostWithoutPort(hostport string) string {
	hostport = strings.TrimSpace(hostport)
	if hostport == "" {
		return ""
	}
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	if strings.HasPrefix(hostport, "[") && strings.HasSuffix(hostport, "]") {
		return strings.Trim(hostport, "[]")
	}
	parts := strings.Split(hostport, ":")
	return parts[0]
}
