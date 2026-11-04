package httpclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/net/http2"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	servertiming "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// upstream_pool.go shares upstream HTTP clients and tracks request
// reservations through response-body closure.
// An upstream client's connection identity depends on provider/proxy
// isolation, pool settings, the caller's protocol variant, and any raw TLS
// profile identity. Acquisition reserves an entry while its request is in
// flight; successful responses keep that reservation until body Close
// releases it once, while request failure releases after the result observer
// has run. Callers close bodies even after abandoning response processing.
// Failed transport calls finish their diagnostic attempt using the dispatched
// request context before their original error returns to platform handling.
//
// Cache hits reserve under the read lock, and misses recheck under the write
// lock before construction or eviction. Expired and oldest-entry eviction
// skip reservations, while incompatible configuration replacement can remove
// an active entry from the cache and close only its idle connections. Request
// callbacks are installed on a copied client rather than cached. Fingerprint
// HTTP/2 uses a separate timeout wrapper whose timer includes underlying
// RoundTrip and requires that transport to honor cancellation.

// Upstream phase and HTTP/2 PING defaults bound connection setup and idle
// health checks independently of each request's body-processing lifetime.
const (
	defaultUpstreamDialTimeout         = 10 * time.Second
	defaultUpstreamDialKeepAlive       = 30 * time.Second
	defaultUpstreamTLSHandshakeTimeout = 10 * time.Second
	http2ReadIdleTimeout               = 15 * time.Second
	http2PingTimeout                   = 15 * time.Second
)

// Transport errors distinguish cache admission failure from the synthetic
// response-return timeout so request observers and callers can classify either result.
var (
	// ErrUpstreamClientLimitReached reports that the cache is full and every entry is in use.
	ErrUpstreamClientLimitReached = errors.New("upstream client cache limit reached")

	// errResponseHeaderTimeout shares the synthetic net.Error value returned when
	// its timer wins the response-return race before a body reaches the caller.
	errResponseHeaderTimeout = &responseHeaderTimeoutError{}
)

// TransportProtocol selects HTTP negotiation and the caller-defined cache variant.
type TransportProtocol struct {
	// CacheVariant identifies protocol construction choices in cache keys.
	// Callers distinguish incompatible HTTP2 and DisableHTTP2 choices because
	// those flags are not independently hashed.
	CacheVariant string // caller protocol cache identity
	// HTTP2 wins over DisableHTTP2 on ordinary transports. Fingerprint
	// selection also requires profile ALPN eligibility, while HTTPS proxy
	// selection uses ordinary construction with default protocol options.
	HTTP2        bool // enable HTTP/2 transport selection
	DisableHTTP2 bool // disable ordinary HTTP/2 negotiation
}

// UpstreamRequestOptions configures one outbound request and its connection pool.
type UpstreamRequestOptions struct {
	ProxyURL   string // selected proxy URL text
	ProviderID int64  // selected provider database identity
	// Isolation selects proxy, provider, or provider_proxy cache identity;
	// unrecognized values use proxy identity. Provider-only reuse additionally
	// compares the stored normalized proxy key.
	Isolation string // client cache isolation mode
	// MaxClients bounds cached entries when positive, with idle eviction making
	// room. Nonpositive values disable the cap, and configuration replacement
	// can remove an active entry from membership.
	MaxClients int // positive cache entry limit
	// IdleTTL expires unreserved entries on acquisition when positive.
	// Nonpositive values disable expiry, and an active reservation prevents this
	// time-based eviction path.
	IdleTTL  time.Duration     // cache entry idle lifetime
	Settings UpstreamSettings  // configured transport pool values
	Protocol TransportProtocol // protocol and cache identity choices
	// PublicTargetsOnly gives the request a separate cache entry whose direct
	// connections are checked and pinned when dialed. The separate entry stops it
	// reusing a connection opened without that check; proxied routes are unchanged.
	PublicTargetsOnly bool // pin direct connections to public addresses
	// TLSProfile contributes its raw CacheKey and is retained by custom dialers.
	// Keep profile values stable and copy or serialize GREASE SupportedVersions
	// storage before concurrent handshakes.
	TLSProfile *tlsfingerprint.Profile // optional fingerprint profile pointer
	// CheckRedirect belongs to this request's client copy; nil selects
	// net/http's default redirect policy. It is installed before PrepareClient
	// can adapt or replace the copy.
	CheckRedirect func(*http.Request, []*http.Request) error
	// PrepareClient receives a shallow client copy and can change callbacks or
	// wrap the shared transport. Keep that transport immutable; return a non-nil
	// configured client to retain selected routing, because nil makes timing.Do
	// use http.DefaultClient.
	PrepareClient func(*http.Client) *http.Client
	// ObserveResult runs before decompression and reservation release after
	// headers or failure. It must return normally so Do can install body
	// tracking or release a failed attempt.
	ObserveResult func(error)
}

// UpstreamPool caches ordinary and fingerprinted clients and tracks in-flight requests.
type UpstreamPool struct {
	// mu protects clients membership and reservation admission. Counters are
	// atomic, but acquisition increments while holding the membership lock so
	// idle eviction cannot remove a just-admitted entry.
	mu      sync.RWMutex                    // cache membership and admission mutex
	clients map[string]*upstreamClientEntry // cached clients by construction identity
}

// UpstreamSettings selects connection counts and phase budgets for cached transports.
type UpstreamSettings struct {
	// MaxIdleConns, MaxIdleConnsPerHost, and MaxConnsPerHost configure ordinary
	// http.Transport paths. Fingerprint HTTP/2 constructs an x/net transport
	// without these connection-count fields; its retained pool has its own
	// connection management.
	MaxIdleConns        int           // Maximum idle connections across hosts.
	MaxIdleConnsPerHost int           // Maximum idle connections per host.
	MaxConnsPerHost     int           // Maximum connections per host, including active connections.
	IdleConnTimeout     time.Duration // Maximum idle connection age.
	// ResponseHeaderTimeout starts after request writing on an ordinary
	// transport. Fingerprint HTTP/2 instead wraps the whole RoundTrip call,
	// including connection setup; nonpositive values disable that synthetic timer.
	ResponseHeaderTimeout time.Duration // Maximum wait for response headers.
}

// upstreamClientEntry tracks a cached client and the metadata used for reuse and eviction.
type upstreamClientEntry struct {
	client       *http.Client // cached construction-time HTTP client
	proxyKey     string       // normalized proxy identity text
	poolKey      string       // connection settings identity text
	protocolMode string       // caller protocol variant text
	// lastUsed and inFlight use separate atomic accesses. Acquisition increments
	// while membership is locked; response cleanup releases and timestamps the
	// entry independently. Reservations prevent idle eviction, while incompatible
	// configuration can still remove an active entry from cache membership.
	lastUsed int64 // last reservation/release Unix nanoseconds
	inFlight int64 // outstanding request reservation count
}

// responseHeaderTimeoutError identifies synthetic response-return timeout
// errors for net.Error callers.
type responseHeaderTimeoutError struct{}

// responseHeaderTimeoutRoundTripper bounds RoundTrip for an http2.Transport.
// The timer stops at response headers so it does not cancel a long-lived SSE body.
type responseHeaderTimeoutRoundTripper struct {
	// base must honor cancellation for a timed-out RoundTrip to return promptly.
	// The wrapper waits for that call after requesting cancellation rather than
	// abandoning its work.
	base    http.RoundTripper // wrapped HTTP/2 request transport
	timeout time.Duration     // positive underlying call budget
}

// cancelOnCloseReadCloser retains a request context until its successful body
// closes.
type cancelOnCloseReadCloser struct {
	// ReadCloser and cancel retain the successful request's body and child
	// context together. Close attempts body cleanup first and cancels afterward,
	// including body errors.
	io.ReadCloser                    // retained successful response body
	cancel        context.CancelFunc // child request cancellation function
}

// trackedBody runs its release callback once when the body is closed.
type trackedBody struct {
	io.ReadCloser           // retained decoded response body
	once          sync.Once // reservation release completion guard
	// onClose follows underlying Close and is guarded by once so successful
	// response cleanup returns a reservation once even when callers close
	// repeatedly.
	onClose func() // request reservation release callback
}

// NewUpstreamPool constructs an initialized upstream cache with its own entry
// mutex.
func NewUpstreamPool() *UpstreamPool {
	return &UpstreamPool{clients: make(map[string]*upstreamClientEntry)}
}

// Do reserves an upstream client, executes req with per-request callbacks,
// and installs supported response decoding. Close the successful response
// body to release its reservation; failure releases after ObserveResult runs,
// and callbacks must return normally to reach cleanup.
//
// CheckRedirect is set on a shallow client copy before PrepareClient, whose
// returned client must retain usable transport ownership. ObserveResult sees
// request error before decompression or release. The body release callback
// runs once, while the underlying Close can still run repeatedly. Pool
// construction and callback configuration remain separate from provider retry
// policy. Transport failure updates the captured diagnostic attempt even when
// the request has no performance collector or response headers.
// @project-doc docs/operations/upstream_transport_security.md#upstream_client_pool
func (s *UpstreamPool) Do(req *http.Request, opts UpstreamRequestOptions) (response *http.Response, failure error) {
	var attemptNumber int64
	defer func() {
		if failure != nil && req != nil {
			// The dispatched request retains its own ordinal after later exchanges start.
			// Finish failure here because network errors provide no response for platform callbacks.
			telemetry.ObserveRequestResult(req.Context(), telemetry.RequestAttemptFromContext(req.Context()), failure)
			if stages := servertiming.StagesFromContext(req.Context()); stages != nil {
				stages.FinishAttempt(attemptNumber)
			}
		}
	}()
	if req != nil {
		req = req.WithContext(servertiming.BeginHTTPTrace(req.Context()))
		if stages := servertiming.StagesFromContext(req.Context()); stages != nil {
			attemptNumber = stages.Attempt()
		}
	}
	entry, err := s.acquire(opts)
	if err != nil {
		return nil, err
	}
	release := func() {
		atomic.AddInt64(&entry.inFlight, -1)
		atomic.StoreInt64(&entry.lastUsed, time.Now().UnixNano())
	}
	// Per-request callbacks change policy while transport resources remain shared.
	// Copy the client before assignment so another request keeps its own redirect decision.
	requestClient := *entry.client
	requestClient.CheckRedirect = opts.CheckRedirect
	client := &requestClient
	if opts.PrepareClient != nil {
		client = opts.PrepareClient(client)
	}
	resp, err := servertiming.Do(client, req)
	if opts.ObserveResult != nil {
		opts.ObserveResult(err)
	}
	if err != nil {
		release()
		return nil, err
	}
	decompressResponseBody(resp)
	resp.Body = wrapTrackedBody(resp.Body, release)
	servertiming.ObserveResponse(req.Context(), resp)
	return resp, nil
}

// acquire returns a compatible entry after incrementing its in-flight
// reservation, or a construction/capacity error. Callers balance successful
// acquisition through Do's failure or body-close path; NewUpstreamPool
// initializes the map required by this operation.
//
// Hits reserve under a read lock, and misses recheck under the write lock
// before constructing a transport. Changed pool settings or a provider-mode
// proxy mismatch remove the existing cache entry even while its responses
// remain active. A changed TLS profile hash chooses another cache key, so
// that older profile entry can remain until ordinary eviction removes it.
// Capacity and expiry remove idle entries only; when no idle entry can make
// space, acquisition returns ErrUpstreamClientLimitReached. Protocol booleans
// require an appropriately distinguishing caller CacheVariant.
func (s *UpstreamPool) acquire(opts UpstreamRequestOptions) (*upstreamClientEntry, error) {
	isolation := opts.Isolation
	proxyKey, parsedProxy, err := proxy.NormalizePoolKey(opts.ProxyURL)
	if err != nil {
		return nil, err
	}
	settings := opts.Settings
	protocolMode := opts.Protocol.CacheVariant
	cacheKey := buildCacheKey(isolation, proxyKey, opts.ProviderID, protocolMode)
	poolKey := buildPoolKey(settings, protocolMode)
	if opts.PublicTargetsOnly {
		cacheKey += "|public_targets"
		poolKey += "|public_targets"
	}
	if opts.TLSProfile != nil {
		profileKey := tlsfingerprint.CacheKey(opts.TLSProfile)
		cacheKey = "tls:" + profileKey + ":" + cacheKey
		poolKey += ":tls:" + profileKey
	}
	now := time.Now()
	nowUnix := now.UnixNano()

	// Reserve a compatible hit while membership stays read-locked; idle eviction
	// needs the write lock and cannot remove this entry before its count increases.
	s.mu.RLock()
	if entry, ok := s.clients[cacheKey]; ok && s.shouldReuseEntry(entry, isolation, proxyKey, poolKey) {
		atomic.StoreInt64(&entry.lastUsed, nowUnix)
		{
			atomic.AddInt64(&entry.inFlight, 1)
		}
		s.mu.RUnlock()
		return entry, nil
	}
	s.mu.RUnlock()

	// Another miss may have published while the read lock was released. Recheck
	// under exclusive ownership before constructing or replacing this cache identity.
	s.mu.Lock()
	if entry, ok := s.clients[cacheKey]; ok {
		if s.shouldReuseEntry(entry, isolation, proxyKey, poolKey) {
			atomic.StoreInt64(&entry.lastUsed, nowUnix)
			{
				atomic.AddInt64(&entry.inFlight, 1)
			}
			s.mu.Unlock()
			return entry, nil
		}
		s.removeClientLocked(cacheKey, entry)
	}

	// Keep active reservations during capacity eviction. If every cached entry
	// is in flight, report a capacity error rather than evicting an active request.
	if opts.MaxClients > 0 {
		s.evictIdleLocked(now, opts.IdleTTL)
		if len(s.clients) >= opts.MaxClients {
			if !s.evictOldestIdleLocked() {
				s.mu.Unlock()
				return nil, ErrUpstreamClientLimitReached
			}
		}
	}

	// Select the direct connector once so ordinary HTTP and fingerprinted TLS
	// share the same destination enforcement, including subsequent HTTP redirects.
	var directDial dialContextFunc
	if opts.PublicTargetsOnly && parsedProxy == nil {
		directDial = publicTargetDialContext(newUpstreamDialer().DialContext, net.DefaultResolver.LookupIP)
	}
	var transport http.RoundTripper
	if opts.TLSProfile == nil {
		transport, err = buildUpstreamTransport(settings, parsedProxy, opts.Protocol, directDial)
	} else {
		transport, err = buildUpstreamTransportWithTLSFingerprint(settings, parsedProxy, opts.TLSProfile, opts.Protocol, directDial)
	}
	if err != nil {
		s.mu.Unlock()
		if opts.TLSProfile != nil {
			return nil, fmt.Errorf("build TLS fingerprint transport: %w", err)
		}
		return nil, fmt.Errorf("build transport: %w", err)
	}
	client := &http.Client{Transport: transport}
	entry := &upstreamClientEntry{
		client:       client,
		proxyKey:     proxyKey,
		poolKey:      poolKey,
		protocolMode: protocolMode,
	}
	atomic.StoreInt64(&entry.lastUsed, nowUnix)
	{
		atomic.StoreInt64(&entry.inFlight, 1)
	}
	s.clients[cacheKey] = entry

	// Evict expired idle entries before enforcing the entry limit.
	s.evictIdleLocked(now, opts.IdleTTL)
	s.evictOverLimitLocked(opts.MaxClients)
	s.mu.Unlock()
	return entry, nil
}

// shouldReuseEntry reports whether a non-nil entry's pool key and
// provider-isolated proxy identity still match. Other isolation modes already
// include proxy identity in the cache key; active-count checks are left to
// eviction callers.
func (s *UpstreamPool) shouldReuseEntry(entry *upstreamClientEntry, isolation, proxyKey, poolKey string) bool {
	if entry == nil {
		return false
	}
	if isolation == "provider" && entry.proxyKey != proxyKey {
		return false
	}
	if entry.poolKey != poolKey {
		return false
	}
	return true
}

// removeClientLocked deletes key and asks its cached transport to close idle
// connections. The caller holds s.mu for writing; active bodies can still use
// the removed entry and release its independent counters after completion.
func (s *UpstreamPool) removeClientLocked(key string, entry *upstreamClientEntry) {
	delete(s.clients, key)
	if entry != nil && entry.client != nil {
		// Remove membership before requesting idle cleanup, so later acquisitions
		// cannot reuse this incompatible entry while existing body holders finish independently.
		entry.client.CloseIdleConnections()
		if closer, ok := entry.client.Transport.(interface {
			// CloseIdleConnections asks the cached transport to release idle connection resources.
			CloseIdleConnections()
		}); ok {
			closer.CloseIdleConnections()
		}
	}
}

// evictIdleLocked removes entries whose sampled reservation count is zero
// and whose last-use time is at or before now minus ttl. The caller holds
// s.mu for writing; nonpositive ttl disables expiry.
//
// The write lock excludes a new acquisition while each entry is checked and
// removed. Response cleanup can still update its two atomic fields
// independently. Expiry deletes cache membership and asks the transport to
// close idle connections, while a nonzero reservation keeps the entry
// regardless of the sampled age.
func (s *UpstreamPool) evictIdleLocked(now time.Time, ttl time.Duration) {
	if ttl <= 0 {
		return
	}
	cutoff := now.Add(-ttl).UnixNano()
	// Admission is excluded by the write lock. Skip reservations before the age
	// test so a stale last-use observation alone cannot remove an in-flight client.
	for key, entry := range s.clients {
		if atomic.LoadInt64(&entry.inFlight) != 0 {
			continue
		}
		if atomic.LoadInt64(&entry.lastUsed) <= cutoff {
			s.removeClientLocked(key, entry)
		}
	}
}

// evictOldestIdleLocked removes one entry with a sampled zero reservation
// count and the oldest sampled last-use time. It reports false when no such
// entry is found, allowing acquisition to return a capacity error.
//
// The caller holds s.mu for writing to exclude new admission during selection
// and removal. Response releases can still update the atomic observations.
// Equal oldest timestamps follow map iteration order; removing the selected
// entry asks its transport to close idle connections.
func (s *UpstreamPool) evictOldestIdleLocked() bool {
	var (
		oldestKey   string
		oldestEntry *upstreamClientEntry
		oldestTime  int64
	)
	// Choose among unreserved entries before comparing last use. This keeps the
	// capacity recovery decision separate from timestamps on clients still serving requests.
	for key, entry := range s.clients {
		if atomic.LoadInt64(&entry.inFlight) != 0 {
			continue
		}
		lastUsed := atomic.LoadInt64(&entry.lastUsed)
		if oldestEntry == nil || lastUsed < oldestTime {
			oldestKey = key
			oldestEntry = entry
			oldestTime = lastUsed
		}
	}
	if oldestEntry == nil {
		return false
	}
	s.removeClientLocked(oldestKey, oldestEntry)
	return true
}

// evictOverLimitLocked repeatedly removes oldest idle entries while cache
// size exceeds positive maxClients and reports whether any entry was removed.
// The caller holds s.mu for writing;
// disabled limits return false, and remaining active entries can prevent
// reaching the supplied limit.
func (s *UpstreamPool) evictOverLimitLocked(maxClients int) bool {
	if maxClients <= 0 {
		return false
	}
	evicted := false
	for len(s.clients) > maxClients {
		if !s.evictOldestIdleLocked() {
			return evicted
		}
		evicted = true
	}
	return evicted
}

// buildPoolKey identifies configured connection counts, lifetimes, header
// timeout, and nondefault protocol variant. Empty and default variant values
// share the base spelling; protocol booleans participate only through the
// caller-selected variant.
func buildPoolKey(settings UpstreamSettings, protocolMode string) string {
	base := fmt.Sprintf(
		"idle:%d|idle_host:%d|max:%d|idle_timeout:%s|header_timeout:%s",
		settings.MaxIdleConns,
		settings.MaxIdleConnsPerHost,
		settings.MaxConnsPerHost,
		settings.IdleConnTimeout,
		settings.ResponseHeaderTimeout,
	)
	if protocolMode == "" || protocolMode == "default" {
		return base
	}
	return base + "|proto:" + protocolMode
}

// buildCacheKey chooses provider, provider/proxy, or proxy isolation and
// appends a nondefault protocol variant. Unrecognized isolation uses proxy
// identity; empty and default variants share spelling, so callers distinguish
// incompatible protocol choices themselves.
func buildCacheKey(isolation, proxyKey string, providerID int64, protocolMode string) string {
	var base string
	switch isolation {
	case "provider":
		base = fmt.Sprintf("provider:%d", providerID)
	case "provider_proxy":
		base = fmt.Sprintf("provider:%d|proxy:%s", providerID, proxyKey)
	default:
		base = fmt.Sprintf("proxy:%s", proxyKey)
	}
	if protocolMode != "" && protocolMode != "default" {
		base += "|proto:" + protocolMode
	}
	return base
}

// newUpstreamDialer supplies the upstream network setup budget and TCP
// keepalive interval. Its ten-second limit covers DNS and connection
// establishment when used as DialContext; custom SOCKS routing installs its
// own bounded forward connector.
func newUpstreamDialer() *net.Dialer {
	return &net.Dialer{
		Timeout:   defaultUpstreamDialTimeout,
		KeepAlive: defaultUpstreamDialKeepAlive,
	}
}

// buildUpstreamTransport applies settings and proxy routing to an ordinary
// certificate-verifying transport. HTTP2 takes precedence when both protocol
// flags are true; otherwise DisableHTTP2 removes TLS-next-protocol
// negotiation, and selected HTTP2 adds idle PING settings. Direct connections
// use directDial when nonnil; proxy routing retains its bounded default dialer.
func buildUpstreamTransport(settings UpstreamSettings, proxyURL *url.URL, protocol TransportProtocol, directDial dialContextFunc) (*http.Transport, error) {
	if directDial == nil || proxyURL != nil {
		directDial = newUpstreamDialer().DialContext
	}
	transport := &http.Transport{
		DialContext:           directDial,
		TLSHandshakeTimeout:   defaultUpstreamTLSHandshakeTimeout,
		MaxIdleConns:          settings.MaxIdleConns,
		MaxIdleConnsPerHost:   settings.MaxIdleConnsPerHost,
		MaxConnsPerHost:       settings.MaxConnsPerHost,
		IdleConnTimeout:       settings.IdleConnTimeout,
		ResponseHeaderTimeout: settings.ResponseHeaderTimeout,
	}
	switch {
	case protocol.HTTP2:
		transport.ForceAttemptHTTP2 = true
		// HTTP/2 idle PINGs request a response after a quiet connection, letting the
		// transport detect a connection whose peer or intermediary has stopped responding.
		enableHTTP2KeepAlive(transport)
	case protocol.DisableHTTP2:
		transport.ForceAttemptHTTP2 = false
		transport.TLSNextProto = make(map[string]func(string, *tls.Conn) http.RoundTripper)
	}
	if err := proxy.ConfigureTransportProxy(transport, proxyURL); err != nil {
		return nil, err
	}
	return transport, nil
}

// enableHTTP2KeepAlive enables HTTP/2 alongside HTTP/1 on a caller-owned
// transport and configures idle PING budgets. Configure before concurrent
// requests; existing protocol selections are retained except HTTP/2 is
// enabled, while new protocol storage starts with HTTP/1 enabled.
func enableHTTP2KeepAlive(transport *http.Transport) {
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = new(tls.Config)
	}
	if transport.Protocols == nil {
		transport.Protocols = new(http.Protocols)
		transport.Protocols.SetHTTP1(true)
	}
	transport.Protocols.SetHTTP2(true)
	if transport.HTTP2 == nil {
		transport.HTTP2 = new(http.HTTP2Config)
	}
	transport.HTTP2.SendPingTimeout = http2ReadIdleTimeout
	transport.HTTP2.PingTimeout = http2PingTimeout
}

// buildUpstreamTransportWithTLSFingerprint selects direct, SOCKS, or CONNECT
// target dialing from profile and proxy. HTTPS proxies return an ordinary
// transport with default protocol options, preserving proxy routing while
// bypassing fingerprinted target dialing.
//
// HTTP/2 requires protocol.HTTP2 and profile eligibility, then actual
// negotiated ALPN equal to h2; mismatch closes the connection. Its x/net
// transport uses fixed PING budgets and the optional cancellation-based
// RoundTrip timeout wrapper. The ordinary fingerprint path returns its
// configured http.Transport, with target handshake timing owned by the custom
// dialer. directDial replaces the network connector on the direct route; nil
// selects a bounded default, and proxied routes ignore it. The same direct
// connector handles plain HTTP redirects and the TCP phase of TLS handshakes.
func buildUpstreamTransportWithTLSFingerprint(settings UpstreamSettings, proxyURL *url.URL, profile *tlsfingerprint.Profile, protocol TransportProtocol, directDial dialContextFunc) (http.RoundTripper, error) {
	if directDial == nil || proxyURL != nil {
		directDial = newUpstreamDialer().DialContext
	}
	useHTTP2 := protocol.HTTP2 && tlsfingerprint.SupportsHTTP2(profile)
	transport := &http.Transport{
		DialContext:           directDial,
		MaxIdleConns:          settings.MaxIdleConns,
		MaxIdleConnsPerHost:   settings.MaxIdleConnsPerHost,
		MaxConnsPerHost:       settings.MaxConnsPerHost,
		IdleConnTimeout:       settings.IdleConnTimeout,
		ResponseHeaderTimeout: settings.ResponseHeaderTimeout,
		// Use the HTTP/2 round tripper only when the profile advertises h2.
		ForceAttemptHTTP2: useHTTP2,
	}

	var dialTLSContext func(ctx context.Context, network, addr string) (net.Conn, error)
	if proxyURL == nil {
		slog.Debug("tls_fingerprint_transport_direct")
		dialer := tlsfingerprint.NewDialer(profile, directDial)
		dialTLSContext = dialer.DialTLSContext
	} else {
		scheme := strings.ToLower(proxyURL.Scheme)
		switch scheme {
		case "socks5", "socks5h":
			slog.Debug("tls_fingerprint_transport_socks5", "proxy", proxyURL.Host)
			socks5Dialer := tlsfingerprint.NewSOCKS5ProxyDialer(profile, proxyURL)
			dialTLSContext = socks5Dialer.DialTLSContext
		case "https":
			// HTTPS proxies select the ordinary connector here, retaining proxy TLS and
			// routing while this path omits target fingerprint dialing and custom protocol flags.
			return buildUpstreamTransport(settings, proxyURL, TransportProtocol{CacheVariant: "default"}, nil)
		case "http":
			slog.Debug("tls_fingerprint_transport_http_connect", "proxy", proxyURL.Host)
			httpDialer := tlsfingerprint.NewHTTPProxyDialer(profile, proxyURL)
			dialTLSContext = httpDialer.DialTLSContext
		default:
			// This branch delegates unsupported scheme handling to ordinary proxy setup,
			// so its validation error remains the result instead of selecting a direct route.
			slog.Debug("tls_fingerprint_transport_unknown_scheme_fallback", "scheme", scheme)
			if err := proxy.ConfigureTransportProxy(transport, proxyURL); err != nil {
				return nil, err
			}
		}
	}
	transport.DialTLSContext = dialTLSContext

	if useHTTP2 && dialTLSContext != nil {
		//nolint:staticcheck // uTLS has its own ConnectionState type; x/net accepts the raw TLS connection.
		h2Transport := &http2.Transport{
			IdleConnTimeout: settings.IdleConnTimeout,
			ReadIdleTimeout: http2ReadIdleTimeout,
			PingTimeout:     http2PingTimeout,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				conn, err := dialTLSContext(ctx, network, addr)
				if err != nil {
					return nil, err
				}
				negotiatedProtocol := tlsfingerprint.NegotiatedProtocol(conn)
				// The HTTP/2 transport needs actual h2 negotiation before it sends frames.
				// Close a differently negotiated connection instead of treating profile
				// eligibility as negotiation success.
				if negotiatedProtocol != "h2" {
					_ = conn.Close()
					return nil, fmt.Errorf("http2: unexpected ALPN protocol %q; want %q", negotiatedProtocol, "h2")
				}
				return conn, nil
			},
		}
		var roundTripper http.RoundTripper = h2Transport
		if settings.ResponseHeaderTimeout > 0 {
			roundTripper = &responseHeaderTimeoutRoundTripper{
				base:    h2Transport,
				timeout: settings.ResponseHeaderTimeout,
			}
		}
		return roundTripper, nil
	}

	return transport, nil
}

// Error supplies the response-header timeout's compatible net/http diagnostic
// text.
func (e *responseHeaderTimeoutError) Error() string {
	return "net/http: timeout awaiting response headers"
}

// Timeout reports this synthetic header error as a network timeout.
func (e *responseHeaderTimeoutError) Timeout() bool {
	return true
}

// Temporary reports this synthetic header timeout as a retry-classifiable
// temporary error.
func (e *responseHeaderTimeoutError) Temporary() bool {
	return true
}

// RoundTrip delegates with a cancellable context and starts a timeout before
// the underlying call, including its connection setup. Nonpositive timeout or
// nil req delegates directly; a missing base returns an error. The base must
// honor cancellation for the wait to end promptly.
//
// If timer firing races with completion, this method waits for timer
// publication and timeout wins, closing any returned body. Otherwise failure
// cancels immediately and success keeps the child context until body Close.
// Long-lived body reads therefore continue after the header timer is stopped.
func (r *responseHeaderTimeoutRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	if r == nil || r.base == nil {
		return nil, errors.New("response header timeout round tripper missing base")
	}
	if r.timeout <= 0 || req == nil {
		return r.base.RoundTrip(req)
	}
	ctx, cancel := context.WithCancel(req.Context())
	timeoutCh := make(chan struct{})
	timedOut := atomic.Bool{}
	// Publish timeout before cancellation can make the base return a context
	// error. The completion path waits for this publication before choosing its
	// caller-visible result when timer execution overlaps response return.
	timer := time.AfterFunc(r.timeout, func() {
		timedOut.Store(true)
		close(timeoutCh)
		cancel()
	})
	resp, err := r.base.RoundTrip(req.WithContext(ctx))
	// A firing timer can overlap response return. Wait for its published
	// timeout decision before selecting success so expiry cannot be lost in that race.
	if !timer.Stop() {
		<-timeoutCh
	}
	if timedOut.Load() {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		cancel()
		return nil, errResponseHeaderTimeout
	}
	if err != nil {
		cancel()
		return nil, err
	}
	if resp == nil || resp.Body == nil {
		cancel()
		return resp, nil
	}
	// Header timing is finished, but the body may stream for much longer.
	// Keep its child context live until body Close releases the request lifetime.
	resp.Body = &cancelOnCloseReadCloser{
		ReadCloser: resp.Body,
		cancel:     cancel,
	}
	return resp, nil
}

// CloseIdleConnections delegates an idle-close request when the wrapped
// transport exposes that operation. Missing receiver/base or an unsupported
// operation is ignored; active response bodies retain their normal cleanup
// and request cancellation obligations.
func (r *responseHeaderTimeoutRoundTripper) CloseIdleConnections() {
	if r == nil || r.base == nil {
		return
	}
	if closer, ok := r.base.(interface {
		// CloseIdleConnections releases idle resources while active response
		// bodies remain caller-owned.
		CloseIdleConnections()
	}); ok {
		closer.CloseIdleConnections()
	}
}

// Close invokes the wrapped body's cleanup and then cancels its request
// context when present. It returns the body's error unchanged; repeated calls
// invoke underlying Close and the cancellation function again.
func (r *cancelOnCloseReadCloser) Close() error {
	err := r.ReadCloser.Close()
	if r.cancel != nil {
		r.cancel()
	}
	return err
}

// Close invokes the underlying body's cleanup and then runs the release
// callback at most once, including when cleanup returns an error. Repeated
// calls still invoke underlying Close; callers retain that body's concurrency
// obligations.
func (b *trackedBody) Close() error {
	err := b.ReadCloser.Close()
	if b.onClose != nil {
		b.once.Do(b.onClose)
	}
	return err
}

// wrapTrackedBody binds onClose to body cleanup through sync.Once, returning
// nil bodies unchanged. Callers still close the returned body; repeat closes
// rerun underlying cleanup but invoke the release callback at most once.
func wrapTrackedBody(body io.ReadCloser, onClose func()) io.ReadCloser {
	if body == nil {
		return body
	}
	return &trackedBody{ReadCloser: body, onClose: onClose}
}

// CloseIdleConnections asks every cached client to close idle connections
// under the pool read lock. It retains cache entries and active bodies;
// callers still close responses to release their reservations.
func (s *UpstreamPool) CloseIdleConnections() {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, entry := range s.clients {
		if entry != nil && entry.client != nil {
			entry.client.CloseIdleConnections()
		}
	}
}
