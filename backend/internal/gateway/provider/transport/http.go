package transport

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	proxyinfra "github.com/TokenFlux/TokenRouter/internal/infra/httpclient/proxy"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// http.go executes upstream HTTP requests with provider isolation, platform
// transport settings, and destination checks.
// Upstream requests carry platform transport profiles alongside provider,
// proxy, and optional TLS identity. Client translates those values into pool
// options, checks destinations before exchange, and derives request-specific
// redirect behavior while egress owns proxy HTTP/2 recovery state and the
// technical pool owns connection reuse.
//
// Provider concurrency replaces the three connection limits only in provider
// or provider_proxy isolation mode. Platform-specific header waits then
// override the generic timeout. Redirect policy is applied to a copied HTTP
// client: disabling redirects takes precedence over public-host checks, while
// public-host checks run before any existing redirect callback. HTTP/2
// compatibility failures exclude timeouts before updating the shared proxy
// recovery policy, so timeout evidence alone cannot activate that recovery
// path.

const (
	// Pool defaults govern cached clients and connection limits when supplied
	// settings are absent or fall outside each option's accepted range.
	// defaultMaxIdleConns limits idle connections across hosts.
	defaultMaxIdleConns = 240
	// defaultMaxIdleConnsPerHost limits idle connections to one host.
	defaultMaxIdleConnsPerHost = 120
	// defaultMaxConnsPerHost includes active connections; new requests wait at the limit.
	defaultMaxConnsPerHost = 240
	// defaultIdleConnTimeout bounds how long unused connections remain open.
	defaultIdleConnTimeout = 90 * time.Second
	// defaultResponseHeaderTimeout bounds the generic wait for upstream response headers.
	defaultResponseHeaderTimeout = 300 * time.Second
	// defaultMaxUpstreamClients limits the cached upstream clients.
	defaultMaxUpstreamClients = 5000
	// defaultClientIdleTTLSeconds is the client eviction idle threshold in seconds.
	defaultClientIdleTTLSeconds = 900
	// HTTP/2 recovery defaults bound failure accumulation and the lifetime of
	// proxy-specific HTTP/1 selection maintained by the egress transport policy.
	defaultOpenAIHTTP2FallbackErrorThreshold = 2
	defaultOpenAIHTTP2FallbackWindow         = 60 * time.Second
	defaultOpenAIHTTP2FallbackTTL            = 10 * time.Minute

	// Grok endpoint hosts identify the CLI request and official API replay target
	// asserted by the transport compatibility tests.
	grokCLIProxyHost    = "cli-chat-proxy.grok.com"
	grokOfficialAPIHost = "api.x.ai"

	// Protocol mode labels separate pool cache variants so later HTTP/1 recovery
	// does not reuse the transport selected for an HTTP/2 request.
	upstreamProtocolModeDefault          = "default"
	upstreamProtocolModeOpenAIH1         = "openai_h1"
	upstreamProtocolModeOpenAIH2         = "openai_h2"
	upstreamProtocolModeOpenAIH1Fallback = "openai_h1_fallback"
)

// openAIHTTP2Settings is the normalized policy sent to egress transport planning.
type openAIHTTP2Settings struct {
	enabled                   bool          // enable OpenAI HTTP/2
	allowProxyFallbackToHTTP1 bool          // permit proxy HTTP/1 recovery
	fallbackErrorThreshold    int           // failures before recovery
	fallbackWindow            time.Duration // failure counting interval
	fallbackTTL               time.Duration // recovery mode lifetime
}

// poolSettings contains connection pool settings supplied by the application.
type poolSettings = httpclient.UpstreamSettings

// upstreamPool lets tests substitute the transport used for upstream requests.
type upstreamPool interface {
	// Do executes a request using resolved technical transport options. A
	// successful response transfers body closure to the caller; closing it
	// releases any in-flight accounting held by the pool.
	Do(*http.Request, httpclient.UpstreamRequestOptions) (*http.Response, error)
}

// Client applies provider transport policies and reuses an upstream connection pool.
type Client struct {
	// source is consulted during preparation; callers keep returned options
	// stable while each synchronous read resolves request transport settings.
	source          func() *Options
	pool            upstreamPool           // shared connection pool
	transportPolicy egress.TransportPolicy // proxy protocol recovery state
}

// httpClientForEgressPolicy supplies request-specific redirect checks through a
// shallow client copy. Disabled redirects return the last response; public-host
// requests validate every hop before the existing callback, while ordinary
// policy returns the supplied client and a nil client stays nil.
func httpClientForEgressPolicy(s *Client, client *http.Client, policy egress.EgressPolicy) *http.Client {
	if client == nil {
		return nil
	}
	switch {
	case policy.DisableRedirects:
		clone := *client
		clone.CheckRedirect = func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		}
		return &clone
	case policy.PublicHostsOnly && s != nil:
		clone := *client
		clone.CheckRedirect = func(next *http.Request, via []*http.Request) error {
			// Redirect requests inherit the public-host marker before validation, so a
			// private next hop is rejected before the preexisting redirect callback can
			// approve it.
			next = next.WithContext(upstream.WithHTTPUpstreamPublicHostsOnly(next.Context()))
			if err := s.redirectChecker(next, via); err != nil {
				return err
			}
			if client.CheckRedirect != nil {
				return client.CheckRedirect(next, via)
			}
			return nil
		}
		return &clone
	default:
		return client
	}
}

// httpClientWithGrokAccessDeniedFallback delegates Grok compatibility replay
// wrapping to the platform implementation.
func httpClientWithGrokAccessDeniedFallback(client *http.Client) *http.Client {
	return xai.ClientWithAccessDeniedFallback(client)
}

// applyGrokCLIProxyHeaders adds platform identity headers for eligible Grok CLI
// requests.
func applyGrokCLIProxyHeaders(req *http.Request) { xai.ApplyTransportCLIHeaders(req) }

// shouldValidateResolvedIP reports whether the options source enables resolved-
// IP validation. Missing options disable that global check; per-request public-
// host restrictions are applied separately by requestPolicy during preparation.
func (s *Client) shouldValidateResolvedIP() bool {
	if s.options() == nil {
		return false
	}
	return s.options().ValidateResolvedIP
}

// validateRequestHost checks the resolved destination when the request policy
// requires it. Otherwise it succeeds immediately; required checks reject nil
// URLs and blank hosts before the technical validator resolves addresses.
func (s *Client) validateRequestHost(req *http.Request) error {
	if !s.requestPolicy(req).RequiresHostValidation() {
		return nil
	}
	if req == nil || req.URL == nil {
		return errors.New("request url is nil")
	}
	host := strings.TrimSpace(req.URL.Hostname())
	if host == "" {
		return errors.New("request host is empty")
	}
	return httpclient.ValidateResolvedIP(host)
}

// redirectChecker validates each destination while limiting the redirect chain
// to ten requests. It returns an error before host validation when the
// previous-request list reaches that limit.
func (s *Client) redirectChecker(req *http.Request, via []*http.Request) error {
	// Cap redirect following even for valid public destinations.
	if len(via) >= 10 {
		return errors.New("stopped after 10 redirects")
	}
	return s.validateRequestHost(req)
}

// getIsolationMode selects proxy, provider, or provider_proxy after trimming
// and lowercasing the setting. Missing, blank, or unrecognized options select
// provider_proxy, which combines provider and proxy identity in pool selection.
func (s *Client) getIsolationMode() string {
	if s.options() == nil {
		return "provider_proxy"
	}
	mode := strings.ToLower(strings.TrimSpace(s.options().ConnectionPoolIsolation))
	if mode == "" {
		return "provider_proxy"
	}
	switch mode {
	case "proxy", "provider", "provider_proxy":
		return mode
	default:
		return "provider_proxy"
	}
}

// maxUpstreamClients returns the configured positive client-cache limit or the
// built-in default. Missing options and nonpositive limits use the same default
// when preparing the technical pool request.
func (s *Client) maxUpstreamClients() int {
	if s.options() == nil {
		return defaultMaxUpstreamClients
	}
	if s.options().MaxUpstreamClients > 0 {
		return s.options().MaxUpstreamClients
	}
	return defaultMaxUpstreamClients
}

// clientIdleTTL returns cached-client idle lifetime as a duration for the
// technical pool. Missing options and nonpositive configured seconds use the
// default eviction threshold; positive seconds are converted directly.
func (s *Client) clientIdleTTL() time.Duration {
	if s.options() == nil {
		return time.Duration(defaultClientIdleTTLSeconds) * time.Second
	}
	if s.options().ClientIdleTTLSeconds > 0 {
		return time.Duration(s.options().ClientIdleTTLSeconds) * time.Second
	}
	return time.Duration(defaultClientIdleTTLSeconds) * time.Second
}

// resolvePoolSettings derives generic limits and applies a positive provider
// concurrency to provider-isolated pools. Both provider and provider_proxy
// modes replace all three connection counts; proxy isolation keeps the
// configured global counts.
func (s *Client) resolvePoolSettings(isolation string, providerConcurrency int) poolSettings {
	settings := defaultPoolSettings(s.options())
	if (isolation == "provider" || isolation == "provider_proxy") && providerConcurrency > 0 {
		settings.MaxIdleConns = providerConcurrency
		settings.MaxIdleConnsPerHost = providerConcurrency
		settings.MaxConnsPerHost = providerConcurrency
	}
	return settings
}

// applyProfilePoolSettings overrides the generic response-header wait for
// OpenAI and Grok requests. OpenAI uses positive platform seconds or zero; Grok
// uses 120 seconds without options and the supplied platform seconds whenever
// options exist.
func (s *Client) applyProfilePoolSettings(settings poolSettings, profile upstream.HTTPUpstreamProfile) poolSettings {
	switch profile {
	case upstream.HTTPUpstreamProfileOpenAI:
		settings.ResponseHeaderTimeout = 0
		if s != nil && s.options() != nil && s.options().OpenAIResponseHeaderTimeout > 0 {
			settings.ResponseHeaderTimeout = time.Duration(s.options().OpenAIResponseHeaderTimeout) * time.Second
		}
	case upstream.HTTPUpstreamProfileGrok:
		// The Grok profile replaces the generic header wait because stream idle
		// timing begins after headers and is managed by response readers.
		settings.ResponseHeaderTimeout = 120 * time.Second
		if s != nil && s.options() != nil {
			settings.ResponseHeaderTimeout = time.Duration(s.options().GrokResponseHeaderTimeout) * time.Second
		}
	}
	return settings
}

// resolveOpenAIHTTP2Settings returns platform policy with defaults for
// nonpositive recovery thresholds and durations. Missing options disable HTTP/2
// and permit proxy recovery; present options directly set both booleans before
// positive numeric overrides.
func (s *Client) resolveOpenAIHTTP2Settings() openAIHTTP2Settings {
	settings := openAIHTTP2Settings{
		enabled:                   false,
		allowProxyFallbackToHTTP1: true,
		fallbackErrorThreshold:    defaultOpenAIHTTP2FallbackErrorThreshold,
		fallbackWindow:            defaultOpenAIHTTP2FallbackWindow,
		fallbackTTL:               defaultOpenAIHTTP2FallbackTTL,
	}
	if s == nil || s.options() == nil {
		return settings
	}
	cfg := s.options().OpenAIHTTP2
	settings.enabled = cfg.Enabled
	settings.allowProxyFallbackToHTTP1 = cfg.AllowProxyFallbackToHTTP1
	if cfg.FallbackErrorThreshold > 0 {
		settings.fallbackErrorThreshold = cfg.FallbackErrorThreshold
	}
	if cfg.FallbackWindowSeconds > 0 {
		settings.fallbackWindow = time.Duration(cfg.FallbackWindowSeconds) * time.Second
	}
	if cfg.FallbackTTLSeconds > 0 {
		settings.fallbackTTL = time.Duration(cfg.FallbackTTLSeconds) * time.Second
	}
	return settings
}

// resolveTLSFingerprintTransportProfile retains the profile in OpenAI HTTP/2
// mode and delegates other modes to HTTP1OnlyProfile. The latter removes h2
// ALPN from a clone when necessary, leaving the supplied profile available to
// its owner.
func resolveTLSFingerprintTransportProfile(profile *tlsfingerprint.Profile, protocolMode string) *tlsfingerprint.Profile {
	// Remove h2 advertisement outside the selected OpenAI HTTP/2 mode so TLS
	// negotiation agrees with the technical transport chosen by pool planning.
	if protocolMode != upstreamProtocolModeOpenAIH2 {
		return tlsfingerprint.HTTP1OnlyProfile(profile)
	}
	return profile
}

// isOpenAIHTTP2CompatibilityError recognizes textual ALPN, stream, frame, and
// protocol failures for recovery observation. Timeout classification takes
// precedence, so an error mentioning HTTP/2 plus a recognized timeout does not
// count as incompatibility.
func isOpenAIHTTP2CompatibilityError(err error) bool {
	if err == nil {
		return false
	}
	// Timeouts can mention HTTP/2 while the upstream simply waits for headers;
	// exclude that evidence before protocol markers can activate proxy recovery.
	if isUpstreamTimeoutError(err) {
		return false
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	markers := []string{
		"alpn",
		"no application protocol",
		"protocol error",
		"stream error",
		"goaway",
		"refused_stream",
		"frame too large",
	}
	for _, marker := range markers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// isUpstreamTimeoutError recognizes deadline, net.Error timeout, and known
// textual timeout evidence. It checks wrapped typed errors first, then scans
// the lowercase message; nil or unrecognized failures return false.
func isUpstreamTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	msg := strings.ToLower(err.Error())
	if msg == "" {
		return false
	}
	timeoutMarkers := []string{
		"timeout awaiting response headers",
		"i/o timeout",
		"context deadline exceeded",
		"client.timeout exceeded while awaiting headers",
		"tls handshake timeout",
	}
	for _, marker := range timeoutMarkers {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// recordOpenAIHTTP2Failure passes compatibility classification and current
// settings to egress recovery state. Egress decides whether the profile, mode,
// and proxy participate; activation logs the proxy key and expiry.
func (s *Client) recordOpenAIHTTP2Failure(profile upstream.HTTPUpstreamProfile, protocolMode, proxyKey string, err error) {
	activated, until := s.transportPolicy.ObserveFailure(string(profile), protocolMode, proxyKey, isOpenAIHTTP2CompatibilityError(err), s.http2Options(), time.Now())
	if activated {
		slog.Warn("openai_http2_proxy_fallback_activated", "proxy", proxyKey, "fallback_until", until.Format(time.RFC3339))
	}
}

// recordOpenAIHTTP2Success lets egress clear eligible proxy HTTP/2 failure
// observations.
func (s *Client) recordOpenAIHTTP2Success(profile upstream.HTTPUpstreamProfile, protocolMode, proxyKey string) {
	s.transportPolicy.ObserveSuccess(string(profile), protocolMode, proxyKey)
}

// normalizeProxyURL returns the proxy pool key and parsed URL from the
// technical normalizer. Empty input selects direct transport, and malformed
// nonempty input returns an error before pool options are created.
func normalizeProxyURL(raw string) (string, *url.URL, error) {
	return proxyinfra.NormalizePoolKey(raw)
}

// defaultPoolSettings applies each generic numeric option over built-in pool
// limits. Positive values override idle limits and idle lifetime; zero
// connection cap and zero header wait disable those constraints, while their
// negative values retain defaults.
func defaultPoolSettings(cfg *Options) poolSettings {
	maxIdleConns := defaultMaxIdleConns
	maxIdleConnsPerHost := defaultMaxIdleConnsPerHost
	maxConnsPerHost := defaultMaxConnsPerHost
	idleConnTimeout := defaultIdleConnTimeout
	responseHeaderTimeout := defaultResponseHeaderTimeout

	if cfg != nil {
		if cfg.MaxIdleConns > 0 {
			maxIdleConns = cfg.MaxIdleConns
		}
		if cfg.MaxIdleConnsPerHost > 0 {
			maxIdleConnsPerHost = cfg.MaxIdleConnsPerHost
		}
		if cfg.MaxConnsPerHost >= 0 {
			maxConnsPerHost = cfg.MaxConnsPerHost
		}
		if cfg.IdleConnTimeoutSeconds > 0 {
			idleConnTimeout = time.Duration(cfg.IdleConnTimeoutSeconds) * time.Second
		}
		if cfg.ResponseHeaderTimeout >= 0 {
			responseHeaderTimeout = time.Duration(cfg.ResponseHeaderTimeout) * time.Second
		}
	}

	return poolSettings{
		MaxIdleConns:          maxIdleConns,
		MaxIdleConnsPerHost:   maxIdleConnsPerHost,
		MaxConnsPerHost:       maxConnsPerHost,
		IdleConnTimeout:       idleConnTimeout,
		ResponseHeaderTimeout: responseHeaderTimeout,
	}
}

// New creates a Client with a shared technical upstream pool and an options
// source. The source is read when preparing requests; nil selects built-in
// settings, and callers release successful response bodies.
func New(source func() *Options) *Client {
	return &Client{source: source, pool: httpclient.NewUpstreamPool()}
}

// options reads the application options source or returns nil for a missing
// receiver or source. The returned pointer is borrowed for synchronous
// preparation, so its owner keeps the values stable during reads.
func (s *Client) options() *Options {
	if s == nil || s.source == nil {
		return nil
	}
	return s.source()
}

// transportOptions translates request policy, platform profile, proxy identity,
// and optional TLS fingerprint into technical pool options. Invalid proxy input
// stops preparation, provider concurrency applies according to isolation, and
// platform header waits override generic settings. Egress selects the protocol
// cache variant and any recovery-adjusted fingerprint. The returned callbacks
// apply per-request client wrappers and report exchange success or failure back
// to egress using the resolved mode and proxy key.
func (s *Client) transportOptions(req *http.Request, proxyURL string, providerID int64, concurrency int, tlsProfile *tlsfingerprint.Profile) (httpclient.UpstreamRequestOptions, error) {
	proxyKey, parsedProxy, err := normalizeProxyURL(proxyURL)
	if err != nil {
		return httpclient.UpstreamRequestOptions{}, err
	}
	profile := upstream.HTTPUpstreamProfileDefault
	if req != nil {
		profile = upstream.HTTPUpstreamProfileFromContext(req.Context())
	}
	isolation := s.getIsolationMode()
	settings := s.applyProfilePoolSettings(s.resolvePoolSettings(isolation, concurrency), profile)

	proxyScheme := ""
	if parsedProxy != nil {
		proxyScheme = parsedProxy.Scheme
	}
	targetPolicy := s.requestPolicy(req)
	policy := s.transportPolicy.Plan(egress.TransportRequest{TLSProfile: egressprovider.FromTLSProfile(tlsProfile), ValidateResolvedIP: targetPolicy.ValidateResolvedIP, PublicHostsOnly: targetPolicy.PublicHostsOnly, DisableRedirects: targetPolicy.DisableRedirects, Profile: string(profile), ProxyURL: proxyURL, ProxyKey: proxyKey, ProxyScheme: proxyScheme, HTTP2: s.http2Options(), HasTLSProfile: tlsProfile != nil, TLSSupportsHTTP2: tlsfingerprint.SupportsHTTP2(tlsProfile), Now: time.Now()})
	mode := policy.TransportMode
	if tlsProfile != nil {
		tlsProfile = resolveTLSFingerprintTransportProfile(egressprovider.ToTLSProfile(policy.TLSProfile), mode)
	}

	opts := httpclient.UpstreamRequestOptions{
		ProxyURL:   policy.ProxyURL,
		ProviderID: providerID,
		Isolation:  isolation,
		MaxClients: s.maxUpstreamClients(),
		IdleTTL:    s.clientIdleTTL(),
		Settings:   settings,
		TLSProfile: tlsProfile,
		// Requests that validate their host before sending also pin the direct
		// connection, so a later DNS answer cannot move them to a private address.
		PublicTargetsOnly: targetPolicy.RequiresHostValidation(),
		Protocol: httpclient.TransportProtocol{
			CacheVariant: policy.TransportMode,
			HTTP2:        policy.TransportMode == upstreamProtocolModeOpenAIH2,
			DisableHTTP2: policy.TransportMode == upstreamProtocolModeOpenAIH1 || policy.TransportMode == upstreamProtocolModeOpenAIH1Fallback,
		},
	}
	if policy.ValidateResolvedIP {
		opts.CheckRedirect = s.redirectChecker
	}
	opts.PrepareClient = func(client *http.Client) *http.Client {
		return httpClientWithGrokAccessDeniedFallback(httpClientForEgressPolicy(s, client, policy))
	}
	opts.ObserveResult = func(err error) {
		if err != nil {
			s.recordOpenAIHTTP2Failure(profile, mode, proxyKey, err)
		} else {
			s.recordOpenAIHTTP2Success(profile, mode, proxyKey)
		}
	}
	return opts, nil
}

// Do applies Grok identity headers, validates the destination, and exchanges
// through the resolved pool. Successful responses remain open for the caller;
// closing their bodies releases pool accounting after the caller finishes
// reading.
func (s *Client) Do(req *http.Request, proxyURL string, providerID int64, concurrency int) (*http.Response, error) {
	applyGrokCLIProxyHeaders(req)
	if err := s.validateRequestHost(req); err != nil {
		return nil, err
	}
	opts, err := s.transportOptions(req, proxyURL, providerID, concurrency, nil)
	if err != nil {
		return nil, err
	}
	return s.pool.Do(req, opts)
}

// DoWithTLS sends through the technical pool with a selected TLS fingerprint
// after destination validation. A nil profile or plain HTTP target delegates to
// Do with the configured proxy; callers close successful response bodies after
// reading.
func (s *Client) DoWithTLS(req *http.Request, proxyURL string, providerID int64, concurrency int, profile *tlsfingerprint.Profile) (*http.Response, error) {
	// Plain HTTP has no TLS handshake to fingerprint, so retain proxy routing and
	// ordinary validation by delegating the same request to Do.
	if profile == nil || (req != nil && req.URL != nil && strings.EqualFold(req.URL.Scheme, "http")) {
		return s.Do(req, proxyURL, providerID, concurrency)
	}
	applyGrokCLIProxyHeaders(req)
	if err := s.validateRequestHost(req); err != nil {
		return nil, err
	}
	opts, err := s.transportOptions(req, proxyURL, providerID, concurrency, profile)
	if err != nil {
		return nil, err
	}
	return s.pool.Do(req, opts)
}

// CloseIdleConnections asks a capable pool to release its idle transports. A
// test substitute without that method is left usable, and active responses
// retain their caller-owned body lifetime.
func (s *Client) CloseIdleConnections() {
	if closer, ok := s.pool.(interface {
		// CloseIdleConnections releases idle connections retained by the substituted pool.
		CloseIdleConnections()
	}); ok {
		closer.CloseIdleConnections()
	}
}

// http2Options converts normalized OpenAI HTTP/2 settings into egress policy
// options.
func (s *Client) http2Options() egress.HTTP2Options {
	v := s.resolveOpenAIHTTP2Settings()
	return egress.HTTP2Options{Enabled: v.enabled, AllowProxyFallbackToHTTP1: v.allowProxyFallbackToHTTP1, FallbackErrorThreshold: v.fallbackErrorThreshold, FallbackWindow: v.fallbackWindow, FallbackTTL: v.fallbackTTL}
}

// requestPolicy combines global validation with the request's public-host and
// redirect markers. A nil receiver or request contributes no corresponding
// settings, while marked requests retain their restrictions regardless of the
// global validation flag.
func (s *Client) requestPolicy(req *http.Request) egress.EgressPolicy {
	input := egress.RequestPolicyInput{}
	if s != nil {
		input.ValidateResolvedIP = s.shouldValidateResolvedIP()
	}
	if req != nil {
		input.DisableRedirects = upstream.HTTPUpstreamRedirectsDisabled(req.Context())
		input.PublicHostsOnly = upstream.HTTPUpstreamPublicHostsOnly(req.Context())
	}
	return egress.RequestPolicy(input)
}
