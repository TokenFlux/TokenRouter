package middleware

import (
	"log"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/server/httpconfig"
)

// cors.go applies configured browser cross-origin permissions and answers
// preflight requests.
// Cross-origin permissions depend on the normalized origin list and whether
// credentials may accompany requests. An exact origin is echoed with Vary:
// Origin; a wildcard grants every origin and disables the credentials header.
// Empty configuration produces no permission headers, and a denied ordinary
// request still reaches its route handler.
//
// OPTIONS requests end at this middleware with the origin decision, allowing a
// browser preflight to finish before endpoint handling. Approved responses
// expose download filename, request-correlation, timing, and plugin-response origin
// headers, and advertise the application and SDK request headers accepted by the
// browser policy. Configuration
// warnings are emitted through a process-wide once guard when CORS is first
// constructed.

// corsWarningOnce emits configuration warnings during the first CORS
// construction across the process's middleware instances.
var corsWarningOnce sync.Once

// CORS snapshots the configured origin policy and returns middleware that
// answers browser preflight requests. Origins are trimmed, exact origins are
// case-sensitive, and an asterisk takes precedence over any specific entries.
// Wildcard mode clears credential permission because browsers cannot use that
// combination.
//
// Approved origins receive the advertised methods, request headers, exposed
// response headers, and one-day preflight cache duration. OPTIONS ends with 204
// or 403 according to that decision; ordinary requests continue even when no
// CORS permission is added. Only the first construction emits configuration
// warnings.
func CORS(cfg httpconfig.CORSConfig) gin.HandlerFunc {
	allowedOrigins := normalizeOrigins(cfg.AllowedOrigins)
	allowAll := slices.Contains(allowedOrigins, "*")
	wildcardWithSpecific := allowAll && len(allowedOrigins) > 1
	// An asterisk already admits every origin. Collapse mixed configuration to
	// that policy so the specific entries do not suggest a narrower permission.
	if wildcardWithSpecific {
		allowedOrigins = []string{"*"}
	}
	allowCredentials := cfg.AllowCredentials

	corsWarningOnce.Do(func() {
		if len(allowedOrigins) == 0 {
			log.Println("Warning: CORS allowed_origins not configured; cross-origin requests will be rejected.")
		}
		if wildcardWithSpecific {
			log.Println("Warning: CORS allowed_origins includes '*'; wildcard will take precedence over explicit origins.")
		}
		if allowAll && allowCredentials {
			log.Println("Warning: CORS allowed_origins set to '*', disabling allow_credentials.")
		}
	})
	// Browsers reject credentialed wildcard permissions. Keep the advertised
	// wildcard origin and suppress credential permission when the configuration
	// requests both together.
	if allowAll && allowCredentials {
		allowCredentials = false
	}

	allowedSet := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if origin == "" || origin == "*" {
			continue
		}
		allowedSet[origin] = struct{}{}
	}
	allowHeaders := []string{
		"Content-Type", "Content-Length", "Accept-Encoding", "X-CSRF-Token", "Authorization", "X-Creative-Workspace-ID", "Idempotency-Key", "MCP-Protocol-Version", "Mcp-Session-Id", "Last-Event-ID",
		"accept", "origin", "Cache-Control", "X-Requested-With", "X-API-Key", "X-Admin-UI-Request", "X-User-UI-Request",
		"X-Request-ID", "X-Client-Request-ID",
		"TypeSafe-Danger-Zone-Images",
	}
	// Include the SDK metadata names in preflight permissions so browser clients
	// can send the enumerated x-stainless properties alongside their API request.
	openAIProperties := []string{
		"lang", "package-version", "os", "arch", "retry-count", "runtime",
		"runtime-version", "async", "helper-method", "poll-helper", "custom-poll-interval", "timeout",
	}
	for _, prop := range openAIProperties {
		allowHeaders = append(allowHeaders, "x-stainless-"+prop)
	}
	allowHeadersValue := strings.Join(allowHeaders, ", ")

	return func(c *gin.Context) {
		origin := strings.TrimSpace(c.GetHeader("Origin"))
		originAllowed := allowAll
		if origin != "" && !allowAll {
			_, originAllowed = allowedSet[origin]
		}

		if originAllowed {
			if allowAll {
				c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Add("Vary", "Origin")
			}
			if allowCredentials {
				c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			}
			c.Writer.Header().Set("Access-Control-Allow-Headers", allowHeadersValue)
			c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE, PATCH")
			c.Writer.Header().Set("Access-Control-Expose-Headers", "Content-Disposition, WWW-Authenticate, Mcp-Session-Id, ETag, Server-Timing, X-Request-ID, X-TokenRouter-Request-ID, X-Sub2API-Request-ID, X-Client-Request-ID, X-TokenRouter-Plugin-Response")
			// Cache approved browser preflight permissions for one day.
			c.Writer.Header().Set("Access-Control-Max-Age", "86400")
		}
		// A browser preflight asks for permissions before sending its operation.
		// Finish that decision here so OPTIONS does not reach the endpoint handler.
		if c.Request.Method == http.MethodOptions {
			if originAllowed {
				c.AbortWithStatus(http.StatusNoContent)
			} else {
				c.AbortWithStatus(http.StatusForbidden)
			}
			return
		}

		c.Next()
	}
}

// normalizeOrigins returns trimmed nonempty entries in their supplied order.
// Empty input returns nil; duplicates and wildcard entries remain for CORS to
// interpret when constructing its origin policy.
func normalizeOrigins(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			continue
		}
		normalized = append(normalized, trimmed)
	}
	return normalized
}
