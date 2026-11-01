package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/synctest"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// proxy_test.go exercises dynamic plugin authentication and bounded HTTP forwarding.
// Fixtures expose native middleware admission, lease ownership, and raw plugin
// responses so browser credentials, private caller grants, transport failures, and
// author-owned response semantics remain separated at the public proxy boundary.

// TestProxyPreservesRawJSONAndPrivateCallerBoundary forwards a user request with
// browser credentials, raw language preferences, and a spoofed identity body.
// The plugin receives the negotiated caller locale and verified identity
// separately from allowed headers and the original body, while the response stays
// unwrapped with cookies removed.
func TestProxyPreservesRawJSONAndPrivateCallerBoundary(t *testing.T) {
	router, handler, runtime, _, security := publicFixture(t)
	handler.options.Language = func(*gin.Context) string { return "zh-Hans" }
	request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/test.http/api/user?next=a%20b", strings.NewReader(`{"id":9}`))
	request.Header.Set("Authorization", "Bearer user")
	request.Header.Set("Accept-Language", "zh-CN, en;q=0.8")
	request.Header.Set("Cookie", "private-session")
	request.Header.Set("X-Api-Key", "private-management-key")
	request.Header.Set("X-Forwarded-For", "198.51.100.77")
	request.Header.Set("X-Plugin-Test", "allowed")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusCreated, response.Code)
	require.Equal(t, `{"ok":true}`, response.Body.String())
	require.Empty(t, response.Header().Get("Set-Cookie"))
	require.Equal(t, "accepted", response.Header().Get("X-Plugin-Result"))
	require.Equal(t, "nosniff", response.Header().Get("X-Content-Type-Options"))
	require.Equal(t, 1, runtime.acquired)
	require.Equal(t, 1, runtime.released)
	require.Equal(t, 1, security.jwt)
	require.Zero(t, security.public)
	forwarded := runtime.transport.last
	require.Equal(t, "/user", forwarded.Path)
	require.Equal(t, "next=a%20b", forwarded.RawQuery)
	require.Equal(t, `{"id":9}`, string(forwarded.Body))
	require.Equal(t, "203.0.113.7", forwarded.ClientIP)
	require.Equal(t, "allowed", forwarded.Headers["X-Plugin-Test"])
	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key", "X-Forwarded-For"} {
		require.NotContains(t, forwarded.Headers, name)
	}
	require.Equal(t, int64(7), forwarded.Caller.UserID)
	require.Equal(t, "Current username", forwarded.Caller.Username)
	require.Equal(t, "user", forwarded.Caller.Role)
	require.Equal(t, "zh-Hans", forwarded.Caller.Locale)
	require.Equal(t, "zh-CN, en;q=0.8", forwarded.Headers["Accept-Language"])
	require.NoError(t, handler.options.Grants.Verify("test.http", 7, forwarded.Caller.Grant))
	require.NotContains(t, response.Body.String(), forwarded.Caller.Grant)
}

// TestProxySelectsExistingAuthenticationAndLimits exercises anonymous, user, administrator, invalid-credential, and backend-mode route combinations.
// Authentication failures never invoke a plugin, public endpoints consume the public bucket, and the emergency switch prevents all contribution admission.
func TestProxySelectsExistingAuthenticationAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name     string // named middleware admission scenario
		method   string // requested HTTP method token
		path     string // requested plugin-relative endpoint
		token    string // supplied panel authorization header
		backend  bool   // enable backend-only panel restriction
		disabled bool   // disable all plugin contributions
		status   int    // expected final HTTP status
		calls    int    // expected contribution invocation count
	}{
		{name: "public anonymous", method: "GET", path: "/public", status: 201, calls: 1},
		{name: "public identified", method: "GET", path: "/public", token: "Bearer user", status: 201, calls: 1},
		{name: "public invalid credential", method: "GET", path: "/public", token: "invalid", status: 401},
		{name: "user anonymous", method: "POST", path: "/user", status: 401},
		{name: "admin denied user", method: "POST", path: "/admin", token: "Bearer user", status: 403},
		{name: "admin accepted", method: "POST", path: "/admin", token: "Bearer admin", status: 201, calls: 1},
		{name: "backend public denied", method: "GET", path: "/public", backend: true, status: 403},
		{name: "backend admin accepted", method: "POST", path: "/admin", token: "Bearer admin", backend: true, status: 201, calls: 1},
		{name: "global disable", method: "GET", path: "/public", disabled: true, status: 503},
		{name: "undeclared route", method: "GET", path: "/missing", status: 404},
		{name: "wrong method", method: "POST", path: "/public", status: 404},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router, _, runtime, _, security := publicFixture(t)
			security.backendMode = tc.backend
			runtime.enabled = !tc.disabled
			request := httptest.NewRequest(tc.method, "/api/v1/plugins/test.http/api"+tc.path, nil)
			request.Header.Set("Authorization", tc.token)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.status, response.Code)
			require.Equal(t, tc.calls, runtime.transport.calls)
			if tc.path == "/public" && tc.method == "GET" && !tc.disabled {
				require.Equal(t, 1, security.public)
			}
			if tc.calls == 1 && tc.token == "" {
				require.Nil(t, runtime.transport.last.Caller)
			}
		})
	}
}

// TestProxyRejectsTraversalOversizedBodiesAndVersionChanges submits traversal paths, oversized chunked input, and release changes between selection and acquisition.
// None reaches the contribution transport, and any acquired lease is released even when the selected authorization snapshot has become stale.
func TestProxyRejectsTraversalOversizedBodiesAndVersionChanges(t *testing.T) {
	for _, requestPath := range []string{"/wild/../admin", "/wild/%2e%2e/admin", "/wild//path", "/wild/%5cpath"} {
		router, _, runtime, _, _ := publicFixture(t)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/api"+requestPath, nil))
		require.Equal(t, 404, response.Code)
		require.Zero(t, runtime.acquired)
	}
	for _, requestPath := range []string{"/user", "/zero"} {
		router, _, runtime, _, _ := publicFixture(t)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/test.http/api"+requestPath, strings.NewReader("123456789"))
		request.Header.Set("Authorization", "Bearer user")
		request.ContentLength = -1
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, 413, response.Code)
		require.Zero(t, runtime.acquired)
	}
	for _, changeDigest := range []bool{false, true} {
		router, _, runtime, _, _ := publicFixture(t)
		if changeDigest {
			runtime.onAcquire = func() {
				loaded := runtime.loaded["test.http"]
				updated := *loaded.Installation
				updated.ManifestSHA256 = strings.Repeat("b", 64)
				loaded.Installation = &updated
				runtime.loaded["test.http"] = loaded
			}
		} else {
			runtime.version = "2.0.0"
		}
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/api/public", nil))
		require.Equal(t, 503, response.Code)
		require.Zero(t, runtime.transport.calls)
		require.Equal(t, 1, runtime.released)
	}
}

// TestProxyRejectsActiveContentAndInvalidReplies supplies active MIME types, malformed metadata, duplicate headers, and oversized buffered output.
// The host returns a gateway error before exposing plugin-controlled headers or bytes, preserving one deterministic response boundary across all failures.
func TestProxyRejectsActiveContentAndInvalidReplies(t *testing.T) {
	for _, reply := range []*plugin.HTTPResponse{
		nil,
		{Status: 199, Headers: map[string]string{"Content-Type": "application/json"}},
		{Status: 600, Headers: map[string]string{"Content-Type": "application/json"}},
		{Status: 200, Headers: map[string]string{"Content-Type": "text/html"}, Body: []byte("<script>private</script>")},
		{Status: 200, Headers: map[string]string{"Content-Type": "image/svg+xml"}},
		{Status: 200, Headers: map[string]string{"Content-Type": "application/json; broken"}},
		{Status: 200, Headers: map[string]string{"Content-Type": "application/json", "content-type": "text/plain"}},
		{Status: 200, Headers: map[string]string{"Content-Type": "application/json"}, Body: make([]byte, maxResponseBody+1)},
	} {
		router, _, runtime, _, _ := publicFixture(t)
		runtime.transport.exchange = func(context.Context, *plugin.HTTPRequest) (*plugin.HTTPResponse, error) { return reply, nil }
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/api/public", nil))
		require.Equal(t, 502, response.Code)
		require.NotContains(t, response.Body.String(), "<script>")
		require.Equal(t, 1, runtime.released)
	}
}

// TestProxyDeadlineCancelsTransport runs a contribution that waits until the selected route deadline expires under the synthetic clock.
// Cancellation reaches transport, the response uses the timeout reason, and the generation lease is released before the handler returns.
func TestProxyDeadlineCancelsTransport(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		router, _, runtime, _, _ := publicFixture(t)
		runtime.transport.exchange = func(ctx context.Context, _ *plugin.HTTPRequest) (*plugin.HTTPResponse, error) {
			<-ctx.Done()
			return nil, plugin.ErrPluginTimeout
		}
		request := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/api/slow", nil)
		request.Header.Set("Authorization", "Bearer user")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, 504, response.Code)
		require.Contains(t, response.Body.String(), "PLUGIN_TIMEOUT")
		require.Equal(t, 1, runtime.released)
	})
}

// TestProxyMarksAuthorResponsesAfterAuthentication distinguishes native authentication failure from a plugin's own unauthorized response.
// Only an invoked plugin reply receives the marker, and neither request headers nor plugin headers can forge its final value.
func TestProxyMarksAuthorResponsesAfterAuthentication(t *testing.T) {
	for _, token := range []string{"invalid", "Bearer user"} {
		router, _, runtime, _, _ := publicFixture(t)
		runtime.transport.exchange = func(context.Context, *plugin.HTTPRequest) (*plugin.HTTPResponse, error) {
			return &plugin.HTTPResponse{Status: http.StatusUnauthorized, Headers: map[string]string{"Content-Type": "application/json", "X-TokenRouter-Plugin-Response": "0"}, Body: []byte(`{"code":"PLUGIN_LOGIN_REQUIRED"}`)}, nil
		}
		request := httptest.NewRequest(http.MethodPost, "/api/v1/plugins/test.http/api/user", nil)
		request.Header.Set("Authorization", token)
		request.Header.Set("X-TokenRouter-Plugin-Response", "1")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusUnauthorized, response.Code)
		if token == "invalid" {
			require.Empty(t, response.Header().Get("X-TokenRouter-Plugin-Response"))
			require.Zero(t, runtime.acquired)
		} else {
			require.Equal(t, "1", response.Header().Get("X-TokenRouter-Plugin-Response"))
			require.Equal(t, `{"code":"PLUGIN_LOGIN_REQUIRED"}`, response.Body.String())
		}
	}
}
