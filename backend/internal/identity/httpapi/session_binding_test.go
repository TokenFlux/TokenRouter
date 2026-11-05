package httpapi_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/server/clientip"
)

// Request binding captures both forwarding policy and normalized User-Agent
// before authentication consumers run. The fixtures vary Gin trusted proxies
// and live configuration, checking injected address selection, header bounds,
// and direct-call behavior when no binding middleware ran.

// TestSessionBindingContextFollowsForwardedIPSwitch varies forwarding trust
// and Gin proxy configuration around one request. It checks the injected
// address, normalized agent, SecurityClientIP, and successful downstream
// execution.
func TestSessionBindingContextFollowsForwardedIPSwitch(t *testing.T) {
	for _, tc := range []struct {
		name           string   // forwarding policy case
		trustForwarded bool     // raw header trust switch
		trustedProxies []string // Gin trusted proxy addresses
		wantIP         string   // expected injected security address
	}{
		{name: "enabled switch takes over raw headers", trustForwarded: true, wantIP: "1.2.3.4"},
		{name: "disabled switch ignores untrusted headers", trustForwarded: false, wantIP: "127.0.0.1"},
		{name: "disabled switch uses configured Gin proxy", trustForwarded: false, trustedProxies: []string{"127.0.0.1"}, wantIP: "1.2.3.4"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.SetTrustForwardedHeaders(tc.trustForwarded)

			r := gin.New()
			require.NoError(t, r.SetTrustedProxies(tc.trustedProxies))
			r.Use(identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
				v := cfg.ForwardedClientIPSettings()
				return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
			}))
			r.GET("/t", func(c *gin.Context) {
				binding := identity.SessionBindingFromContext(c.Request.Context())
				require.NotNil(t, binding)
				require.Equal(t, tc.wantIP, binding.IP)
				require.Equal(t, "test-agent", binding.UserAgent)
				require.Equal(t, tc.wantIP, identityhttp.SecurityClientIP(c))
				c.Status(200)
			})

			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/t", nil)
			req.RemoteAddr = "127.0.0.1:54321"
			req.Header.Set("X-Real-IP", "1.2.3.4")
			req.Header.Set("User-Agent", "test-agent")
			r.ServeHTTP(w, req)

			require.Equal(t, 200, w.Code)
		})
	}
}

// TestSessionBindingContextSnapshotsForwardedModeAndHeaders changes live
// forwarding configuration after binding middleware captures the initial
// custom header. It checks that the request keeps its captured address while
// runtime settings reflect the change.
func TestSessionBindingContextSnapshotsForwardedModeAndHeaders(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetForwardedClientIPSettings(true, []string{"X-Initial-IP"})

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.Use(identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
		v := cfg.ForwardedClientIPSettings()
		return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
	}))
	r.GET("/t", func(c *gin.Context) {
		binding := identity.SessionBindingFromContext(c.Request.Context())
		require.NotNil(t, binding)
		require.Equal(t, "1.2.3.4", binding.IP)

		// Change live settings after middleware captures this request, so the later
		// client-IP reader must select the captured mode and header list.
		cfg.SetForwardedClientIPSettings(false, []string{"X-Changed-IP"})
		require.Equal(t, "1.2.3.4", clientip.GetSecurityClientIP(c, false))
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Initial-IP", "1.2.3.4")
	req.Header.Set("X-Changed-IP", "4.4.4.4")
	req.Header.Set("X-Real-IP", "8.8.8.8")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	runtimeSettings := cfg.ForwardedClientIPSettings()
	require.False(t, runtimeSettings.TrustForwardedIP)
	require.Equal(t, []string{"X-Changed-IP"}, runtimeSettings.Headers)
}

// TestSessionBindingContextBoundsPersistedUserAgent sends an oversized ASCII
// User-Agent through binding middleware. It checks that stored binding text
// reaches the byte cap and matches the rewritten request header.
func TestSessionBindingContextBoundsPersistedUserAgent(t *testing.T) {
	cfg := &config.Config{}
	r := gin.New()
	r.Use(identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
		v := cfg.ForwardedClientIPSettings()
		return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
	}))
	r.GET("/t", func(c *gin.Context) {
		binding := identity.SessionBindingFromContext(c.Request.Context())
		require.Len(t, binding.UserAgent, authctx.MaxPersistentUserAgentBytes)
		require.Equal(t, binding.UserAgent, c.Request.UserAgent())
		c.Status(200)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.Header.Set("User-Agent", strings.Repeat("u", 2048))
	r.ServeHTTP(w, req)
	require.Equal(t, 200, w.Code)
}

// TestSecurityClientIPFallsBackWithoutInjectedBinding omits binding middleware
// and supplies an untrusted forwarding header. It checks that SecurityClientIP
// returns the peer address from Gin proxy resolution.
func TestSecurityClientIPFallsBackWithoutInjectedBinding(t *testing.T) {
	r := gin.New()
	require.NoError(t, r.SetTrustedProxies(nil))
	r.GET("/t", func(c *gin.Context) {
		c.String(200, identityhttp.SecurityClientIP(c))
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "9.9.9.9:12345"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
	require.Equal(t, "9.9.9.9", w.Body.String())
}

// TestRequestSessionBindingPrefersInjectedBinding enables forwarding and
// supplies a fixed address and User-Agent. It checks that the helper returns a
// hash matching those middleware-captured request values.
func TestRequestSessionBindingPrefersInjectedBinding(t *testing.T) {
	cfg := &config.Config{}
	cfg.SetTrustForwardedHeaders(true)

	r := gin.New()
	require.NoError(t, r.SetTrustedProxies([]string{"127.0.0.1"}))
	r.Use(identityhttp.SessionBindingContext(func() identityhttp.ForwardedIPSettings {
		v := cfg.ForwardedClientIPSettings()
		return identityhttp.ForwardedIPSettings{TrustForwardedIP: v.TrustForwardedIP, Headers: v.Headers}
	}))
	r.GET("/t", func(c *gin.Context) {
		issued := &identity.SessionBinding{IP: "1.2.3.4", UserAgent: "test-agent"}
		require.Equal(t, issued.Hash(), identityhttp.RequestSessionBinding(c).Hash())
		c.Status(200)
	})

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/t", nil)
	req.RemoteAddr = "127.0.0.1:54321"
	req.Header.Set("X-Real-IP", "1.2.3.4")
	req.Header.Set("User-Agent", "test-agent")
	r.ServeHTTP(w, req)

	require.Equal(t, 200, w.Code)
}
