package httpapi

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/coder/websocket"
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
// subprotocol names to a local upgrade server. It checks legacy compatibility,
// server preference for tokenrouter-admin, Server-Timing header propagation,
// and HTTP 403 rejection when Origin mismatches.
func TestOpsWSBrandNegotiation(t *testing.T) {
	handler := NewOpsHandler(nil, OpsWSOptions{OriginPolicy: OriginPolicyPermissive})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := handler.acceptOpsWebSocket(w, r, http.Header{"Server-Timing": []string{"app;dur=1.2"}})
		if err != nil {
			return
		}
		// Release the server side immediately after handshake; these cases inspect
		// protocol selection and response headers without a traffic-push loop.
		_ = conn.Close(websocket.StatusNormalClosure, "")
	}))
	// Stop accepting local handshakes after the cases. The server handler closes
	// each upgraded socket separately, and client cases close their returned
	// connections.
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	for _, protocols := range [][]string{
		{"sub2api-admin", "jwt.fixture"},
		{"tokenrouter-admin", "jwt.fixture"},
		{"sub2api-admin", "tokenrouter-admin", "jwt.fixture"},
	} {
		// Open each case through a real local handshake to observe the selected
		// response subprotocol and Server-Timing header, then close the socket.
		conn, resp, err := websocket.Dial(t.Context(), wsURL, &websocket.DialOptions{
			Subprotocols: protocols,
		})
		require.NoError(t, err)
		want := "tokenrouter-admin"
		if len(protocols) == 2 && protocols[0] == "sub2api-admin" {
			want = "sub2api-admin"
		}
		require.Equal(t, want, conn.Subprotocol())
		require.Equal(t, "app;dur=1.2", resp.Header.Get("Server-Timing"))
		_ = conn.CloseNow()
	}

	// Verify mismatched Origin headers are rejected with HTTP 403 Forbidden before upgrade.
	badConn, badResp, badErr := websocket.Dial(t.Context(), wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": []string{"https://evil.example"}},
	})
	require.Error(t, badErr)
	if badConn != nil {
		_ = badConn.CloseNow()
	}
	require.NotNil(t, badResp)
	require.Equal(t, http.StatusForbidden, badResp.StatusCode)
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
