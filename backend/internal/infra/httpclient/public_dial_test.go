package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient/tlsfingerprint"
)

// Public-dial fixtures replace the package DNS lookup so a test can return a
// different answer for a host without network access. These tests run serially
// because the lookup is shared package state.

// stubTargetLookup makes lookupTargetIPs return answers for the test duration,
// restoring production DNS afterwards. Each string must be a valid IP literal.
func stubTargetLookup(t *testing.T, answers ...string) {
	t.Helper()
	original := lookupTargetIPs
	t.Cleanup(func() { lookupTargetIPs = original })
	ips := make([]net.IP, 0, len(answers))
	for _, answer := range answers {
		ips = append(ips, net.ParseIP(answer))
	}
	lookupTargetIPs = func(context.Context, string) ([]net.IP, error) {
		return ips, nil
	}
}

// TestPublicTargetDialContextConnectsToCheckedLiteral resolves a host to a public
// address. The connector must receive that literal and the original port, so the
// connection cannot depend on a second DNS answer.
func TestPublicTargetDialContextConnectsToCheckedLiteral(t *testing.T) {
	stubTargetLookup(t, "93.184.216.34")
	var dialed string
	dial := publicTargetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
		dialed = address
		client, server := net.Pipe()
		t.Cleanup(func() { _ = server.Close() })
		return client, nil
	})

	conn, err := dial(context.Background(), "tcp", "images.example.com:443")

	require.NoError(t, err)
	require.NoError(t, conn.Close())
	require.Equal(t, "93.184.216.34:443", dialed)
}

// TestPublicTargetDialContextRejectsAnyNonpublicAnswer returns answers containing
// a private or reserved address. The connector must not run, even when another
// answer in the same lookup is public.
func TestPublicTargetDialContextRejectsAnyNonpublicAnswer(t *testing.T) {
	for _, answers := range [][]string{
		{"127.0.0.1"},
		{"169.254.169.254"},
		{"93.184.216.34", "10.0.0.1"},
		{"100.64.0.1"},
	} {
		stubTargetLookup(t, answers...)
		dial := publicTargetDialContext(func(context.Context, string, string) (net.Conn, error) {
			t.Fatal("connector must not run for a rejected host")
			return nil, nil
		})

		_, err := dial(context.Background(), "tcp", "rebind.example.com:443")

		require.ErrorContains(t, err, "is not allowed")
	}
}

// TestPublicTargetDialContextTriesLaterAddresses fails the first checked address.
// The connector must then try the next literal, and an all-failed lookup must
// report the connection errors rather than a false success.
func TestPublicTargetDialContextTriesLaterAddresses(t *testing.T) {
	stubTargetLookup(t, "93.184.216.34", "93.184.216.35")
	var attempts []string
	var attemptsMu sync.Mutex
	dial := publicTargetDialContext(func(_ context.Context, _, address string) (net.Conn, error) {
		attemptsMu.Lock()
		attempts = append(attempts, address)
		attemptsMu.Unlock()
		return nil, errors.New("refused " + address)
	})

	_, err := dial(context.Background(), "tcp", "images.example.com:443")

	require.ErrorContains(t, err, "refused 93.184.216.35:443")
	attemptsMu.Lock()
	defer attemptsMu.Unlock()
	require.ElementsMatch(t, []string{"93.184.216.34:443", "93.184.216.35:443"}, attempts)
}

// TestPublicTargetDialContextSkipsBlockedAddress stalls the first checked literal.
// A healthy alternative must connect before that attempt finishes, cancelling the
// blocked connection rather than waiting for its individual network timeout.
func TestPublicTargetDialContextSkipsBlockedAddress(t *testing.T) {
	stubTargetLookup(t, "93.184.216.34", "93.184.216.35")
	stopped := make(chan struct{})
	peer, server := net.Pipe()
	t.Cleanup(func() { _ = server.Close() })
	dial := publicTargetDialContext(func(ctx context.Context, _, address string) (net.Conn, error) {
		if address == "93.184.216.34:443" {
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		}
		return peer, nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dial(ctx, "tcp", "images.example.com:443")
	require.NoError(t, err)
	require.NoError(t, conn.Close())
	select {
	case <-stopped:
	case <-ctx.Done():
		t.Fatal("losing connection was not cancelled")
	}
}

// TestPublicTargetDialContextPreservesDeadline cancels a request before connecting.
// The shared budget must honor that earlier deadline and never wait for the
// wrapper's longer timeout while a connection attempt is blocked.
func TestPublicTargetDialContextPreservesDeadline(t *testing.T) {
	stubTargetLookup(t, "93.184.216.34")
	dial := publicTargetDialContext(func(ctx context.Context, _, _ string) (net.Conn, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := dial(ctx, "tcp", "images.example.com:443")
	require.ErrorIs(t, err, context.DeadlineExceeded)
}

// TestGetClientPinsDirectValidatedDial reproduces a host passing the pre-request
// check and then resolving to a loopback server when dialing. The request must
// fail at the dialer without reaching that server.
func TestGetClientPinsDirectValidatedDial(t *testing.T) {
	var hits atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hits.Add(1)
	}))
	t.Cleanup(server.Close)
	originalValidate := validateResolvedIP
	t.Cleanup(func() { validateResolvedIP = originalValidate })
	validateResolvedIP = func(string) error { return nil }
	stubTargetLookup(t, "127.0.0.1")
	client, err := GetClient(Options{ValidateResolvedIP: true, Timeout: 7 * time.Second})
	require.NoError(t, err)
	parsed, err := url.Parse(server.URL)
	require.NoError(t, err)

	response, err := client.Get("http://rebind.example.com:" + parsed.Port())
	if response != nil {
		_ = response.Body.Close()
	}

	require.ErrorContains(t, err, "is not allowed")
	require.Zero(t, hits.Load())
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
	stubTargetLookup(t, "127.0.0.1")
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
	stubTargetLookup(t, "127.0.0.1")
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
	stubTargetLookup(t, "127.0.0.1")
	roundTripper, err := buildUpstreamTransportWithTLSFingerprint(
		UpstreamSettings{},
		nil,
		&tlsfingerprint.Profile{Name: "test"},
		TransportProtocol{CacheVariant: "default"},
		publicTargetDialContext(newUpstreamDialer().DialContext),
	)
	require.NoError(t, err)
	transport, ok := roundTripper.(*http.Transport)
	require.True(t, ok)

	_, err = transport.DialTLSContext(context.Background(), "tcp", "rebind.example.com:443")

	require.ErrorContains(t, err, "is not allowed")
}
