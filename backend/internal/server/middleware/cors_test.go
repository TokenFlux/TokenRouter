package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"

	"github.com/TokenFlux/TokenRouter/internal/config"
)

// cors_test.go checks browser permission headers that depend on the request origin, wildcard policy, and
// credential flag. These cases check preflight termination separately from
// ordinary responses and verify the advertised application headers, cache
// duration, and Vary behavior.

// TestCORS_DisallowedOrigin_NoAllowHeaders supplies disallowed or missing
// origins for preflight and ordinary methods. None of these responses receive
// the headers that would grant a browser cross-origin access.
func TestCORS_DisallowedOrigin_NoAllowHeaders(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	tests := []struct {
		name   string // subtest case label
		method string // request HTTP method
		origin string // incoming browser origin
	}{
		{
			name:   "preflight_disallowed_origin",
			method: http.MethodOptions,
			origin: "https://evil.example.com",
		},
		{
			name:   "get_disallowed_origin",
			method: http.MethodGet,
			origin: "https://evil.example.com",
		},
		{
			name:   "post_disallowed_origin",
			method: http.MethodPost,
			origin: "https://attacker.example.com",
		},
		{
			name:   "preflight_no_origin",
			method: http.MethodOptions,
			origin: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tt.method, "/", nil)
			if tt.origin != "" {
				c.Request.Header.Set("Origin", tt.origin)
			}

			middleware(c)

			// A disallowed origin receives no permission headers or cache duration.
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"),
				"不允许的 origin 不应收到 Allow-Headers")
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Methods"),
				"不允许的 origin 不应收到 Allow-Methods")
			assert.Empty(t, w.Header().Get("Access-Control-Max-Age"),
				"不允许的 origin 不应收到 Max-Age")
			assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"),
				"不允许的 origin 不应收到 Allow-Origin")
		})
	}
}

// TestCORS_AllowedOrigin_HasAllowHeaders exercises an exact permitted origin
// across preflight and ordinary requests. Each response advertises the
// application request headers, exposed timing header, methods, and one-day
// cache duration.
func TestCORS_AllowedOrigin_HasAllowHeaders(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	tests := []struct {
		name   string // subtest case label
		method string // request HTTP method
	}{
		{name: "preflight_OPTIONS", method: http.MethodOptions},
		{name: "normal_GET", method: http.MethodGet},
		{name: "normal_POST", method: http.MethodPost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest(tt.method, "/", nil)
			c.Request.Header.Set("Origin", "https://allowed.example.com")
			if tt.method == http.MethodOptions {
				c.Request.Header.Set("Access-Control-Request-Headers", "X-Creative-Workspace-ID")
			}

			middleware(c)

			// An allowed origin receives method, header, and cache-duration permissions.
			assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"),
				"允许的 origin 应收到 Allow-Headers")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-Admin-UI-Request")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-User-UI-Request")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "X-Creative-Workspace-ID")
			assert.Contains(t, w.Header().Get("Access-Control-Allow-Headers"), "Idempotency-Key")
			assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"),
				"允许的 origin 应收到 Allow-Methods")
			assert.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "Server-Timing")
			assert.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "Content-Disposition")
			assert.Contains(t, w.Header().Get("Access-Control-Expose-Headers"), "X-TokenRouter-Plugin-Response")
			assert.Equal(t, "86400", w.Header().Get("Access-Control-Max-Age"),
				"允许的 origin 应收到 Max-Age=86400")
			assert.Equal(t, "https://allowed.example.com", w.Header().Get("Access-Control-Allow-Origin"),
				"允许的 origin 应收到 Allow-Origin")
		})
	}
}

// TestCORS_PreflightDisallowedOrigin_ReturnsForbidden sends OPTIONS from an
// origin outside the configured list. The isolated middleware context receives
// a 403 response for that preflight request.
func TestCORS_PreflightDisallowedOrigin_ReturnsForbidden(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/", nil)
	c.Request.Header.Set("Origin", "https://evil.example.com")

	middleware(c)

	assert.Equal(t, http.StatusForbidden, w.Code,
		"不允许的 origin 的 preflight 请求应返回 403")
}

// TestCORS_PreflightAllowedOrigin_ReturnsNoContent sends OPTIONS from the
// configured exact origin. The isolated middleware context receives a 204
// response for that permitted preflight request.
func TestCORS_PreflightAllowedOrigin_ReturnsNoContent(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodOptions, "/", nil)
	c.Request.Header.Set("Origin", "https://allowed.example.com")

	middleware(c)

	assert.Equal(t, http.StatusNoContent, w.Code,
		"允许的 origin 的 preflight 请求应返回 204")
}

// TestCORS_WildcardOrigin_AllowsAny uses an asterisk and sends a concrete
// browser origin. The response advertises the wildcard together with
// request-header and method permissions.
func TestCORS_WildcardOrigin_AllowsAny(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://any-origin.example.com")

	middleware(c)

	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"),
		"通配符配置应返回 *")
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"),
		"通配符 origin 应设置 Allow-Headers")
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"),
		"通配符 origin 应设置 Allow-Methods")
}

// TestCORS_AllowCredentials_SetCorrectly enables credentials for an exact
// origin and also sends a disallowed origin. Credential permission appears for
// the allowed request and is absent from the denied origin's response.
func TestCORS_AllowCredentials_SetCorrectly(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: true,
	}
	middleware := CORS(cfg)

	t.Run("allowed_origin_gets_credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://allowed.example.com")

		middleware(c)

		assert.Equal(t, "true", w.Header().Get("Access-Control-Allow-Credentials"),
			"允许的 origin 且开启 credentials 应设置 Allow-Credentials")
	})

	t.Run("disallowed_origin_no_credentials", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://evil.example.com")

		middleware(c)

		assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"),
			"不允许的 origin 不应收到 Allow-Credentials")
	})
}

// TestCORS_WildcardWithCredentials_DisablesCredentials configures both wildcard
// origins and credential permission. The middleware suppresses the credentials
// header so its wildcard response remains usable by browsers.
func TestCORS_WildcardWithCredentials_DisablesCredentials(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"*"},
		AllowCredentials: true,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://any.example.com")

	middleware(c)

	// Exercise the incompatible flags together. Browsers reject a credentialed
	// wildcard response, so the middleware clears credential permission during
	// construction.
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"),
		"通配符 origin 应禁用 Allow-Credentials")
}

// TestCORS_MultipleAllowedOrigins configures two exact origins and exercises
// each plus an unlisted origin. Each configured origin is echoed, while the
// unlisted request receives no origin or request-header permission.
func TestCORS_MultipleAllowedOrigins(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins: []string{
			"https://app1.example.com",
			"https://app2.example.com",
		},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	t.Run("first_origin_allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app1.example.com")

		middleware(c)

		assert.Equal(t, "https://app1.example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("second_origin_allowed", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app2.example.com")

		middleware(c)

		assert.Equal(t, "https://app2.example.com", w.Header().Get("Access-Control-Allow-Origin"))
		assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})

	t.Run("unlisted_origin_rejected", func(t *testing.T) {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
		c.Request.Header.Set("Origin", "https://app3.example.com")

		middleware(c)

		assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, w.Header().Get("Access-Control-Allow-Headers"))
	})
}

// TestCORS_VaryHeader_SetForSpecificOrigin sends a request from an exact
// allowed origin. The response includes Vary: Origin so caches can distinguish
// permissions for different browser origins.
func TestCORS_VaryHeader_SetForSpecificOrigin(t *testing.T) {
	cfg := config.CORSConfig{
		AllowedOrigins:   []string{"https://allowed.example.com"},
		AllowCredentials: false,
	}
	middleware := CORS(cfg)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	c.Request.Header.Set("Origin", "https://allowed.example.com")

	middleware(c)

	assert.Contains(t, w.Header().Values("Vary"), "Origin",
		"非通配符允许的 origin 应设置 Vary: Origin")
}

// TestNormalizeOrigins supplies empty slices, padded origins, and blank entries
// to normalization. Empty input stays nil, whitespace is removed, and
// meaningful entries keep their input order.
func TestNormalizeOrigins(t *testing.T) {
	tests := []struct {
		name   string   // subtest case label
		input  []string // configured origin entries
		expect []string // expected normalized entries
	}{
		{name: "nil_input", input: nil, expect: nil},
		{name: "empty_input", input: []string{}, expect: nil},
		{name: "trims_whitespace", input: []string{" https://a.com ", "  https://b.com"}, expect: []string{"https://a.com", "https://b.com"}},
		{name: "removes_empty_strings", input: []string{"", "  ", "https://a.com"}, expect: []string{"https://a.com"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := normalizeOrigins(tt.input)
			assert.Equal(t, tt.expect, result)
		})
	}
}
