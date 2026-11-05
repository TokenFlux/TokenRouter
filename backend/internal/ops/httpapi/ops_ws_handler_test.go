package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/stretchr/testify/require"
)

// TestOpsWSHelpers mixes one valid network prefix with invalid text, strips a
// normal host port, and checks matching addresses. It protects those supplied
// helper cases without asserting complete proxy or Origin policy behavior.
func TestOpsWSHelpers(t *testing.T) {
	prefixes, invalid := parseTrustedProxyList("10.0.0.0/8,invalid")
	require.Len(t, prefixes, 1)
	require.Len(t, invalid, 1)

	host := hostWithoutPort("example.com:443")
	require.Equal(t, "example.com", host)

	addr := netip.MustParseAddr("10.0.0.1")
	require.True(t, isAddrInTrustedProxies(addr, prefixes))
	require.False(t, isAddrInTrustedProxies(netip.MustParseAddr("192.168.0.1"), prefixes))
}

// TestOpsWSBrandNegotiation offers legacy, current, and mixed administrator
// subprotocol names to a local upgrade server. It checks legacy compatibility
// and current-name preference; jwt.fixture is not echoed, and this fixture
// performs no JWT validation.
func TestOpsWSBrandNegotiation(t *testing.T) {
	handler := NewOpsHandler(nil, OpsWSOptions{OriginPolicy: OriginPolicyPermissive})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := handler.wsUpgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		// Release the server side immediately after handshake; these cases inspect
		// protocol selection and require no traffic-push loop.
		_ = conn.Close()
	}))
	// Stop accepting local handshakes after the cases. The server handler closes
	// each upgraded socket separately, and client cases close their returned
	// connections.
	defer server.Close()
	for _, protocols := range [][]string{
		{"sub2api-admin", "jwt.fixture"},
		{"tokenrouter-admin", "jwt.fixture"},
		{"sub2api-admin", "tokenrouter-admin", "jwt.fixture"},
	} {
		dialer := websocket.Dialer{Subprotocols: protocols}
		// Open each case through a real local handshake to observe Gorilla's selected
		// response subprotocol, then close the resulting connection.
		conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
		require.NoError(t, err)
		want := "tokenrouter-admin"
		if len(protocols) == 2 && protocols[0] == "sub2api-admin" {
			want = "sub2api-admin"
		}
		require.Equal(t, want, conn.Subprotocol())
		require.NoError(t, conn.Close())
	}
}

// TestOpsWebSocketPoliciesAreInstanceOwned constructs opposite trust and origin policies.
// Each handler must retain its own admission limits and copied proxy ranges,
// so configuring another handler cannot change an existing handshake decision.
func TestOpsWebSocketPoliciesAreInstanceOwned(t *testing.T) {
	addresses := []string{"127.0.0.0/8"}
	trusted := NewOpsHandler(nil, OpsWSOptions{TrustProxy: true, TrustedProxies: addresses, OriginPolicy: OriginPolicyStrict, MaxConns: 25, MaxConnsPerIP: 3})
	direct := NewOpsHandler(nil, OpsWSOptions{OriginPolicy: OriginPolicyPermissive, MaxConns: 8})
	addresses[0] = "10.0.0.0/8"
	request := httptest.NewRequest(http.MethodGet, "https://internal.test/ops", nil)
	request.RemoteAddr = "127.0.0.1:1234"
	request.Header.Set("Origin", "https://public.test")
	request.Header.Set("X-Forwarded-Host", "public.test")
	request.Header.Set("X-Forwarded-For", "203.0.113.9")
	require.True(t, isAllowedOpsWSOrigin(request, trusted.wsProxy))
	require.False(t, isAllowedOpsWSOrigin(request, direct.wsProxy))
	require.Equal(t, "203.0.113.9", requestClientIP(request, trusted.wsProxy))
	require.Equal(t, "127.0.0.1", requestClientIP(request, direct.wsProxy))
	request.Header.Del("Origin")
	require.False(t, isAllowedOpsWSOrigin(request, trusted.wsProxy))
	require.True(t, isAllowedOpsWSOrigin(request, direct.wsProxy))
	require.EqualValues(t, 25, trusted.wsLimits.MaxConns)
	require.EqualValues(t, 3, trusted.wsLimits.MaxConnsPerIP)
	require.EqualValues(t, 8, direct.wsLimits.MaxConns)
}
