package httpclient

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/timing"
)

// upstream_pool_test.go combines fake transports with local HTTP servers to
// verify connection reuse, reservation release, protocol selection, and public
// destination enforcement. Timeout fixtures honor request cancellation so the synthetic timer's
// observable result can be checked after the underlying transport returns.

// httpClientSink retains constructed or cached client pointers so benchmark
// iterations publish their final client result in package-visible storage.
var httpClientSink *http.Client

// poolRoundTripFunc adapts controlled fixture responses to the HTTP transport interface.
type poolRoundTripFunc func(*http.Request) (*http.Response, error)

// blockingHeaderRoundTripper delays response return until request cancellation is observed.
type blockingHeaderRoundTripper struct{}

// TestUpstreamPoolActiveLimitAndCloseOnce holds a response in a one-entry
// pool before another provider tries admission. It requires capacity
// rejection, harmless repeated body close, and later admission after release.
func TestUpstreamPoolActiveLimitAndCloseOnce(t *testing.T) {
	pool := NewUpstreamPool()
	one := poolTestOptions(1, nil)
	one.MaxClients = 1
	held, err := pool.Do(poolTestRequest(t), one)
	require.NoError(t, err)
	two := poolTestOptions(2, nil)
	two.MaxClients = 1
	_, err = pool.Do(poolTestRequest(t), two)
	require.ErrorIs(t, err, ErrUpstreamClientLimitReached)
	require.NoError(t, held.Body.Close())
	require.NoError(t, held.Body.Close())
	next, err := pool.Do(poolTestRequest(t), two)
	require.NoError(t, err)
	require.NoError(t, next.Body.Close())
}

// TestUpstreamPoolFailureReleasesEntry observes a failed request before its
// reservation is released. It requires capacity rejection inside the observer
// and another provider's successful request after the failed attempt returns.
func TestUpstreamPoolFailureReleasesEntry(t *testing.T) {
	pool := NewUpstreamPool()
	opts := poolTestOptions(1, nil)
	opts.MaxClients = 1
	failure := errors.New("transport failed")
	observed := false
	opts.PrepareClient = func(c *http.Client) *http.Client {
		clone := *c
		clone.Transport = poolRoundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, failure
		})
		return &clone
	}
	// The result observer runs before failure release. Probe another provider
	// while this attempt still occupies the only cache entry to expose that ordering.
	opts.ObserveResult = func(err error) {
		require.ErrorIs(t, err, failure)
		observed = true
		probe := poolTestOptions(2, nil)
		probe.MaxClients = 1
		_, acquireErr := pool.acquire(probe)
		require.ErrorIs(t, acquireErr, ErrUpstreamClientLimitReached)
	}
	_, err := pool.Do(poolTestRequest(t), opts)
	require.ErrorIs(t, err, failure)
	require.True(t, observed)
	next := poolTestOptions(2, nil)
	next.MaxClients = 1
	resp, err := pool.Do(poolTestRequest(t), next)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestUpstreamPoolIsolationAndConfigurationChange compares reused transport
// pointers across isolation modes, normalized proxy spelling, provider
// changes, and header-timeout changes. It requires reuse or replacement
// according to the cache's configured identity rules.
func TestUpstreamPoolIsolationAndConfigurationChange(t *testing.T) {
	for _, isolation := range []string{"proxy", "provider", "provider_proxy"} {
		t.Run(isolation, func(t *testing.T) {
			pool := NewUpstreamPool()
			var first, again, other *http.Client
			run := func(id int64, proxy string, capture **http.Client) {
				opts := poolTestOptions(id, capture)
				opts.Isolation = isolation
				opts.ProxyURL = proxy
				resp, err := pool.Do(poolTestRequest(t), opts)
				require.NoError(t, err)
				require.NoError(t, resp.Body.Close())
			}
			run(1, "http://proxy-a:8080", &first)
			run(1, "http://proxy-a:8080/", &again)
			require.Same(t, first.Transport, again.Transport)
			run(2, "http://proxy-a:8080", &other)
			if isolation == "proxy" {
				require.Same(t, first.Transport, other.Transport)
			} else {
				require.NotSame(t, first.Transport, other.Transport)
			}
			run(1, "http://proxy-b:8080", &other)
			require.NotSame(t, first.Transport, other.Transport)
		})
	}
	pool := NewUpstreamPool()
	var first, second *http.Client
	opts := poolTestOptions(1, &first)
	resp, err := pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	opts = poolTestOptions(1, &second)
	opts.Settings.ResponseHeaderTimeout = time.Second
	resp, err = pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.NotSame(t, first.Transport, second.Transport)
}

// TestUpstreamPoolEvictsOldestIdle seeds two idle entries with different
// last-use times before a third acquisition. It requires the oldest entry to
// disappear while the two-entry limit remains satisfied.
func TestUpstreamPoolEvictsOldestIdle(t *testing.T) {
	pool := NewUpstreamPool()
	a, err := pool.acquire(poolTestOptions(1, nil))
	require.NoError(t, err)
	b, err := pool.acquire(poolTestOptions(2, nil))
	require.NoError(t, err)
	atomic.StoreInt64(&a.inFlight, 0)
	atomic.StoreInt64(&b.inFlight, 0)
	atomic.StoreInt64(&a.lastUsed, time.Now().Add(-2*time.Minute).UnixNano())
	atomic.StoreInt64(&b.lastUsed, time.Now().Add(-time.Minute).UnixNano())
	_, err = pool.acquire(poolTestOptions(3, nil))
	require.NoError(t, err)
	require.Len(t, pool.clients, 2)
	for _, entry := range pool.clients {
		require.NotSame(t, a, entry)
	}
}

// TestUpstreamPoolIdleTTLDoesNotEvictActive ages one reserved entry beyond
// idle TTL before acquiring another. It requires the active entry to remain
// cached, protecting reservations from time-based idle eviction.
func TestUpstreamPoolIdleTTLDoesNotEvictActive(t *testing.T) {
	pool := NewUpstreamPool()
	opts := poolTestOptions(1, nil)
	opts.IdleTTL = time.Second
	a, err := pool.acquire(opts)
	require.NoError(t, err)
	atomic.StoreInt64(&a.lastUsed, time.Now().Add(-2*time.Minute).UnixNano())
	opts.ProviderID = 2
	_, err = pool.acquire(opts)
	require.NoError(t, err)
	found := false
	for _, entry := range pool.clients {
		if entry == a {
			found = true
		}
	}
	require.True(t, found)
}

// TestUpstreamPoolConcurrentRelease reuses one provider entry across
// concurrent requests that each close twice. It requires all calls to succeed
// and a later different provider to enter the one-entry pool after release.
func TestUpstreamPoolConcurrentRelease(t *testing.T) {
	pool := NewUpstreamPool()
	var group sync.WaitGroup
	// Buffer every possible worker error before waiting. Each worker can report
	// a failed request or two close errors, so producers finish before serial assertions begin.
	errs := make(chan error, 64)
	for range 16 {
		group.Go(func() {
			opts := poolTestOptions(1, nil)
			opts.MaxClients = 1
			resp, err := pool.Do(poolTestRequest(t), opts)
			if err != nil {
				errs <- err
				return
			}
			if err = resp.Body.Close(); err != nil {
				errs <- err
			}
			if err = resp.Body.Close(); err != nil {
				errs <- err
			}
		})
	}
	// Every worker has completed its response closes before results are consumed
	// and a replacement provider probes whether any reservation is still held.
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	opts := poolTestOptions(2, nil)
	opts.MaxClients = 1
	resp, err := pool.Do(poolTestRequest(t), opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestBuildUpstreamTransportSetsDialTimeout constructs an ordinary upstream
// transport with fixed settings. It requires a configured network callback
// and the selected TLS handshake timeout, protecting phase setup outside the
// response-header wait.
func TestBuildUpstreamTransportSetsDialTimeout(t *testing.T) {
	settings := upstreamTestSettings()

	transport, err := buildUpstreamTransport(settings, nil, TransportProtocol{CacheVariant: "default"}, nil)
	require.NoError(t, err)
	require.NotNil(t, transport.DialContext, "DialContext 缺失会退化为无超时的零值 dialer")
	require.Equal(t, defaultUpstreamTLSHandshakeTimeout, transport.TLSHandshakeTimeout)
}

// TestNewUpstreamDialerHasBoundedTimeout inspects the default upstream
// net.Dialer. It requires its positive configured timeout and keepalive
// values, protecting network setup before header or body handling can begin.
func TestNewUpstreamDialerHasBoundedTimeout(t *testing.T) {
	dialer := newUpstreamDialer()

	require.Greater(t, dialer.Timeout, time.Duration(0), "建连超时必须有上限")
	require.Equal(t, defaultUpstreamDialTimeout, dialer.Timeout)
	require.Equal(t, defaultUpstreamDialKeepAlive, dialer.KeepAlive)
}

// TestBuildUpstreamTransportKeepsDialTimeoutWithHTTPProxy constructs an
// ordinary transport with HTTP proxy routing. It requires both Proxy and
// DialContext fields to remain configured, protecting network-budget
// availability after proxy selection.
func TestBuildUpstreamTransportKeepsDialTimeoutWithHTTPProxy(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:1080")
	require.NoError(t, err)

	transport, err := buildUpstreamTransport(upstreamTestSettings(), proxyURL, TransportProtocol{CacheVariant: "default"}, nil)
	require.NoError(t, err)
	require.NotNil(t, transport.Proxy)
	require.NotNil(t, transport.DialContext)
}

// TestBuildUpstreamTransportKeepsDialContextWithSOCKS5Proxy constructs an
// ordinary transport with SOCKS5H routing. It requires an installed
// DialContext field, protecting SOCKS setup while its bounded forward
// connector owns network dialing.
func TestBuildUpstreamTransportKeepsDialContextWithSOCKS5Proxy(t *testing.T) {
	proxyURL, err := url.Parse("socks5h://127.0.0.1:1080")
	require.NoError(t, err)

	transport, err := buildUpstreamTransport(upstreamTestSettings(), proxyURL, TransportProtocol{CacheVariant: "default"}, nil)
	require.NoError(t, err)
	require.NotNil(t, transport.DialContext)
}

// TestUpstreamDialerRespectsContextCancellation cancels a context before
// dialing a closed loopback listener. It requires a dialing error for that
// canceled context and unavailable local address, without classifying the
// returned error's identity.
func TestUpstreamDialerRespectsContextCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := listener.Addr().String()
	require.NoError(t, listener.Close())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	conn, err := newUpstreamDialer().DialContext(ctx, "tcp", addr)
	if conn != nil {
		_ = conn.Close()
	}
	require.Error(t, err, "已取消的 context 必须立即中止拨号")
}

// TestEnableOpenAIHTTP2KeepAlive_EnablesPingHealthCheck applies HTTP/2
// keepalive to an empty transport. It requires both protocol choices and the
// configured idle PING/send timeout fields, protecting construction of
// connection health checks.
func TestEnableOpenAIHTTP2KeepAlive_EnablesPingHealthCheck(t *testing.T) {
	tr := &http.Transport{}

	enableHTTP2KeepAlive(tr)
	require.NotNil(t, tr.HTTP2)
	require.Equal(t, http2ReadIdleTimeout, tr.HTTP2.SendPingTimeout)
	require.Equal(t, http2PingTimeout, tr.HTTP2.PingTimeout)
	requireHTTP2Configured(t, tr, "http2 必须已挂到底层 http.Transport 上")
}

// TestBuildUpstreamTransport_OpenAIH2_EnablesPingHealthCheck constructs the
// selected HTTP/2 protocol variant. It requires attempted HTTP/2 and both
// protocol choices, protecting eager negotiation configuration for that
// variant.
func TestBuildUpstreamTransport_OpenAIH2_EnablesPingHealthCheck(t *testing.T) {
	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, TransportProtocol{CacheVariant: "openai_h2", HTTP2: true}, nil)
	require.NoError(t, err)
	require.True(t, tr.ForceAttemptHTTP2, "openai_h2 必须启用 HTTP/2")
	requireHTTP2Configured(t, tr, "openai_h2 必须显式配置 http2 以启用 ReadIdleTimeout")
}

// TestBuildUpstreamTransport_NonOpenAIH2_NotEagerlyConfigured constructs the
// default variant without an HTTP/2 request flag. It requires nil explicit
// protocol and h2 callback fields, protecting the ordinary transport's
// construction state.
func TestBuildUpstreamTransport_NonOpenAIH2_NotEagerlyConfigured(t *testing.T) {
	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, TransportProtocol{CacheVariant: "default"}, nil)
	require.NoError(t, err)
	require.Nil(t, tr.Protocols, "default 模式不应在构建期主动配置 http2 keepalive")
	require.Nil(t, tr.TLSNextProto["h2"], "default 模式不应在构建期主动配置 http2 keepalive")
}

// TestBuildUpstreamTransport_OpenAIH2_NegotiatesHTTP2 trusts a local TLS
// server advertising HTTP/2 and sends one request. It requires success,
// closed response cleanup, and protocol major two, protecting actual
// negotiation for the selected variant.
func TestBuildUpstreamTransport_OpenAIH2_NegotiatesHTTP2(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), nil, TransportProtocol{CacheVariant: "openai_h2", HTTP2: true}, nil)
	require.NoError(t, err)
	defer tr.CloseIdleConnections()
	require.NotNil(t, tr.TLSClientConfig)
	roots := x509.NewCertPool()
	roots.AddCert(srv.Certificate())
	tr.TLSClientConfig.RootCAs = roots

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, nil)
	require.NoError(t, err)
	resp, err := tr.RoundTrip(req)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, 2, resp.ProtoMajor, "openai_h2 必须协商到 HTTP/2")
}

// TestBuildUpstreamTransport_OpenAIH2_WithHTTPProxy_EnablesKeepAlive
// constructs HTTP/2 selection together with an HTTP proxy. It requires
// attempted HTTP/2, configured protocol choices, and retained proxy routing
// before any real network exchange.
func TestBuildUpstreamTransport_OpenAIH2_WithHTTPProxy_EnablesKeepAlive(t *testing.T) {
	proxyURL, err := url.Parse("http://127.0.0.1:8080")
	require.NoError(t, err)

	tr, err := buildUpstreamTransport(http2KeepAliveTestPoolSettings(), proxyURL, TransportProtocol{CacheVariant: "openai_h2", HTTP2: true}, nil)
	require.NoError(t, err)
	require.True(t, tr.ForceAttemptHTTP2)
	requireHTTP2Configured(t, tr, "经代理的 openai_h2 也必须启用 http2 keepalive")
	require.NotNil(t, tr.Proxy, "HTTP 代理仍须通过 Transport.Proxy 生效")
}

// BenchmarkHTTPUpstreamProxyClient compares repeated transport construction
// with assignments of an already cached client pointer. The reuse case
// excludes warmup and pool lookup, so its reported work describes pointer
// assignment rather than acquisition performance.
func BenchmarkHTTPUpstreamProxyClient(b *testing.B) {
	svc := NewUpstreamPool()
	settings := UpstreamSettings{
		MaxIdleConns:          240,
		MaxIdleConnsPerHost:   120,
		MaxConnsPerHost:       240,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 300 * time.Second,
	}

	proxyURL := "http://127.0.0.1:8080"
	b.ReportAllocs()

	b.Run("新建", func(b *testing.B) {
		parsedProxy, err := url.Parse(proxyURL)
		if err != nil {
			b.Fatalf("解析代理地址失败: %v", err)
		}
		for range b.N {
			// Each iteration includes allocation of a transport.
			transport, err := buildUpstreamTransport(settings, parsedProxy, TransportProtocol{CacheVariant: "default"}, nil)
			if err != nil {
				b.Fatalf("创建 Transport 失败: %v", err)
			}
			httpClientSink = &http.Client{
				Transport: transport,
			}
		}
	})

	b.Run("复用", func(b *testing.B) {
		// Populate the cache before timing repeated assignments.
		entry, err := svc.acquire(UpstreamRequestOptions{
			ProxyURL:   proxyURL,
			ProviderID: 1,
			Isolation:  "provider_proxy",
			MaxClients: 5000,
			IdleTTL:    15 * time.Minute,
			Settings:   settings,
		})
		if err != nil {
			b.Fatalf("getOrCreateClient: %v", err)
		}
		client := entry.client
		b.ResetTimer() // Exclude cache warmup.
		for range b.N {
			// This measures assignment of a cached pointer, not a pool lookup.
			httpClientSink = client
		}
	})
}

// TestUpstreamPoolRedirectPolicyIsPerRequest alternates allowed and blocked
// redirects while reusing one transport. It requires independent client
// copies, expected target hits, retained transport identity, and released
// failure occupancy for another provider.
func TestUpstreamPoolRedirectPolicyIsPerRequest(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	blocked := errors.New("redirect blocked for this request")
	var previous *http.Client
	for _, step := range []struct {
		name  string // case display name
		block bool   // reject this step redirect
	}{
		{name: "default"},
		{name: "block_after_default", block: true},
		{name: "clear_after_block"},
		{name: "block_again", block: true},
	} {
		t.Run(step.name, func(t *testing.T) {
			opts := UpstreamRequestOptions{Isolation: "provider", ProviderID: 1, MaxClients: 1}
			if step.block {
				opts.CheckRedirect = func(*http.Request, []*http.Request) error {
					return blocked
				}
			}
			var current *http.Client
			opts.PrepareClient = func(client *http.Client) *http.Client {
				current = client
				return client
			}
			before := hits.Load()
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			require.NoError(t, err)
			resp, err := pool.Do(req, opts)
			if resp != nil {
				require.NoError(t, resp.Body.Close())
			}
			if step.block {
				require.ErrorIs(t, err, blocked)
				require.Equal(t, before, hits.Load(), "被拒绝的重定向不能访问目标")
			} else {
				require.NoError(t, err)
				require.Equal(t, http.StatusNoContent, resp.StatusCode)
				require.Equal(t, before+1, hits.Load())
			}
			if previous != nil {
				require.NotSame(t, previous, current, "每个请求应有独立的客户端策略")
				require.Same(t, previous.Transport, current.Transport, "切换策略仍复用同一 transport")
			}
			previous = current
		})
	}

	// A redirect failure must release the slot so a different provider can use the one-entry pool.
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := pool.Do(req, UpstreamRequestOptions{Isolation: "provider", ProviderID: 2, MaxClients: 1})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestUpstreamPoolPrepareClientOverridesRequestPolicy replaces a request
// redirect policy in PrepareClient with last-response behavior. It requires
// the initial redirect response, zero target hits, then default redirect
// success on a later request.
func TestUpstreamPoolPrepareClientOverridesRequestPolicy(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	opts := UpstreamRequestOptions{
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return errors.New("base policy must be overridden")
		},
		PrepareClient: func(client *http.Client) *http.Client {
			require.NotNil(t, client.CheckRedirect, "基础回调应在适配前设置")
			client.CheckRedirect = func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			}
			return client
		},
	}
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err := pool.Do(req, opts)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusFound, resp.StatusCode)
	require.Zero(t, hits.Load())

	// The next request should use the default redirect policy.
	req, err = http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
	require.NoError(t, err)
	resp, err = pool.Do(req, UpstreamRequestOptions{})
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	require.Equal(t, http.StatusNoContent, resp.StatusCode)
	require.Equal(t, int64(1), hits.Load())
}

// TestUpstreamPoolConcurrentRedirectPolicies starts twenty-four requests
// sharing transport storage with alternating redirect rules. It requires
// successful allowed requests, matching blocked errors, and twelve target
// hits after all workers finish.
func TestUpstreamPoolConcurrentRedirectPolicies(t *testing.T) {
	target, hits := redirectTestServer(t)
	pool := NewUpstreamPool()
	blocked := errors.New("redirect blocked")
	const requests = 24
	var group sync.WaitGroup
	// Hold workers behind one start gate and buffer one possible error per
	// worker. After releasing the gate, join every worker before closing the
	// results channel and counting target hits.
	errs := make(chan error, requests)
	start := make(chan struct{})
	for i := range requests {
		group.Go(func() {
			<-start
			opts := UpstreamRequestOptions{}
			if i%2 == 0 {
				opts.CheckRedirect = func(*http.Request, []*http.Request) error {
					return blocked
				}
			}
			req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, target, nil)
			if err != nil {
				errs <- err
				return
			}
			resp, err := pool.Do(req, opts)
			if resp != nil {
				if closeErr := resp.Body.Close(); closeErr != nil {
					errs <- closeErr
					return
				}
			}
			if i%2 == 0 {
				if !errors.Is(err, blocked) {
					errs <- fmt.Errorf("请求 %d 应阻断重定向，实际错误：%v", i, err)
				}
			} else if err != nil {
				errs <- err
			}
		})
	}
	close(start)
	group.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int64(requests/2), hits.Load())
}

// TestTLSFingerprintHTTPSProxyFallsBackWithoutBypassingProxy constructs
// fingerprint selection with an HTTPS proxy carrying credentials. It requires
// an ordinary transport, retained exact proxy URL, and absent custom
// DialTLSContext, protecting proxy retention during that selection.
func TestTLSFingerprintHTTPSProxyFallsBackWithoutBypassingProxy(t *testing.T) {
	proxyURL, err := url.Parse("https://user:pass@proxy.example:8443")
	require.NoError(t, err)
	roundTripper, err := buildUpstreamTransportWithTLSFingerprint(
		UpstreamSettings{},
		proxyURL,
		&tlsfingerprint.Profile{Name: "test"},
		TransportProtocol{CacheVariant: "default"},
		nil,
	)
	require.NoError(t, err)
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)
	require.NotNil(t, transport.Proxy)
	require.Nil(t, transport.DialTLSContext)
	req := &http.Request{URL: &url.URL{Scheme: "https", Host: "upstream.example"}}
	resolved, err := transport.Proxy(req)
	require.NoError(t, err)
	require.Equal(t, "https://user:pass@proxy.example:8443", resolved.String())
}

// TestResponseHeaderTimeoutRoundTripperTimesOut uses a fake base that waits
// for context cancellation before returning. It requires the synthetic
// timeout error and completion within one second, checking the base's
// cooperation with the timer.
func TestResponseHeaderTimeoutRoundTripperTimesOut(t *testing.T) {
	transport := &responseHeaderTimeoutRoundTripper{
		base:    blockingHeaderRoundTripper{},
		timeout: 10 * time.Millisecond,
	}
	req, err := http.NewRequest(http.MethodGet, "https://example.com/v1/responses", nil)
	require.NoError(t, err)

	startedAt := time.Now()
	resp, err := transport.RoundTrip(req)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}

	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout awaiting response headers")
	require.Less(t, time.Since(startedAt), time.Second)
}

// upstreamTestSettings supplies fixed upstream connection counts and phase
// durations for fixtures.
func upstreamTestSettings() UpstreamSettings {
	return UpstreamSettings{
		MaxIdleConns:          240,
		MaxIdleConnsPerHost:   120,
		MaxConnsPerHost:       240,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 300 * time.Second,
	}
}

// RoundTrip delegates one pool-fixture request to its test-supplied transport
// callback.
func (f poolRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

// poolTestOptions supplies fixed pool configuration and a fake successful
// response transport. An optional captured pointer receives the preadaptation
// client copy so tests can compare shared transport identity while each
// returned adapted client owns its fake wrapper.
func poolTestOptions(id int64, captured **http.Client) UpstreamRequestOptions {
	return UpstreamRequestOptions{ProviderID: id, Isolation: "provider_proxy", MaxClients: 2, IdleTTL: 15 * time.Minute, Settings: upstreamTestSettings(), PrepareClient: func(client *http.Client) *http.Client {
		if captured != nil {
			*captured = client
		}
		clone := *client
		clone.Transport = poolRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader("response")),
				Request:    req,
			}, nil
		})
		return &clone
	}}
}

// poolTestRequest creates a fixture GET using t's context and fails t on
// construction error. The fixed example target is replaced by
// poolTestOptions' fake transport before these pool behavior checks execute.
func poolTestRequest(t *testing.T) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.com/", nil)
	require.NoError(t, err)
	return req
}

// http2KeepAliveTestPoolSettings supplies fixed pooling and header timing for
// HTTP/2 fixtures.
func http2KeepAliveTestPoolSettings() UpstreamSettings {
	return UpstreamSettings{
		MaxIdleConns:          10,
		MaxIdleConnsPerHost:   5,
		MaxConnsPerHost:       10,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: time.Minute,
	}
}

// requireHTTP2Configured requires non-nil protocol choices with HTTP/1 and
// HTTP/2 enabled. It reports failures through t using msg so transport tests
// can identify the construction context for the assertion.
func requireHTTP2Configured(t *testing.T, tr *http.Transport, msg string) {
	t.Helper()
	require.NotNil(t, tr.Protocols, msg)
	require.True(t, tr.Protocols.HTTP1(), msg)
	require.True(t, tr.Protocols.HTTP2(), msg)
}

// redirectTestServer starts a local redirect-to-target fixture and registers
// its cleanup with t. It returns the start URL and an atomic counter
// incremented only after a request reaches the target path.
func redirectTestServer(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	hits := new(atomic.Int64)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	return server.URL + "/start", hits
}

// RoundTrip waits for req's context cancellation and returns that context
// error with no response. Tests use it to make timeout completion depend on
// the wrapper's cancellation signal rather than remote I/O.
func (blockingHeaderRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// Keep this base call outstanding until it observes cancellation, so the
	// timeout wrapper must release the waiting call before it can return its synthetic error.
	<-req.Context().Done()
	return nil, req.Context().Err()
}

// TestUpstreamPoolInvokesRequestTimingObserver validates the shared exchange
// observation hook after transport response creation. Reading and closing the
// response still release pool capacity exactly as ordinary forwarding does.
func TestUpstreamPoolInvokesRequestTimingObserver(t *testing.T) {
	pool := NewUpstreamPool()
	var observed *http.Response
	ctx, _ := timing.WithStages(t.Context(), time.Now(), func(_ context.Context, resp *http.Response) { observed = resp })
	req := poolTestRequest(t).WithContext(ctx)
	resp, err := pool.Do(req, poolTestOptions(1, nil))
	require.NoError(t, err)
	require.Same(t, resp, observed)
	require.Equal(t, 1, telemetry.RequestAttemptFromResponse(resp).Number)
	_, err = io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
}

// TestUpstreamPoolFailureFinishesOwningMediaAttempt completes a network failure
// or cancellation after a newer same-provider exchange. The original error stays
// recognizable, and only the failed dispatch changes its diagnostic outcome.
func TestUpstreamPoolFailureFinishesOwningMediaAttempt(t *testing.T) {
	for _, canceled := range []bool{false, true} {
		var saved telemetry.RequestRecord
		root := telemetry.WithRequestCapture(t.Context(), telemetry.RequestRecord{RequestID: "local"}, func(r telemetry.RequestRecord) { saved = r })
		root = telemetry.WithRequestProvider(root, 7, "X-Custom")
		firstContext, cancel := context.WithCancel(root)
		defer cancel()
		started := make(chan struct{})
		releaseFirst := make(chan struct{})
		release := sync.OnceFunc(func() { close(releaseFirst) })
		defer release()
		transportErr := errors.New("fixture network failure")
		opts := poolTestOptions(7, nil)
		opts.PrepareClient = func(client *http.Client) *http.Client {
			client.Transport = poolRoundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.URL.Path == "/first" {
					close(started)
					// Hold the original transport until replacement headers have arrived,
					// then release failure or cancellation to exercise delayed ownership.
					select {
					case <-req.Context().Done():
						return nil, req.Context().Err()
					case <-releaseFirst:
						return nil, transportErr
					}
				}
				return &http.Response{
					StatusCode: 200,
					Header:     http.Header{"X-Custom": {"second"}},
					Body:       io.NopCloser(strings.NewReader("ok")),
					Request:    req,
				}, nil
			})
			return client
		}
		pool := NewUpstreamPool()
		first, err := http.NewRequestWithContext(firstContext, http.MethodGet, "https://example.com/first", nil)
		require.NoError(t, err)
		done := make(chan error, 1)
		go func() {
			_, failure := pool.Do(first, opts)
			done <- failure
		}()
		<-started
		second, err := http.NewRequestWithContext(root, http.MethodGet, "https://example.com/second", nil)
		require.NoError(t, err)
		response, err := pool.Do(second, opts)
		require.NoError(t, err)
		require.NoError(t, response.Body.Close())
		expected := transportErr
		if canceled {
			expected = context.Canceled
			cancel()
		} else {
			release()
		}
		require.ErrorIs(t, <-done, expected)
		require.Len(t, saved.Attempts, 2)
		require.Equal(t, "failed", saved.Attempts[0].Outcome)
		require.Empty(t, saved.Attempts[0].RequestID)
		require.Equal(t, "running", saved.Attempts[1].Outcome)
		require.Equal(t, "second", saved.Attempts[1].RequestID)
	}
}

// TestUpstreamPoolPublicTargetsUseSeparatePinnedEntry sends the same provider's
// unrestricted and public-only requests through one pool. The restricted request
// must get its own entry, so it cannot reuse a connection dialed without checks,
// and must be refused when the host resolves to the loopback server.
func TestUpstreamPoolPublicTargetsUseSeparatePinnedEntry(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)
	pool := NewUpstreamPool()
	options := UpstreamRequestOptions{ProviderID: 7, Isolation: "provider", Settings: upstreamTestSettings()}
	open, err := http.NewRequest(http.MethodGet, server.URL, nil)
	require.NoError(t, err)
	response, err := pool.Do(open, options)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	options.PublicTargetsOnly = true
	restricted, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:"+parsed.Port(), nil)
	require.NoError(t, err)
	_, err = pool.Do(restricted, options)

	require.ErrorContains(t, err, "is not allowed")
	require.Equal(t, int64(1), hits.Load())
	require.Len(t, pool.clients, 2)
}

// TestUpstreamPoolPublicTargetsKeepProxyRoute selects a loopback HTTP proxy for a
// public-only request. The proxy's own private address must stay reachable, since
// the pinning dialer belongs to direct routes only.
func TestUpstreamPoolPublicTargetsKeepProxyRoute(t *testing.T) {
	var hits atomic.Int64
	proxyServer := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(proxyServer.Close)
	pool := NewUpstreamPool()
	request, err := http.NewRequest(http.MethodGet, "http://images.example.com/a.png", nil)
	require.NoError(t, err)

	response, err := pool.Do(request, UpstreamRequestOptions{
		ProxyURL: proxyServer.URL, ProviderID: 7, Isolation: "provider",
		Settings: upstreamTestSettings(), PublicTargetsOnly: true,
	})

	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	require.Equal(t, int64(1), hits.Load())
}

// TestFingerprintTransportUsesPinnedDirectDial builds a direct fingerprint
// transport with the pinning connector. Its TLS dial must fail on the private
// answer before any network connection or handshake starts.
func TestFingerprintTransportUsesPinnedDirectDial(t *testing.T) {
	roundTripper, err := buildUpstreamTransportWithTLSFingerprint(
		UpstreamSettings{},
		nil,
		&tlsfingerprint.Profile{Name: "test"},
		TransportProtocol{CacheVariant: "default"},
		publicTargetDialContext(newUpstreamDialer().DialContext, net.DefaultResolver.LookupIP),
	)
	require.NoError(t, err)
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)

	_, err = transport.DialTLSContext(context.Background(), "tcp", "localhost:443")

	require.ErrorContains(t, err, "is not allowed")
}

// TestFingerprintTransportRejectsPrivateHTTPRedirect follows a synthetic HTTPS
// response to a real local HTTP server. Pre-request validation is assumed to
// have passed; connection-time checks must still reject the redirected target.
func TestFingerprintTransportRejectsPrivateHTTPRedirect(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(server.Close)
	roundTripper, err := buildUpstreamTransportWithTLSFingerprint(
		upstreamTestSettings(), nil, &tlsfingerprint.Profile{Name: "test"},
		TransportProtocol{CacheVariant: "default"},
		publicTargetDialContext(newUpstreamDialer().DialContext, net.DefaultResolver.LookupIP),
	)
	require.NoError(t, err)
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)
	t.Cleanup(transport.CloseIdleConnections)
	// Replace only the initial HTTPS response; net/http still follows its
	// redirect through this fingerprint transport's actual ordinary HTTP connector.
	transport.RegisterProtocol("https", poolRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusFound,
			Header:     http.Header{"Location": []string{server.URL}},
			Body:       io.NopCloser(strings.NewReader("")),
			Request:    request,
		}, nil
	}))
	client := &http.Client{Transport: transport, Timeout: time.Second}
	response, err := client.Get("https://public.example/start")
	if response != nil {
		require.NoError(t, response.Body.Close())
	}
	require.ErrorContains(t, err, "is not allowed")
	require.Zero(t, hits.Load())
}
