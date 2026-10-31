package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestPublicAssetsCacheAndSandboxRetainedVersions requests current JavaScript, a retained older version, and a root SVG without panel credentials.
// Cache validators, conditional responses, and SVG sandbox headers remain correct, and every opened descriptor closes before the request completes.
func TestPublicAssetsCacheAndSandboxRetainedVersions(t *testing.T) {
	router, _, _, files, security := publicFixture(t)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/1.0.0/frontend/index.js", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, "text/javascript", response.Header().Get("Content-Type"))
	require.Equal(t, "public, max-age=31536000, immutable", response.Header().Get("Cache-Control"))
	require.Equal(t, "window.fixture=true", response.Body.String())
	tag := response.Header().Get("ETag")
	require.Len(t, tag, 34)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/1.0.0/frontend/index.js", nil)
	request.Header.Set("If-None-Match", tag)
	response = httptest.NewRecorder()
	router.ServeHTTP(response, request)
	require.Equal(t, 304, response.Code)
	require.Empty(t, response.Body.String())
	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/0.9.0/icon.svg", nil))
	require.Equal(t, 200, response.Code)
	require.Equal(t, "sandbox", response.Header().Get("Content-Security-Policy"))
	require.Equal(t, "inline", response.Header().Get("Content-Disposition"))
	require.Equal(t, len(files.opened), files.closed)
	require.Equal(t, 3, security.public)
	require.Zero(t, security.jwt)
}

// TestPublicAssetsRejectPrivatePathsAndUnavailableVersions probes traversal, package internals, unsupported extensions, and an absent retained version.
// Every request returns not found without exposing private package bytes, including encoded path segments that Gin decodes before route handling.
func TestPublicAssetsRejectPrivatePathsAndUnavailableVersions(t *testing.T) {
	for _, suffix := range []string{"1.0.0/plugin.json", "1.0.0/bin/plugin", "1.0.0/frontend/../plugin.json", "1.0.0/frontend/%2e%2e/plugin.json", "1.0.0/frontend/%252e%252e/plugin.json", "1.0.0/frontend/index.html", "9.0.0/frontend/index.js"} {
		router, _, _, _, _ := publicFixture(t)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/"+suffix, nil))
		require.Equal(t, 404, response.Code, suffix)
		require.NotContains(t, response.Body.String(), "private")
	}
}
