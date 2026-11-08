package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// request_origin_test.go verifies origin capture independently from client IP
// compatibility settings. Forwarding headers affect public metadata only when the
// direct peer belongs to the explicit trusted proxy list; malformed lists fail closed.

// TestRequestOrigin checks trusted, untrusted, and malformed proxy configurations.
// Untrusted host and protocol headers must never control the resulting public origin used by sharing tags.
func TestRequestOrigin(t *testing.T) {
	for _, tc := range []struct {
		name     string   // scenario display name
		peers    []string // trusted peer list
		expected string   // resulting public origin
	}{
		{"untrusted", nil, "http://internal.test"},
		{"trusted", []string{"127.0.0.1/32"}, "https://public.test"},
		{"invalid list", []string{"127.0.0.1", "invalid"}, "http://internal.test"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			router := gin.New()
			router.Use(RequestOrigin(tc.peers))
			router.GET("/", func(c *gin.Context) { c.String(http.StatusOK, GetRequestOrigin(c)) })
			request := httptest.NewRequest(http.MethodGet, "http://internal.test/", nil)
			request.RemoteAddr = "127.0.0.1:1234"
			request.Header.Set("X-Forwarded-Host", "public.test")
			request.Header.Set("X-Forwarded-Proto", "https")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			require.Equal(t, tc.expected, response.Body.String())
		})
	}
}
