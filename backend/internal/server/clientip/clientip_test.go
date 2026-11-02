package clientip

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// TestGetTrustedClientIPUsesGinClientIP
// supplies forwarding headers from an untrusted peer.
// With Gin forwarding trust disabled, the returned address must be the direct
// connection's normalized IP despite the three header values.
func TestGetTrustedClientIPUsesGinClientIP(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))

	r.GET("/t", func(c *gin.Context) {
		c.String(200, GetTrustedClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "9.9.9.9", w.Body.String())
}

// TestGetClientIPPreservesLegacyDockerForwardedHeaders
// supplies private proxy addresses and a later XFF candidate.
// Compatibility resolution must select the forwarded external address even
// when Gin itself trusts no forwarding peers for its ClientIP result.
func TestGetClientIPPreservesLegacyDockerForwardedHeaders(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, true, nil)
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "192.168.32.1:12345"
	req.Header.Set("X-Forwarded-For", "10.0.0.2, 203.0.113.42")
	req.Header.Set("X-Real-IP", "192.168.32.1")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "203.0.113.42", w.Body.String())
}

// TestGetSecurityClientIPSwitchEnabledUsesLegacyHeaders
// enables compatibility with Gin trust disabled.
// The security resolver must use X-Real-IP under that mode, showing the switch's
// observable effect on the address returned to the handler.
func TestGetSecurityClientIPSwitchEnabledUsesLegacyHeaders(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		c.String(200, GetSecurityClientIP(c, true))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "1.2.3.4", w.Body.String())
}

// TestGetSecurityClientIPCustomHeaderPrecedenceAndFallback
// combines custom sources, built-ins, and invalid values.
// The captured request mode must control selection, with configured order,
// nonprivate preference, private fallback, and direct-peer recovery matching each case.
func TestGetSecurityClientIPCustomHeaderPrecedenceAndFallback(t *testing.T) {
	tests := []struct {
		name           string            // selection scenario for subtest output
		trustForward   bool              // captured forwarding compatibility mode
		headers        []string          // configured custom-header lookup order
		requestHeaders map[string]string // forwarding values supplied by fixture
		want           string            // expected selected client address
	}{
		{
			name:         "configured order precedes built-ins",
			trustForward: true,
			headers:      []string{"X-CDN-First", "X-CDN-Second"},
			requestHeaders: map[string]string{
				"X-CDN-First":      "198.51.100.10",
				"X-CDN-Second":     "203.0.113.20",
				"CF-Connecting-IP": "8.8.8.8",
			},
			want: "198.51.100.10",
		},
		{
			name:         "comma candidates skip invalid and private values",
			trustForward: true,
			headers:      []string{"X-CDN-First", "X-CDN-Second"},
			requestHeaders: map[string]string{
				"X-CDN-First":  "not-an-ip, 10.0.0.8",
				"X-CDN-Second": "also-bad, 203.0.113.9",
			},
			want: "203.0.113.9",
		},
		{
			name:         "legacy public header wins over custom private fallback",
			trustForward: true,
			headers:      []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "10.0.0.8",
				"X-Real-IP": "1.2.3.4",
			},
			want: "1.2.3.4",
		},
		{
			name:         "custom private fallback retains configured precedence",
			trustForward: true,
			headers:      []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "10.0.0.8",
				"X-Real-IP": "192.168.1.4",
			},
			want: "10.0.0.8",
		},
		{
			name:         "invalid custom value continues to built-ins",
			trustForward: true,
			headers:      []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":         "1.2.3.4:443",
				"CF-Connecting-IP": "4.4.4.4",
			},
			want: "4.4.4.4",
		},
		{
			name:         "invalid legacy values continue to a valid forwarded address",
			trustForward: true,
			requestHeaders: map[string]string{
				"CF-Connecting-IP": "unknown",
				"X-Real-IP":        "proxy.internal",
				"X-Forwarded-For":  "also-invalid, 203.0.113.50",
			},
			want: "203.0.113.50",
		},
		{
			name:         "all invalid legacy values fall back to the connection address",
			trustForward: true,
			requestHeaders: map[string]string{
				"CF-Connecting-IP": "unknown",
				"X-Real-IP":        "proxy.internal",
				"X-Forwarded-For":  "also-invalid",
			},
			want: "9.9.9.9",
		},
		{
			name:         "disabled mode ignores custom and legacy headers",
			trustForward: false,
			headers:      []string{"X-CDN-IP"},
			requestHeaders: map[string]string{
				"X-CDN-IP":  "1.2.3.4",
				"X-Real-IP": "4.4.4.4",
			},
			want: "9.9.9.9",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.GET("/t", func(c *gin.Context) {
				SetForwardedIPSettings(c, test.trustForward, test.headers)
				c.String(200, GetSecurityClientIP(c, !test.trustForward))
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.RemoteAddr = "9.9.9.9:12345"
			for name, value := range test.requestHeaders {
				req.Header.Set(name, value)
			}
			r.ServeHTTP(w, req)

			require.Equal(t, test.want, w.Body.String())
		})
	}
}

// TestGetSecurityClientIPSwitchDisabledUsesConfiguredTrustedProxy
// disables compatibility for an explicitly trusted peer.
// Gin's configured proxy chain must still resolve the forwarded address, proving
// the disabled compatibility switch delegates to that configured trust policy.
func TestGetSecurityClientIPSwitchDisabledUsesConfiguredTrustedProxy(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"9.9.9.9"}))
	r.GET("/t", func(c *gin.Context) {
		c.String(200, GetSecurityClientIP(c, false))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "1.2.3.4", w.Body.String())
}

// TestGetClientIPSwitchDisabledUsesTrustedProxyChain
// captures a false mode with forwarding trust disabled.
// GetClientIP must return the direct peer address even though X-Real-IP contains
// another valid address that compatibility mode would otherwise accept.
func TestGetClientIPSwitchDisabledUsesTrustedProxyChain(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		SetForwardedIPSettings(c, false, nil)
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "9.9.9.9", w.Body.String())
}

// TestGetSecurityClientIPRequestSnapshotCopiesCustomHeaders
// changes the caller's header-name slice after capture.
// Address resolution must still use the captured original name, so later caller
// mutations cannot redirect this request to the other supplied header.
func TestGetSecurityClientIPRequestSnapshotCopiesCustomHeaders(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		headers := []string{"X-Original-IP"}
		SetForwardedIPSettings(c, true, headers)
		headers[0] = "X-Mutated-IP"
		c.String(200, GetSecurityClientIP(c, false))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Original-IP", "1.2.3.4")
	req.Header.Set("X-Mutated-IP", "4.4.4.4")
	r.ServeHTTP(w, req)

	require.Equal(t, "1.2.3.4", w.Body.String())
}

// TestGetSecurityClientIPRequestSnapshotOverridesLiveFallback
// passes a fallback opposite to the captured mode.
// Both true and false snapshots must win over that argument, returning the
// compatibility address or direct peer according to the request's stored policy.
func TestGetSecurityClientIPRequestSnapshotOverridesLiveFallback(t *testing.T) {
	tests := []struct {
		name          string // captured-policy override scenario name
		requestTrust  bool   // captured request forwarding mode
		fallbackTrust bool   // opposing live fallback argument
		want          string // expected captured-policy client address
	}{
		{name: "captured secure mode wins", requestTrust: false, fallbackTrust: true, want: "9.9.9.9"},
		{
			name:          "captured compatibility mode wins",
			requestTrust:  true,
			fallbackTrust: false,
			want:          "1.2.3.4",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(nil))
			r.GET("/t", func(c *gin.Context) {
				SetForwardedIPSettings(c, test.requestTrust, nil)
				c.String(200, GetSecurityClientIP(c, test.fallbackTrust))
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.RemoteAddr = "9.9.9.9:12345"
			req.Header.Set("X-Real-IP", "1.2.3.4")
			r.ServeHTTP(w, req)

			require.Equal(t, test.want, w.Body.String())
		})
	}
}

// TestGetClientIPWithoutSnapshotIgnoresForwardedHeaders reads the address on a
// route with no captured policy while a client supplies every built-in header.
// The direct peer must win, so a missing middleware cannot accept a chosen address.
func TestGetClientIPWithoutSnapshotIgnoresForwardedHeaders(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		c.String(200, GetClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "203.0.113.9:12345"
	req.Header.Set("CF-Connecting-IP", "8.8.8.8")
	req.Header.Set("X-Real-IP", "8.8.8.8")
	req.Header.Set("X-Forwarded-For", "8.8.8.8")
	r.ServeHTTP(w, req)

	require.Equal(t, "203.0.113.9", w.Body.String())
}
