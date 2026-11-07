//go:build embed

package web

import (
	"bytes"
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
)

// mockSettingsProvider supplies configured public-data results and a serial
// call count for HTML fixtures.
type mockSettingsProvider struct {
	// settings and err are supplied together as the next provider result; serial
	// tests replace them between completed requests. called records each invocation
	// and needs the same serial access because it has no synchronization.
	settings any   // supplied public settings payload
	err      error // supplied public settings error
	called   int   // serial provider invocation count
}

// blockingPublic delays its first public-data result to expose rendering across
// cache invalidation.
type blockingPublic struct {
	// entered announces the first blocked read and release lets it return old
	// data. The test invalidates between those signals, and calls atomically identifies
	// that first invocation before later callers receive new data.
	entered, release chan struct{} // entered: first read started; release: continue first read
	calls            atomic.Int64  // concurrent provider invocation count
}

// TestInjectSiteTitle supplies configured names, HTML delimiters, invalid
// settings, and characters requiring escaping. Replacement must use the fixed
// suffix, retain surrounding elements, and leave unmatched title forms
// unchanged.
func TestInjectSiteTitle(t *testing.T) {
	t.Run("replaces_title_with_site_name", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter - AI API Gateway</title></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":"MyCustomSite"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Contains(t, string(result), "<title>MyCustomSite - AI API Gateway</title>")
		assert.NotContains(t, string(result), "TokenRouter")
	})

	t.Run("returns_unchanged_when_site_name_empty", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter - AI API Gateway</title></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":""}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("returns_unchanged_when_site_name_missing", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter - AI API Gateway</title></head><body></body></html>`)
		settingsJSON := []byte(`{"other_field":"value"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("returns_unchanged_when_invalid_json", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter - AI API Gateway</title></head><body></body></html>`)
		settingsJSON := []byte(`{invalid json}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("returns_unchanged_when_no_title_tag", func(t *testing.T) {
		html := []byte(`<html><head></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":"MyCustomSite"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("returns_unchanged_when_title_has_attributes", func(t *testing.T) {
		// Title replacement matches the attribute-free title tag emitted in index.html.
		html := []byte(`<html><head><title lang="en">TokenRouter</title></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":"NewSite"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("escapes_html_in_site_name", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter - AI API Gateway</title></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":"</title><script>alert(1)</script><title>"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.NotContains(t, string(result), "<script>")
		assert.Contains(t, string(result), "&lt;/title&gt;&lt;script&gt;alert(1)&lt;/script&gt;&lt;title&gt;")
	})

	t.Run("escapes_ampersand_in_site_name", func(t *testing.T) {
		html := []byte(`<html><head><title>TokenRouter</title></head><body></body></html>`)
		settingsJSON := []byte(`{"site_name":"A&B"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Contains(t, string(result), "<title>A&amp;B - AI API Gateway</title>")
	})

	t.Run("preserves_rest_of_html", func(t *testing.T) {
		html := []byte(`<html><head><meta charset="UTF-8"><title>TokenRouter</title><script src="app.js"></script></head><body><div id="app"></div></body></html>`)
		settingsJSON := []byte(`{"site_name":"TestSite"}`)

		result := injectSiteTitle(html, settingsJSON)

		assert.Contains(t, string(result), `<meta charset="UTF-8">`)
		assert.Contains(t, string(result), `<script src="app.js"></script>`)
		assert.Contains(t, string(result), `<div id="app"></div>`)
		assert.Contains(t, string(result), "<title>TestSite - AI API Gateway</title>")
	})
}

// TestInjectSiteFavicon supplies HTTP, relative, data-image, and rejected logo
// URLs to known icon markup. Replacement must select accepted text, escape
// attribute separators, and retain the input when the URL is rejected.
func TestInjectSiteFavicon(t *testing.T) {
	t.Run("replaces_favicon_with_site_logo", func(t *testing.T) {
		html := []byte(`<html><head><link rel="icon" type="image/png" href="/logo.png" /></head></html>`)
		settingsJSON := []byte(`{"site_logo":"https://example.com/custom-logo.png"}`)

		result := injectSiteFavicon(html, settingsJSON)

		assert.Contains(t, string(result), `<link rel="icon" href="https://example.com/custom-logo.png" />`)
		assert.NotContains(t, string(result), `/logo.png`)
	})

	t.Run("supports_relative_and_data_image_urls", func(t *testing.T) {
		html := []byte(`<link rel="icon" href="/logo.png" />`)

		assert.Contains(t, string(injectSiteFavicon(html, []byte(`{"site_logo":"/uploads/logo.svg"}`))), `/uploads/logo.svg`)
		assert.Contains(t, string(injectSiteFavicon(html, []byte(`{"site_logo":"data:image/png;base64,abc"}`))), `data:image/png;base64,abc`)
	})

	t.Run("rejects_unsafe_logo_urls", func(t *testing.T) {
		html := []byte(`<link rel="icon" href="/logo.png" />`)

		result := injectSiteFavicon(html, []byte(`{"site_logo":"javascript:alert(1)"}`))

		assert.Equal(t, string(html), string(result))
	})

	t.Run("escapes_logo_url_for_html", func(t *testing.T) {
		html := []byte(`<link rel="icon" href="/logo.png" />`)

		result := injectSiteFavicon(html, []byte(`{"site_logo":"https://example.com/logo.png?a=1&b=2"}`))

		assert.Contains(t, string(result), `a=1&amp;b=2`)
	})
}

// TestReplaceNoncePlaceholder supplies single and repeated placeholders with
// populated and empty nonces. Replacement must substitute every occurrence and
// retain equivalent bytes for empty or placeholder-free input.
func TestReplaceNoncePlaceholder(t *testing.T) {
	t.Run("replaces_single_placeholder", func(t *testing.T) {
		html := []byte(`<script nonce="__CSP_NONCE_VALUE__">console.log('test');</script>`)
		nonce := "abc123xyz"

		result := replaceNoncePlaceholder(html, nonce)

		expected := `<script nonce="abc123xyz">console.log('test');</script>`
		assert.Equal(t, expected, string(result))
	})

	t.Run("replaces_multiple_placeholders", func(t *testing.T) {
		html := []byte(`<script nonce="__CSP_NONCE_VALUE__">a</script><script nonce="__CSP_NONCE_VALUE__">b</script>`)
		nonce := "nonce123"

		result := replaceNoncePlaceholder(html, nonce)

		assert.Equal(t, 2, strings.Count(string(result), `nonce="nonce123"`))
		assert.NotContains(t, string(result), NonceHTMLPlaceholder)
	})

	t.Run("handles_empty_nonce", func(t *testing.T) {
		html := []byte(`<script nonce="__CSP_NONCE_VALUE__">test</script>`)
		nonce := ""

		result := replaceNoncePlaceholder(html, nonce)

		assert.Equal(t, `<script nonce="">test</script>`, string(result))
	})

	t.Run("no_placeholder_returns_unchanged", func(t *testing.T) {
		html := []byte(`<script>console.log('test');</script>`)
		nonce := "abc123"

		result := replaceNoncePlaceholder(html, nonce)

		assert.Equal(t, string(html), string(result))
	})

	t.Run("handles_empty_html", func(t *testing.T) {
		html := []byte(``)
		nonce := "abc123"

		result := replaceNoncePlaceholder(html, nonce)

		assert.Empty(t, result)
	})
}

// TestNonceHTMLPlaceholder inspects the exact marker used by reusable script
// markup. The constant must match the nonce-placeholder spelling embedded in
// test HTML and used for response substitution.
func TestNonceHTMLPlaceholder(t *testing.T) {
	t.Run("constant_value", func(t *testing.T) {
		assert.Equal(t, "__CSP_NONCE_VALUE__", NonceHTMLPlaceholder)
	})
}

// TestFrontendServer_InjectSettings uses the embedded entry with simple and
// nested settings JSON. The returned markup must insert the supplied
// configuration before head closure and retain the unresolved nonce
// placeholder.
func TestFrontendServer_InjectSettings(t *testing.T) {
	t.Run("injects_settings_with_nonce_placeholder", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"key": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		settingsJSON := []byte(`{"test":"data"}`)
		result := server.injectSettings(settingsJSON)

		// The cached script retains a placeholder for the request's nonce.
		assert.Contains(t, string(result), `<script nonce="__CSP_NONCE_VALUE__">`)
		assert.Contains(t, string(result), `window.__APP_CONFIG__={"test":"data"};`)
		assert.Contains(t, string(result), `</script></head>`)
	})

	t.Run("injects_before_head_close", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"key": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		settingsJSON := []byte(`{}`)
		result := server.injectSettings(settingsJSON)

		headCloseIndex := bytes.Index(result, []byte("</head>"))
		scriptIndex := bytes.Index(result, []byte(`<script nonce="`))

		assert.True(t, scriptIndex < headCloseIndex, "script should be before </head>")
	})

	t.Run("handles_complex_settings", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]any{
				"nested": map[string]any{
					"array": []int{1, 2, 3},
				},
			},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		settingsJSON := []byte(`{"nested":{"array":[1,2,3]},"special":"<>&"}`)
		result := server.injectSettings(settingsJSON)

		assert.Contains(t, string(result), `window.__APP_CONFIG__={"nested":{"array":[1,2,3]},"special":"<>&"};`)
	})
}

// TestFrontendServer_ServeIndexHTML renders configured pages, repeats cache
// reads with different nonces, revalidates an ETag, and injects a provider
// error. Responses must expose expected status, HTML, nonce, cache-read count,
// and rendering headers.
func TestFrontendServer_ServeIndexHTML(t *testing.T) {
	t.Run("serves_html_with_nonce", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

		// Simulate the context value supplied by SecurityHeaders.
		testNonce := "test-nonce-12345"
		c.Set(middleware.CSPNonceKey, testNonce)

		server.serveIndexHTML(c)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")

		body := w.Body.String()

		assert.NotContains(t, body, NonceHTMLPlaceholder)
		assert.Contains(t, body, `nonce="`+testNonce+`"`)
	})

	t.Run("caches_html_content", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		w1 := httptest.NewRecorder()
		c1, _ := gin.CreateTestContext(w1)
		c1.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c1.Set(middleware.CSPNonceKey, "nonce1")

		server.serveIndexHTML(c1)
		assert.Equal(t, 1, provider.called)

		// The second request reuses the cached template.
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c2.Set(middleware.CSPNonceKey, "nonce2")

		server.serveIndexHTML(c2)
		// A cache hit avoids another settings read.
		assert.Equal(t, 1, provider.called)

		// The cached template still receives a fresh nonce per request.
		assert.Contains(t, w2.Body.String(), `nonce="nonce2"`)
	})

	t.Run("sets_etag_header", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(middleware.CSPNonceKey, "nonce123")

		server.serveIndexHTML(c)

		etag := w.Header().Get("ETag")
		assert.NotEmpty(t, etag)
		assert.True(t, strings.HasPrefix(etag, `"`))
		assert.True(t, strings.HasSuffix(etag, `"`))
	})

	t.Run("returns_304_for_matching_etag", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(middleware.CSPNonceKey, "test-nonce")
			c.Next()
		})
		router.Use(server.Middleware())

		// Prime the cache and obtain its ETag.
		w1 := httptest.NewRecorder()
		req1 := httptest.NewRequest(http.MethodGet, "/", nil)
		router.ServeHTTP(w1, req1)
		etag := w1.Header().Get("ETag")
		require.NotEmpty(t, etag)

		// Revalidate the cached response using the ETag.
		w2 := httptest.NewRecorder()
		req2 := httptest.NewRequest(http.MethodGet, "/", nil)
		req2.Header.Set("If-None-Match", etag)
		router.ServeHTTP(w2, req2)

		assert.Equal(t, http.StatusNotModified, w2.Code)
		assert.Empty(t, w2.Body.String())
	})

	t.Run("sets_cache_control_header", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(middleware.CSPNonceKey, "nonce123")

		server.serveIndexHTML(c)

		assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
	})

	t.Run("fallback_on_settings_error", func(t *testing.T) {
		provider := &mockSettingsProvider{
			err: context.DeadlineExceeded,
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		// Force a settings read after invalidation.
		server.InvalidateCache()

		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(middleware.CSPNonceKey, "nonce123")

		server.serveIndexHTML(c)

		// A settings failure leaves the base page available with status 200.
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	})
}

// TestFrontendServer_InvalidateCache primes a rendered page and then changes
// settings after invalidation. A following page request must read the provider
// again, while nil server and cache notifications must not panic.
func TestFrontendServer_InvalidateCache(t *testing.T) {
	t.Run("invalidates_cache", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		// Prime the cache before changing settings.
		w1 := httptest.NewRecorder()
		c1, _ := gin.CreateTestContext(w1)
		c1.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c1.Set(middleware.CSPNonceKey, "nonce1")

		server.serveIndexHTML(c1)
		assert.Equal(t, 1, provider.called)

		server.InvalidateCache()

		provider.settings = map[string]string{"test": "new_value"}

		// Invalidation forces the next request to read the new settings.
		w2 := httptest.NewRecorder()
		c2, _ := gin.CreateTestContext(w2)
		c2.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c2.Set(middleware.CSPNonceKey, "nonce2")

		server.serveIndexHTML(c2)
		assert.Equal(t, 2, provider.called)
	})

	t.Run("handles_nil_server", func(t *testing.T) {
		var server *FrontendServer

		assert.NotPanics(t, func() {
			server.InvalidateCache()
		})
	})

	t.Run("handles_nil_cache", func(t *testing.T) {
		server := &FrontendServer{}

		assert.NotPanics(t, func() {
			server.InvalidateCache()
		})
	})
}

// TestOverrideFilesNeverReceiveImmutableCacheHeaders exercises disk replacements
// through both frontend middleware entry points and an override-disabled server.
// It checks response bytes, status, Gin termination, and embedded caching when
// replacements are missing or directories.
func TestOverrideFilesNeverReceiveImmutableCacheHeaders(t *testing.T) {
	// ServeEmbeddedFrontend resolves overrides relative to the process working
	// directory. A temporary working directory isolates disk fixtures for both entry points.
	t.Chdir(t.TempDir())

	distFS, err := fs.Sub(frontendFS, "dist")
	require.NoError(t, err)
	entries, err := fs.ReadDir(distFS, "assets")
	require.NoError(t, err)
	cleanPath := ""
	for _, entry := range entries {
		candidate := "assets/" + entry.Name()
		if !entry.IsDir() && isFingerprintedEmbeddedAssetPath(candidate) {
			cleanPath = candidate
			break
		}
	}
	require.NotEmpty(t, cleanPath)
	embeddedContents, err := fs.ReadFile(distFS, cleanPath)
	require.NoError(t, err)

	overrideDir := filepath.Join("data", "public")
	filePath := filepath.Join(overrideDir, cleanPath)
	server := &FrontendServer{
		distFS:      distFS,
		fileServer:  http.FileServer(http.FS(distFS)),
		overrideDir: overrideDir,
	}
	disabledServer := &FrontendServer{
		distFS:     distFS,
		fileServer: http.FileServer(http.FS(distFS)),
	}

	for _, entryPoint := range []struct {
		name             string          // middleware entry label
		middleware       gin.HandlerFunc // static response entry
		overridesEnabled bool            // disk overrides enabled
	}{
		{"frontend_server", server.Middleware(), true},
		{"direct_bundle", ServeEmbeddedFrontend(), true},
		{"frontend_server_disabled", disabledServer.Middleware(), false},
	} {
		t.Run(entryPoint.name, func(t *testing.T) {
			for _, candidateKind := range []string{"file", "missing", "directory"} {
				t.Run(candidateKind, func(t *testing.T) {
					require.NoError(t, os.RemoveAll(overrideDir))
					switch candidateKind {
					case "file":
						require.NoError(t, os.MkdirAll(filepath.Dir(filePath), 0o755))
						require.NoError(t, os.WriteFile(filePath, []byte("override"), 0o644))
					case "directory":
						require.NoError(t, os.MkdirAll(filePath, 0o755))
					}

					nextCalled := false
					aborted := false
					router := gin.New()
					router.Use(func(c *gin.Context) {
						c.Next()
						aborted = c.IsAborted()
					})
					router.Use(entryPoint.middleware)
					router.GET("/"+cleanPath, func(c *gin.Context) {
						nextCalled = true
						c.String(http.StatusTeapot, "next handler")
					})

					w := httptest.NewRecorder()
					req := httptest.NewRequest(http.MethodGet, "/"+cleanPath, nil)
					router.ServeHTTP(w, req)

					assert.Equal(t, http.StatusOK, w.Code)
					assert.False(t, nextCalled)
					assert.True(t, aborted)
					if entryPoint.overridesEnabled && candidateKind == "file" {
						assert.Equal(t, "override", w.Body.String())
						assert.Empty(t, w.Header().Get("Cache-Control"))
					} else {
						assert.Equal(t, string(embeddedContents), w.Body.String())
						assert.Equal(t, staticAssetsCacheControl, w.Header().Get("Cache-Control"))
					}
				})
			}
		})
	}
}

// TestFrontendServer_Middleware routes injected pages, existing assets,
// marketplace navigation, and reserved gateway paths through Gin. API handlers
// must remain reachable, SPA routes must receive HTML, and discovered
// fingerprinted assets must receive the immutable cache policy.
func TestFrontendServer_Middleware(t *testing.T) {
	t.Run("skips_api_routes", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		apiPaths := []string{
			"/api/v1/users",
			"/mcp",
			"/v1/models",
			"/models",
			"/v1beta/chat",
			"/backend-api/codex/responses",
			"/backend-api/codex/responses/compact",
			"/antigravity/test",
			"/setup/init",
			"/health",
			"/responses",
			"/responses/compact",
		}

		for _, path := range apiPaths {
			t.Run(path, func(t *testing.T) {
				router := gin.New()
				router.Use(server.Middleware())
				nextCalled := false
				router.GET(path, func(c *gin.Context) {
					nextCalled = true
					c.String(http.StatusOK, "ok")
				})

				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				router.ServeHTTP(w, req)

				assert.True(t, nextCalled, "next handler should be called for API route")
			})
		}
	})

	t.Run("serves_model_marketplace_for_browser_navigation", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(server.Middleware())
		nextCalled := false
		router.GET("/models", func(c *gin.Context) {
			nextCalled = true
			c.String(http.StatusOK, "api")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/models", nil)
		// Simulate address-bar navigation so the SPA handles the request.
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		router.ServeHTTP(w, req)

		assert.False(t, nextCalled)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Values("Vary"), "Accept")
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, w.Body.String(), "<!doctype html>")
	})

	t.Run("skips_responses_compact_post_routes", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(server.Middleware())
		nextCalled := false
		router.POST("/responses/compact", func(c *gin.Context) {
			nextCalled = true
			c.String(http.StatusOK, `{"ok":true}`)
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/responses/compact", strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		assert.True(t, nextCalled, "next handler should be called for compact API route")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"ok":true}`, w.Body.String())
	})

	t.Run("skips_alpha_search_post_route", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(server.Middleware())
		nextCalled := false
		router.POST("/alpha/search", func(c *gin.Context) {
			nextCalled = true
			c.JSON(http.StatusOK, gin.H{"ok": true})
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/alpha/search", strings.NewReader(`{"model":"gpt-5.6-sol"}`))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)

		assert.True(t, nextCalled, "next handler should be called for alpha search API route")
		assert.Equal(t, http.StatusOK, w.Code)
		assert.JSONEq(t, `{"ok":true}`, w.Body.String())
	})

	t.Run("serves_index_for_spa_routes", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(func(c *gin.Context) {
			c.Set(middleware.CSPNonceKey, "test-nonce")
			c.Next()
		})
		router.Use(server.Middleware())

		spaPaths := []string{
			"/",
			"/dashboard",
			"/users/123",
			"/settings/profile",
		}

		for _, path := range spaPaths {
			t.Run(path, func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				router.ServeHTTP(w, req)

				assert.Equal(t, http.StatusOK, w.Code)
				assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
			})
		}
	})

	t.Run("serves_static_files", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		router := gin.New()
		router.Use(server.Middleware())

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/logo.svg", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "image/svg+xml")
		assert.Empty(t, w.Header().Get("Cache-Control"))

		entries, err := fs.ReadDir(server.distFS, "assets")
		require.NoError(t, err)
		fingerprintedPath := ""
		for _, entry := range entries {
			candidate := "assets/" + entry.Name()
			if !entry.IsDir() && isFingerprintedEmbeddedAssetPath(candidate) {
				fingerprintedPath = candidate
				break
			}
		}
		require.NotEmpty(t, fingerprintedPath)

		assetWriter := httptest.NewRecorder()
		assetRequest := httptest.NewRequest(http.MethodGet, "/"+fingerprintedPath, nil)
		router.ServeHTTP(assetWriter, assetRequest)

		assert.Equal(t, http.StatusOK, assetWriter.Code)
		assert.Equal(t, staticAssetsCacheControl, assetWriter.Header().Get("Cache-Control"))
	})
}

// TestEmbeddedFrontendBypassesBareVideoAPIRoutes supplies generation, edit,
// extension, and request-specific paths under the bare video API prefix. Every
// path must bypass frontend handling so a registered video handler can receive
// it.
func TestEmbeddedFrontendBypassesBareVideoAPIRoutes(t *testing.T) {
	for _, path := range []string{
		"/videos/generations",
		"/videos/edits",
		"/videos/extensions",
		"/videos/request-123",
	} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		require.True(t, shouldBypassEmbeddedFrontend(request), "path=%s", path)
	}
}

// TestEmbeddedFrontendModelsRouteNegotiation combines browser Accept text with
// model API version, credential, and JSON-preference hints. Each fixture must
// choose its expected frontend-bypass result for the shared /models route.
func TestEmbeddedFrontendModelsRouteNegotiation(t *testing.T) {
	tests := []struct {
		name       string            // subtest display name
		path       string            // supplied request path and query
		accept     string            // supplied media preference header
		headers    map[string]string // supplied credential header values
		wantBypass bool              // expected API forwarding choice
	}{
		{
			name:       "浏览器页面导航",
			path:       "/models",
			accept:     "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8",
			wantBypass: false,
		},
		{
			name:       "Codex 模型请求",
			path:       "/models?client_version=0.144.0",
			accept:     "text/html,application/json;q=0.9",
			wantBypass: true,
		},
		{
			name:       "JSON API 请求",
			path:       "/models",
			accept:     "application/json",
			wantBypass: true,
		},
		{
			name:   "带 API Key 的请求",
			path:   "/models",
			accept: "text/html",
			headers: map[string]string{
				"Authorization": "Bearer sk-test",
			},
			wantBypass: true,
		},
		{
			name:       "JSON 优先的混合请求",
			path:       "/models",
			accept:     "application/json,text/html,*/*",
			wantBypass: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			request.Header.Set("Accept", tt.accept)
			for name, value := range tt.headers {
				request.Header.Set(name, value)
			}

			require.Equal(t, tt.wantBypass, shouldBypassEmbeddedFrontend(request))
		})
	}
}

// TestNewFrontendServer constructs the server with a public-data fixture and
// reads its compiled entry. Construction must retain that provider and
// initialize filesystem, asset handler, base bytes, and language cache.
func TestNewFrontendServer(t *testing.T) {
	t.Run("creates_server_successfully", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)

		require.NoError(t, err)
		assert.NotNil(t, server)
		assert.NotNil(t, server.distFS)
		assert.NotNil(t, server.fileServer)
		assert.NotNil(t, server.baseHTML)
		assert.NotNil(t, server.cache)
		assert.Equal(t, provider, server.settings)
	})

	t.Run("reads_base_html", func(t *testing.T) {
		provider := &mockSettingsProvider{
			settings: map[string]string{"test": "value"},
		}

		server, err := NewFrontendServer(provider)
		require.NoError(t, err)

		assert.NotEmpty(t, server.baseHTML)
		assert.Contains(t, string(server.baseHTML), "<!doctype html>")
	})
}

// TestHasEmbeddedFrontend reads the build's compiled frontend availability
// flag. The embed fixture must report true because its dist entry is present
// and readable in the built test bundle.
func TestHasEmbeddedFrontend(t *testing.T) {
	t.Run("returns_true_when_frontend_embedded", func(t *testing.T) {
		result := HasEmbeddedFrontend()
		assert.True(t, result)
	})
}

// TestServeEmbeddedFrontend sends static, root, SPA, marketplace, and reserved
// API requests through the direct bundle entry point. HTML and asset responses
// must remain available while reserved routes reach their registered handlers.
func TestServeEmbeddedFrontend(t *testing.T) {
	t.Run("serves_static_files", func(t *testing.T) {
		middleware := ServeEmbeddedFrontend()

		router := gin.New()
		router.Use(middleware)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/logo.svg", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "image/svg+xml")
	})

	t.Run("serves_index_html_for_root", func(t *testing.T) {
		middleware := ServeEmbeddedFrontend()

		router := gin.New()
		router.Use(middleware)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, w.Body.String(), "<!doctype html>")
	})

	t.Run("serves_index_html_for_spa_routes", func(t *testing.T) {
		middleware := ServeEmbeddedFrontend()

		router := gin.New()
		router.Use(middleware)

		spaPaths := []string{"/dashboard", "/users/123", "/settings"}

		for _, path := range spaPaths {
			t.Run(path, func(t *testing.T) {
				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				router.ServeHTTP(w, req)

				assert.Equal(t, http.StatusOK, w.Code)
				assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
			})
		}
	})

	t.Run("serves_model_marketplace_for_browser_navigation", func(t *testing.T) {
		middleware := ServeEmbeddedFrontend()

		router := gin.New()
		router.Use(middleware)
		nextCalled := false
		router.GET("/models", func(c *gin.Context) {
			nextCalled = true
			c.String(http.StatusOK, "api")
		})

		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/models", nil)
		// The entry point without settings injection also serves browser navigation through the SPA.
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		router.ServeHTTP(w, req)

		assert.False(t, nextCalled)
		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, w.Header().Values("Vary"), "Accept")
		assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
		assert.Contains(t, w.Body.String(), "<!doctype html>")
	})

	t.Run("skips_api_routes", func(t *testing.T) {
		middleware := ServeEmbeddedFrontend()

		apiPaths := []string{
			"/api/users",
			"/mcp",
			"/v1/models",
			"/models",
			"/v1beta/chat",
			"/backend-api/codex/responses",
			"/backend-api/codex/responses/compact",
			"/antigravity/test",
			"/setup/init",
			"/health",
			"/responses",
			"/responses/compact",
		}

		for _, path := range apiPaths {
			t.Run(path, func(t *testing.T) {
				nextCalled := false
				router := gin.New()
				router.Use(middleware)
				router.GET(path, func(c *gin.Context) {
					nextCalled = true
					c.String(http.StatusOK, "ok")
				})

				w := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, path, nil)
				router.ServeHTTP(w, req)

				assert.True(t, nextCalled, "next handler should be called for API route")
			})
		}
	})
}

// TestMCPRoutesBypassEmbeddedFrontend exercises Streamable HTTP methods in both
// embedded serving modes. Even a browser Accept header must reach the MCP
// handler instead of receiving the application's HTML entry page.
func TestMCPRoutesBypassEmbeddedFrontend(t *testing.T) {
	server, err := NewFrontendServer(&mockSettingsProvider{settings: map[string]string{}})
	require.NoError(t, err)
	for name, frontend := range map[string]gin.HandlerFunc{"injected": server.Middleware(), "static": ServeEmbeddedFrontend()} {
		for _, method := range []string{http.MethodPost, http.MethodGet, http.MethodDelete} {
			t.Run(name+method, func(t *testing.T) {
				router := gin.New()
				router.Use(frontend)
				router.Any("/mcp", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"transport": "mcp"}) })
				request := httptest.NewRequest(method, "/mcp", nil)
				request.Header.Set("Accept", "text/html, application/json")
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				require.Equal(t, http.StatusOK, response.Code)
				require.Contains(t, response.Header().Get("Content-Type"), "application/json")
				require.JSONEq(t, `{"transport":"mcp"}`, response.Body.String())
			})
		}
	}
}

// BenchmarkReplaceNoncePlaceholder repeatedly substitutes one marker in a fixed
// HTML payload after setup is excluded from timing. It measures
// replacement-call cost and leaves output validation to the dedicated nonce
// tests.
func BenchmarkReplaceNoncePlaceholder(b *testing.B) {
	html := []byte(`<!DOCTYPE html><html><head><script nonce="__CSP_NONCE_VALUE__">window.__APP_CONFIG__={"test":"data"};</script></head><body></body></html>`)
	nonce := "abcdefghijklmnop123456=="

	b.ResetTimer()
	for range b.N {
		replaceNoncePlaceholder(html, nonce)
	}
}

// BenchmarkFrontendServerServeIndexHTML repeatedly renders the root entry with
// a fixed provider and nonce after construction leaves the timed region. Its
// measured calls include recorder/context construction, initial cache
// population, and later cache-hit page serving.
func BenchmarkFrontendServerServeIndexHTML(b *testing.B) {
	provider := &mockSettingsProvider{
		settings: map[string]string{"test": "value"},
	}

	// This benchmark relies on the embed fixture having a readable entry. It
	// discards the construction error before timing; ordinary constructor tests validate those initialized fields.
	server, _ := NewFrontendServer(provider)

	b.ResetTimer()
	for range b.N {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Set(middleware.CSPNonceKey, "test-nonce")

		server.serveIndexHTML(c)
	}
}

// TestFrontendServerLateHTMLPublication blocks the first settings read,
// invalidates the page cache, and releases that old result. A following request
// must read and return the newer site name rather than reuse the stale render.
func TestFrontendServerLateHTMLPublication(t *testing.T) {
	p := &blockingPublic{entered: make(chan struct{}), release: make(chan struct{})}
	s, err := NewFrontendServer(p)
	require.NoError(t, err)
	call := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		s.serveIndexHTML(c)
		return w
	}
	done := make(chan struct{})
	// Wait for the first provider read to block before invalidating. Join its
	// render completion after release so the next request tests the late-publication decision.
	go func() { call(); close(done) }()
	<-p.entered
	s.InvalidateCache()
	close(p.release)
	<-done
	w := call()
	require.True(t, strings.Contains(w.Body.String(), "NEW-TEST"), "旧回源覆盖失效，后续请求未取得新配置")
}

// GetPublicSettingsForInjection increments the fixture's unsynchronized call
// count and returns settings and err as configured. It ignores ctx; use this
// fixture serially, keeping shared settings stable while rendering encodes
// them.
func (m *mockSettingsProvider) GetPublicSettingsForInjection(ctx context.Context) (any, error) {
	m.called++
	return m.settings, m.err
}

// GetPublicSettingsForInjection returns old settings after its first-call gate
// is released and new settings thereafter. The fixture owner initializes
// entered and release before use, then closes release to let that first call
// finish.
//
// An atomic invocation count identifies the first caller, which closes entered
// to notify the controlling test before waiting. The supplied context is
// ignored, so cancellation cannot unblock the gate; the test owns its release
// even when request cancellation occurs. Later calls return new settings
// immediately.
func (p *blockingPublic) GetPublicSettingsForInjection(context.Context) (any, error) {
	// Let the first caller announce its blocked read before returning old settings.
	// The controlling test releases it after invalidation, while later calls return the newer fixture.
	if p.calls.Add(1) == 1 {
		close(p.entered)
		<-p.release
		return map[string]any{"site_name": "OLD-TEST"}, nil
	}
	return map[string]any{"site_name": "NEW-TEST"}, nil
}

// TestFrontendServer_SocialMetadataCache serves multiple destinations from one language cache.
// Final validators distinguish host and path, and invalidation publishes updated sharing copy without retaining stale metadata or cover URLs.
func TestFrontendServer_SocialMetadataCache(t *testing.T) {
	provider := &mockSettingsProvider{settings: map[string]string{
		"site_name": "Site", "site_share_title": "Before", "site_share_image_url": "/api/v1/settings/share-image/first",
	}}
	server, err := NewFrontendServer(provider)
	require.NoError(t, err)
	router := gin.New()
	router.GET("/*path", server.serveIndexHTML)
	request := func(address, etag string) *httptest.ResponseRecorder {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, address, nil)
		req.Header.Set("If-None-Match", etag)
		router.ServeHTTP(recorder, req)
		return recorder
	}
	first := request("https://one.test/models?private=secret", "")
	require.Contains(t, first.Body.String(), `property="og:title" content="Before"`)
	require.Contains(t, first.Body.String(), `property="og:url" content="https://one.test/models"`)
	require.NotContains(t, first.Body.String(), "secret")
	etag := first.Header().Get("ETag")
	require.Equal(t, http.StatusNotModified, request("https://one.test/models", etag).Code)
	for _, address := range []string{"https://two.test/models", "https://one.test/other"} {
		changed := request(address, etag)
		require.Equal(t, http.StatusOK, changed.Code)
		require.NotEqual(t, etag, changed.Header().Get("ETag"))
	}
	require.Equal(t, 1, provider.called)
	provider.settings = map[string]string{"site_name": "Site", "site_share_title": "After"}
	server.InvalidateCache()
	changed := request("https://one.test/models", etag)
	require.Equal(t, http.StatusOK, changed.Code)
	require.Contains(t, changed.Body.String(), `property="og:title" content="After"`)
	require.Contains(t, changed.Body.String(), `name="twitter:card" content="summary"`)
	require.NotContains(t, changed.Body.String(), "share-image/first")
	require.Equal(t, 2, provider.called)
}
