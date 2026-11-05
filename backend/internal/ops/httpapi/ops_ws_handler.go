package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// ops_ws_handler.go admits Ops WebSocket connections and streams cached traffic
// samples under the administrator route middleware.
// Ops WebSockets send the shared runtime's cached traffic payload and use a
// reader to process control frames while discarding inbound application
// messages. Environment-loaded policy trusts forwarded host and client
// addresses only when the direct peer matches configured prefixes. A present
// Origin is compared by hostname alone; strict mode additionally rejects a
// missing Origin, while permissive mode accepts it.
//
// Runtime preparation precedes connection admission, and positive limits
// reserve global and optional per-address counts around upgrade and socket
// handling. Environment integers are narrowed without an int32 range check, so
// their sign can change before admission reads them. The socket loop serializes
// data, ping, and close writes, cancels on reader failure, and closes the
// connection before joining its reader. Upgrade receives an explicit
// timing-header map; Gorilla constructs its 101 response from that map rather
// than the response writer's ordinary Header map.

// These origin modes, close codes, and frame timing budgets
// configure the Ops WebSocket handshake and ongoing socket loop.
const (
	OriginPolicyStrict     = "strict"     // reject a missing Origin
	OriginPolicyPermissive = "permissive" // accept a missing Origin

	qpsWSPushInterval = 2 * time.Second // cached payload push ticker interval

	opsWSCloseRealtimeDisabled = 4001 // realtime-disabled application close code

	qpsWSWriteTimeout = 10 * time.Second // individual write deadline interval
	qpsWSPongWait     = 60 * time.Second // read deadline renewed by pong
	qpsWSPingInterval = 30 * time.Second // heartbeat ping ticker interval

	// qpsWSMaxReadBytes bounds inbound application messages even though the reader
	// discards their contents while processing control frames.
	qpsWSMaxReadBytes = 1024
)

// OpsWSProxyConfig describes trusted direct peers and missing-Origin handling
// for administrator sockets.
type OpsWSProxyConfig struct {
	// TrustProxy and TrustedProxies jointly permit forwarded headers only for a
	// valid direct peer inside a prefix. A disabled switch or empty prefix list
	// selects the direct request values.
	TrustProxy bool // forwarded-header trust enable flag
	// TrustedProxies is retained as a shared prefix slice. The parser masks CIDRs
	// and unmaps single addresses; request peers are unmapped before membership
	// testing.
	TrustedProxies []netip.Prefix // trusted direct-peer network prefixes
	// OriginPolicy affects a blank Origin: strict rejects it, while permissive,
	// blank, or unknown values allow it. Present origins use hostname comparison
	// under every mode.
	OriginPolicy string // missing-Origin policy mode
}

// opsWSRuntimeLimits carries narrowed connection ceilings consumed by realtime
// runtime admission.
type opsWSRuntimeLimits struct {
	// MaxConns is narrowed to int32 after native-integer parsing. Positive values
	// reserve a global slot; nonpositive runtime values bypass that reservation
	// while the handler still calls ReleaseTotal.
	MaxConns int32 // global admission count limit
	// MaxConnsPerIP enables per-address admission only when positive and client IP
	// is known. Zero or a negative narrowed value skips that admission path.
	MaxConnsPerIP int32 // per-address admission count limit
}

// OpsWSOptions supplies an instance's handshake policy and admission ceilings.
// Configuration validates addresses and integer ranges before construction;
// tests can supply isolated policies without changing process-global state.
type OpsWSOptions struct {
	TrustProxy     bool     // forwarded peer trust switch
	TrustedProxies []string // trusted peer address prefixes
	OriginPolicy   string   // missing Origin header policy
	MaxConns       int      // global connection admission ceiling
	MaxConnsPerIP  int      // per-address connection admission ceiling
}

// closeWS attempts a formatted close control frame under the write deadline,
// then closes the socket. A nil connection is ignored, and frame-write or close
// errors are discarded because callers are ending this connection.
func closeWS(conn *websocket.Conn, code int, reason string) {
	if conn == nil {
		return
	}
	// Attempt the close control write before releasing the socket, ignoring either
	// failure because this path finishes the disabled connection.
	msg := websocket.FormatCloseMessage(code, reason)
	_ = conn.WriteControl(websocket.CloseMessage, msg, time.Now().Add(qpsWSWriteTimeout))
	_ = conn.Close()
}

// QPSWSHandler admits an enabled realtime socket and serves the shared cached
// traffic payload. It requires a usable Gin request before checking a nil
// handler or service. Route middleware supplies administrator authentication.
//
// Disabled realtime attempts an upgrade followed by application close 4001.
// Gorilla can already write a handshake error before that failure branch
// attempts its JSON 404 response. Enabled requests prepare the runtime before
// reserving counts, so a refused admission can still start or keep its sampler
// active.
//
// Positive global and known-address limits are reserved before upgrade and
// released by deferred handler cleanup after socket handling returns.
// Nonpositive global limits bypass reservation although ReleaseTotal still
// runs. The explicit upgrade headers contain available Server-Timing only;
// ordinary writer Header mutations during Hijack do not add other headers to
// Gorilla's 101 serialization.
// GET /api/v1/admin/ops/ws/qps
func (h *OpsHandler) QPSWSHandler(c *gin.Context) {
	clientIP := requestClientIP(c.Request, h.wsProxy)

	if h == nil || h.opsService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "ops service not initialized"})
		return
	}

	// Attempt the disabled-state close through an upgraded socket so clients can
	// receive the application code; failed upgrades may already have committed
	// Gorilla's error response.
	if !h.opsService.IsRealtimeMonitoringEnabled(c.Request.Context()) {
		conn, err := h.wsUpgrader.Upgrade(c.Writer, c.Request, servermiddleware.ServerTimingResponseHeader(c))
		if err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "ops realtime monitoring is disabled"})
			return
		}
		closeWS(conn, opsWSCloseRealtimeDisabled, "realtime_disabled")
		return
	}

	rt := h.opsService.Realtime()
	// Prepare starts sampling before admission and cancels pending idle stopping.
	// A later admission refusal does not undo this preparation here.
	rt.Prepare(h.opsService)

	// For positive ceilings, reserve before upgrade so connecting requests consume
	// capacity; deferred release covers upgrade failure and the completed socket
	// loop.
	if !rt.AcquireTotal(h.wsLimits.MaxConns) {
		logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] connection limit reached: %d/%d", rt.Total(), h.wsLimits.MaxConns)
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many connections"})
		return
	}
	defer func() {
		rt.ReleaseTotal()
	}()

	// Reserve the known address after global admission; refusal exits through the
	// already registered global release, while absent address skips this limit.
	if h.wsLimits.MaxConnsPerIP > 0 && clientIP != "" {
		if !rt.AcquireIP(clientIP, h.wsLimits.MaxConnsPerIP) {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] per-ip connection limit reached: ip=%s limit=%d", clientIP, h.wsLimits.MaxConnsPerIP)
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "too many connections"})
			return
		}
		defer rt.ReleaseIP(clientIP)
	}

	// Pass the explicit timing-header map to Gorilla, which constructs 101 after
	// Hijack from these supplied headers rather than the writer's ordinary header
	// map.
	conn, err := h.wsUpgrader.Upgrade(c.Writer, c.Request, servermiddleware.ServerTimingResponseHeader(c))
	if err != nil {
		logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] upgrade failed: %v", err)
		return
	}

	// Close the upgraded connection again on handler exit as final cleanup;
	// handleQPSWebSocket has already joined its own reader before returning.
	defer func() {
		_ = conn.Close()
	}()

	handleQPSWebSocket(c.Request.Context(), conn, rt)
}

// handleQPSWebSocket pushes cached payloads and pings while one reader handles
// control frames and discards inbound data. A nil connection is ignored;
// otherwise parentCtx and the realtime runtime are required.
//
// Pongs extend the read deadline, and a peer close queues a best-effort echo
// before canceling the loop. The main loop serializes text, ping, and close
// writes with a new deadline for each. Reader errors cancel it, and selected
// write failures cancel and close immediately. Cleanup closes the socket before
// joining the reader. Context cancellation can race with the close-frame queue,
// so the available peer frame is used when observed and otherwise a
// normal-close frame is attempted.
func handleQPSWebSocket(parentCtx context.Context, conn *websocket.Conn, rt *ops.RealtimeRuntime) {
	if conn == nil {
		return
	}

	// Use one cancellation lifetime for the reader and writer loop, then close the
	// connection before waiting so a blocked read can finish.
	ctx, cancel := context.WithCancel(parentCtx)
	defer cancel()

	// Combine selected shutdown paths through one socket close; reader completion
	// remains a separate join after closing and any final-frame attempt.
	var closeOnce sync.Once
	closeConn := func() {
		closeOnce.Do(func() {
			_ = conn.Close()
		})
	}

	closeFrameCh := make(chan []byte, 1)

	var wg sync.WaitGroup
	// Keep control-frame processing in one reader goroutine; its deferred
	// cancellation notifies the writer loop when reads or deadline setup fail.
	wg.Go(func() {
		defer cancel()

		// Bound inbound application messages while discarding their content;
		// continued reads are still needed for Gorilla to dispatch ping, pong, and
		// close controls.
		conn.SetReadLimit(qpsWSMaxReadBytes)
		if err := conn.SetReadDeadline(time.Now().Add(qpsWSPongWait)); err != nil {
			logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] set read deadline failed: %v", err)
			return
		}
		// Extend read readiness after a pong so a responding peer can remain
		// connected across the periodic heartbeat intervals.
		conn.SetPongHandler(func(string) error {
			return conn.SetReadDeadline(time.Now().Add(qpsWSPongWait))
		})
		// Queue a peer close frame without blocking the reader, then cancel; the main
		// writer can echo an available frame through its serialized write path.
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
				// Suppress diagnostics for expected closure codes while ending the reader
				// for every read error; its deferred cancellation still wakes the writer.
				if websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseNoStatusReceived) {
					logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] read failed: %v", err)
				}
				return
			}
		}
	})

	// Read the shared cached payload on push ticks; the socket does not run its
	// own SQL refresh or wait for a missing payload.
	pushTicker := time.NewTicker(qpsWSPushInterval)
	defer pushTicker.Stop()

	// Schedule ping controls independently of payload availability so a responding
	// peer can renew its pong-driven read deadline even while the cache is empty.
	pingTicker := time.NewTicker(qpsWSPingInterval)
	defer pingTicker.Stop()

	// Serialize application writes in this loop and set a new deadline for each;
	// Gorilla permits its reader's control writes concurrently.
	writeWithTimeout := func(messageType int, data []byte) error {
		if err := conn.SetWriteDeadline(time.Now().Add(qpsWSWriteTimeout)); err != nil {
			return err
		}
		return conn.WriteMessage(messageType, data)
	}

	// Use a normal close when no peer frame is available, and ignore the
	// close-write error because socket closure and reader joining follow.
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
				// Cancel, close, and join after a payload-write failure so this handler
				// cannot release admission counts while its reader still runs.
				cancel()
				closeConn()
				wg.Wait()
				return
			}

		case <-pingTicker.C:
			// A ping failure follows the same cancellation, socket-close, and
			// reader-join sequence as a failed payload write.
			if err := writeWithTimeout(websocket.PingMessage, nil); err != nil {
				logger.LegacyPrintf("handler.admin.ops_ws", "[OpsWS] ping failed: %v", err)
				cancel()
				closeConn()
				wg.Wait()
				return
			}

		// Echo the queued peer frame before closing and joining the reader; the
		// single writer owns the final application write.
		case closeFrame := <-closeFrameCh:
			sendClose(closeFrame)
			closeConn()
			wg.Wait()
			return

		// On cancellation, consume a peer frame only when already available, then
		// attempt closure and join; racing signals can select the normal close.
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

// isAllowedOpsWSOrigin compares a supplied Origin hostname with the effective
// request hostname. A nil request is rejected. Missing Origin uses
// strict-versus-permissive policy, and direct assignments of unknown modes
// allow that missing header.
//
// A trusted direct peer can replace request Host with the nonblank first
// X-Forwarded-Host component. Both hostnames are case-folded, while scheme and
// port are excluded from this comparison. The helper parses Origin as a URL but
// imposes no scheme or userinfo check. An empty effective hostname rejects the
// request, and hostWithoutPort supplies its separate textual fallback rules.
func isAllowedOpsWSOrigin(r *http.Request, policy OpsWSProxyConfig) bool {
	if r == nil {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		switch strings.ToLower(strings.TrimSpace(policy.OriginPolicy)) {
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

	trustProxyHeaders := shouldTrustOpsWSProxyHeaders(r, policy)
	reqHost := hostWithoutPort(r.Host)
	// A matching direct-peer policy permits the first forwarded host to override
	// Host; absent or blank first components keep the direct request host.
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

// shouldTrustOpsWSProxyHeaders requires the trust switch and a valid direct
// peer inside the configured prefix slice. Nil requests, malformed peers,
// disabled trust, or no matching prefix reject forwarded-header use.
func shouldTrustOpsWSProxyHeaders(r *http.Request, policy OpsWSProxyConfig) bool {
	if r == nil {
		return false
	}
	if !policy.TrustProxy {
		return false
	}
	peerIP, ok := requestPeerIP(r)
	if !ok {
		return false
	}
	return isAddrInTrustedProxies(peerIP, policy.TrustedProxies)
}

// requestPeerIP parses RemoteAddr as host:port and otherwise retries it as a
// bracket-trimmed address. Nil requests or invalid address text return false;
// successful IPv4-mapped values are unmapped before trust evaluation.
func requestPeerIP(r *http.Request) (netip.Addr, bool) {
	if r == nil {
		return netip.Addr{}, false
	}
	host, _, err := net.SplitHostPort(strings.TrimSpace(r.RemoteAddr))
	// Retry RemoteAddr as an address literal when host:port parsing fails,
	// allowing bracketed or plain peer addresses from compatible request fixtures.
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

// requestClientIP returns the first forwarded address component when valid and
// the direct peer is trusted, otherwise its parsed direct address. A nil
// request or unparseable direct address returns empty text.
//
// Trust is determined from RemoteAddr before X-Forwarded-For is considered.
// Only its first component is tried, with surrounding brackets removed and
// mapped IPv4 unmapped. An invalid first component returns to the direct-peer
// path; later components are not searched. QPSWSHandler skips per-address
// admission when this result is empty.
func requestClientIP(r *http.Request, policy OpsWSProxyConfig) string {
	if r == nil {
		return ""
	}

	trustProxyHeaders := shouldTrustOpsWSProxyHeaders(r, policy)
	if trustProxyHeaders {
		xff := strings.TrimSpace(r.Header.Get("X-Forwarded-For"))
		if xff != "" {
			// Try the first forwarded component under the direct-peer trust decision.
			// Invalid first text falls back to the peer instead of scanning later
			// components.
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

// isAddrInTrustedProxies reports membership of a valid address in any supplied
// prefix. Invalid addresses or no matching prefixes return false; this helper
// performs no mapped-address or prefix-family normalization itself.
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

// parseTrustedProxyList returns accepted masked prefixes and trimmed rejected
// tokens from a comma-separated list. Blank tokens are omitted, duplicate
// entries remain, and a list with no valid entries returns no prefixes.
//
// CIDR input is parsed with its supplied address family and masked; single
// addresses are unmapped first and converted to full-width prefixes. Thus an
// IPv4-mapped single address becomes IPv4, while a mapped CIDR retains its
// parsed family. Callers comparing unmapped peers account for this distinction.
// Invalid address or prefix syntax is returned for diagnostics instead of
// producing a matching prefix.
func parseTrustedProxyList(raw string) (prefixes []netip.Prefix, invalid []string) {
	for token := range strings.SplitSeq(raw, ",") {
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
				// Represent an unmapped literal as one host prefix, using IPv4 or IPv6
				// width so prefix membership retains its address family.
				bits := 128
				if addr.Is4() {
					bits = 32
				}
				p = netip.PrefixFrom(addr, bits)
			}
		}

		// Return rejected nonblank tokens as diagnostics while keeping accepted
		// prefixes usable; a caller can still receive an empty accepted list.
		if err != nil || !p.IsValid() {
			invalid = append(invalid, item)
			continue
		}

		prefixes = append(prefixes, p.Masked())
	}
	return prefixes, invalid
}

// hostWithoutPort trims text and extracts the host when SplitHostPort succeeds,
// otherwise stripping surrounding brackets or returning text before the first
// colon. Blank input returns empty; unbracketed IPv6 can be truncated by that
// last fallback.
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
	// For remaining malformed or unbracketed input, keep text before the first
	// colon; this textual fallback is not complete IPv6 host parsing.
	parts := strings.Split(hostport, ":")
	return parts[0]
}

// configureWebSocket copies one handler's handshake and connection policy.
// Trusted proxy strings are parsed into independent prefixes. Call during handler
// construction before routing requests; the upgrader closes over this handler's
// immutable policy rather than sharing configuration with another instance.
func (h *OpsHandler) configureWebSocket(options OpsWSOptions) {
	prefixes, _ := parseTrustedProxyList(strings.Join(options.TrustedProxies, ","))
	h.wsProxy = OpsWSProxyConfig{TrustProxy: options.TrustProxy, TrustedProxies: prefixes, OriginPolicy: options.OriginPolicy}
	// Process configuration bounds these counts to int32 before the runtime's
	// counters receive them, preventing a large positive ceiling from wrapping negative.
	h.wsLimits = opsWSRuntimeLimits{MaxConns: int32(options.MaxConns), MaxConnsPerIP: int32(options.MaxConnsPerIP)}
	h.wsUpgrader = websocket.Upgrader{
		CheckOrigin:  func(r *http.Request) bool { return isAllowedOpsWSOrigin(r, h.wsProxy) },
		Subprotocols: []string{"tokenrouter-admin", "sub2api-admin"},
	}
}
