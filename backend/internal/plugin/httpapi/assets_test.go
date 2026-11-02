package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/plugin"
)

// TestPublicAssetsCacheAndSandboxRetainedVersions requests current JavaScript, a retained older version, and a root SVG without panel credentials.
// Cache validators, conditional responses, and SVG sandbox headers remain correct, and every opened descriptor closes before the request completes.
func TestPublicAssetsCacheAndSandboxRetainedVersions(t *testing.T) {
	router, _, runtime, files, security := publicFixture(t)
	loaded := runtime.loaded["test.http"]
	previous := loaded.Installation.VersionSnapshot
	previous.Version = "0.9.0"
	loaded.Installation.Previous = &previous
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

// TestDeclaredIconsRemainVersionScopedAndSizeBounded places custom icons outside the ordinary public directories for current and previous releases.
// Only each release's declared image becomes readable, while another image path and a declared icon larger than 64 KiB remain inaccessible.
func TestDeclaredIconsRemainVersionScopedAndSizeBounded(t *testing.T) {
	router, _, runtime, files, _ := publicFixture(t)
	loaded := runtime.loaded["test.http"]
	loaded.Installation.Manifest.Icon = "art/current.svg"
	previous := loaded.Installation.VersionSnapshot
	previous.Version = "0.9.0"
	declaration := *previous.Manifest
	declaration.Version = previous.Version
	declaration.Icon = "art/previous.svg"
	previous.Manifest = &declaration
	loaded.Installation.Previous = &previous
	for _, version := range []string{"1.0.0", "0.9.0"} {
		item := files.packages[version]
		for _, name := range []string{"art/current.svg", "art/previous.svg"} {
			data := []byte("<svg/>")
			digest := sha256.Sum256(data)
			item.Files[name] = plugin.Asset{Size: int64(len(data)), SHA256: hex.EncodeToString(digest[:])}
			files.content[item.Directory+":"+name] = data
		}
	}
	for _, tc := range []struct {
		path   string // requested version and icon path
		status int    // expected public response status
	}{
		{path: "1.0.0/art/current.svg", status: 200},
		{path: "0.9.0/art/previous.svg", status: 200},
		{path: "1.0.0/art/previous.svg", status: 404},
		{path: "0.9.0/art/current.svg", status: 404},
	} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/"+tc.path, nil))
		require.Equal(t, tc.status, response.Code, tc.path)
	}
	files.content[loaded.Package.Directory+":art/current.svg"] = make([]byte, 64<<10+1)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/plugins/test.http/assets/1.0.0/art/current.svg", nil))
	require.Equal(t, 404, response.Code)
}
