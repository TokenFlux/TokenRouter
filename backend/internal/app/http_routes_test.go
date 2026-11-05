package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/config"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
)

// Gateway route guards classify aliases and reject disabled protocols using the
// client's error format before body processing. These scenarios distinguish
// route registration from successful execution, cover method and path
// admission, and inspect retired paths separately from existing resource
// operations and generation endpoints.

// Protocol error aliases select the expected Anthropic, OpenAI, and Google
// envelopes in group-admission denial scenarios.
const (
	groupClientProtocolErrorAnthropic = gatewayhttp.GroupClientProtocolErrorAnthropic // Messages denial envelope
	groupClientProtocolErrorOpenAI    = gatewayhttp.GroupClientProtocolErrorOpenAI    // OpenAI denial envelope
	groupClientProtocolErrorGoogle    = gatewayhttp.GroupClientProtocolErrorGoogle    // Google denial envelope
)

// Route classification aliases and request-reading guard callbacks connect
// protocol admission scenarios to the production gateway adapters.
var (
	routeProtocol = gatewayhttp.RouteProtocol

	// Protocol checks use stateless adapters that read the current request.
	testRouteGuards                      = gatewayhttp.NewRouteGuards(legacyRouteMiddleware(nil, nil, nil, nil, nil, &config.Config{}))
	requireGroupClientProtocol           = testRouteGuards.RequireGroupClientProtocol
	extendedRouteProtocol                = gatewayhttp.ExtendedRouteProtocol
	requireGeminiGenerateContentProtocol = testRouteGuards.RequireGeminiGenerateContentProtocol
)

// groupClientProtocolErrorFormat aliases the production error-envelope choice
// for route guards.
type groupClientProtocolErrorFormat = gatewayhttp.GroupClientProtocolErrorFormat

// TestAuthRoutesRateLimitFailCloseWhenRedisUnavailable points Redis at an
// unavailable local port and requests eight public authentication paths. Every
// request must return TooManyRequests and the limiter message before ordinary
// business validation.
func TestAuthRoutesRateLimitFailCloseWhenRedisUnavailable(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{
		Addr:         "127.0.0.1:1",
		DialTimeout:  50 * time.Millisecond,
		ReadTimeout:  50 * time.Millisecond,
		WriteTimeout: 50 * time.Millisecond,
	})
	t.Cleanup(func() {
		// The fixture client owns its local connection state. Close it after
		// route assertions and discard the cleanup error from the intentionally unavailable Redis endpoint.
		_ = rdb.Close()
	})

	router := newAuthRoutesTestRouter(rdb)
	paths := []string{
		"/api/v1/auth/register",
		"/api/v1/auth/login",
		"/api/v1/auth/login/2fa",
		"/api/v1/auth/passkey/login/begin",
		"/api/v1/auth/passkey/login/finish",
		"/api/v1/auth/send-verify-code",
		"/api/v1/auth/oauth/google/one-tap",
		"/api/v1/auth/oauth/pending/send-verify-code",
	}

	for _, path := range paths {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		req.RemoteAddr = "203.0.113.10:12345"

		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		require.Equal(t, http.StatusTooManyRequests, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "rate limit exceeded", "path=%s", path)
	}
}

// TestGatewayRoutesIgnoreRequestLanguage requests disabled OpenAI protocols and
// aliases with a Chinese language header. It checks Forbidden, English
// Content-Language and error text, the protocol code, and absence of Han
// characters.
func TestGatewayRoutesIgnoreRequestLanguage(t *testing.T) {
	router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{ID: 1, AllowedProtocols: []protocolcore.ProtocolID{}})
	for _, path := range []string{"/v1/responses", "/responses", "/backend-api/codex/responses", "/v1/chat/completions"} {
		request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"test"}`))
		request.Header.Set("Accept-Language", "zh-CN")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		require.Equal(t, http.StatusForbidden, response.Code)
		require.Equal(t, "en", response.Header().Get("Content-Language"))
		require.Contains(t, response.Body.String(), "protocol_not_allowed")
		require.NotRegexp(t, `[\p{Han}]`, response.Body.String())
	}
}

// newGatewayRoutesTestRouterWithConfig delegates configured route fixtures to
// the shared option helper.
func newGatewayRoutesTestRouterWithConfig(cfg *config.Config, platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithOptions(cfg, platform...)
}

// TestGatewayRoutesClientProtocolGateRejectsAliasesBeforeReadingBody supplies
// groups missing Messages, Responses, or Chat permission across their aliases.
// It checks each request receives the expected protocol error with Forbidden
// and an unread tracked body.
func TestGatewayRoutesClientProtocolGateRejectsAliasesBeforeReadingBody(t *testing.T) {
	tests := []struct {
		name      string                    // denied client protocol
		platform  string                    // unused platform scenario label
		protocols []protocolcore.ProtocolID // enabled group protocols
		paths     []string                  // equivalent request aliases
		code      string                    // expected denial code
	}{
		{
			name:      "messages",
			platform:  capability.PlatformOpenAI,
			protocols: []protocolcore.ProtocolID{protocolcore.ProtocolOpenAIResponses, protocolcore.ProtocolOpenAIChatCompletions},
			paths:     []string{"/v1/messages", "/v1/messages/count_tokens", "/messages/count_tokens", "/antigravity/v1/messages"},
			code:      "permission_error",
		},
		{
			name:      "responses",
			platform:  capability.PlatformQoder,
			protocols: []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages, protocolcore.ProtocolOpenAIChatCompletions},
			paths:     []string{"/v1/responses", "/v1/responses/compact", "/responses", "/responses/compact", "/backend-api/codex/responses", "/backend-api/codex/responses/compact"},
			code:      "protocol_not_allowed",
		},
		{
			name:      "chat_completions",
			platform:  capability.PlatformQoder,
			protocols: []protocolcore.ProtocolID{protocolcore.ProtocolAnthropicMessages, protocolcore.ProtocolOpenAIResponses},
			paths:     []string{"/v1/chat/completions", "/chat/completions"},
			code:      "protocol_not_allowed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(1)
			router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{
				ID:               groupID,
				AllowedProtocols: tt.protocols,
			})
			for _, path := range tt.paths {
				reader := &protocolGateTrackingReader{}
				req := httptest.NewRequest(http.MethodPost, path, reader)
				req.Header.Set("Content-Type", "application/json")
				w := httptest.NewRecorder()

				router.ServeHTTP(w, req)

				require.Equal(t, http.StatusForbidden, w.Code, "path=%s", path)
				require.Contains(t, w.Body.String(), tt.code, "path=%s", path)
				require.False(t, reader.read, "path=%s must be rejected before reading body", path)
			}
		})
	}
}

// TestGatewayRoutesCountTokensHonorsProtocolGate requests counting aliases with
// every group protocol disabled. It checks the Messages permission envelope
// takes precedence over later counting support and leaves each tracked body
// unread.
func TestGatewayRoutesCountTokensHonorsProtocolGate(t *testing.T) {
	tests := []struct {
		name     string // counting route scenario
		platform string // unused platform scenario label
		path     string // requested counting alias
	}{
		{name: "qoder_v1", platform: capability.PlatformQoder, path: "/v1/messages/count_tokens"},
		{name: "qoder_alias", platform: capability.PlatformQoder, path: "/messages/count_tokens"},
		{name: "antigravity_v1", platform: capability.PlatformAntigravity, path: "/v1/messages/count_tokens"},
		{name: "antigravity_alias", platform: capability.PlatformAntigravity, path: "/messages/count_tokens"},
		{name: "forced_antigravity", platform: capability.PlatformOpenAI, path: "/antigravity/v1/messages/count_tokens"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			groupID := int64(1)
			router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{
				ID:               groupID,
				AllowedProtocols: []protocolcore.ProtocolID{},
			})
			reader := &protocolGateTrackingReader{}
			req := httptest.NewRequest(http.MethodPost, tt.path, reader)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), "permission_error")
			require.NotContains(t, w.Body.String(), "protocol_not_allowed")
			require.False(t, reader.read, "unsupported count_tokens must not read request body")
		})
	}
}

// TestGatewayRoutesResponsesSubpathGuardRunsBeforeProtocolGate requests encoded
// unsupported subpaths while Responses permission is disabled. It checks
// NotFound and the subpath message appear before a protocol_not_allowed
// rejection can be returned.
func TestGatewayRoutesResponsesSubpathGuardRunsBeforeProtocolGate(t *testing.T) {
	groupID := int64(1)
	router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{
		ID: groupID,
		AllowedProtocols: []protocolcore.ProtocolID{
			protocolcore.ProtocolAnthropicMessages,
			protocolcore.ProtocolOpenAIChatCompletions,
		},
	})

	for _, path := range []string{"/v1/responses/%3fa=b", "/responses/%3fa=b", "/backend-api/codex/responses/%3fa=b"} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))

		require.Equal(t, http.StatusNotFound, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "Unsupported responses subpath", "path=%s", path)
		require.NotContains(t, w.Body.String(), "protocol_not_allowed", "path=%s", path)
	}
}

// TestRequireGroupClientProtocolUsesNativeErrorEnvelopes denies a configured
// protocol under three client formats. It checks Forbidden and format-specific
// body text, plus the LocalPolicyDenied business-observation reason after the
// guard finishes.
func TestRequireGroupClientProtocolUsesNativeErrorEnvelopes(t *testing.T) {
	tests := []struct {
		name     string                         // client envelope scenario
		protocol protocolcore.ProtocolID        // denied group protocol
		format   groupClientProtocolErrorFormat // selected client envelope
		contains []string                       // expected message fragments
	}{
		{"anthropic", protocolcore.ProtocolAnthropicMessages, groupClientProtocolErrorAnthropic, []string{"permission_error", "Anthropic Messages"}},
		{"openai", protocolcore.ProtocolOpenAIResponses, groupClientProtocolErrorOpenAI, []string{"protocol_not_allowed", "OpenAI Responses"}},
		{"google", protocolcore.ProtocolGeminiGenerateContent, groupClientProtocolErrorGoogle, []string{"PERMISSION_DENIED", "Gemini GenerateContent"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			var deniedReason string
			router.Use(func(c *gin.Context) {
				c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{AllowedProtocols: []protocolcore.ProtocolID{}}})
				c.Next()
				deniedReason = c.GetString(gatewayhttp.OpsClientBusinessLimitedReasonKey)
			})
			router.POST("/", requireGroupClientProtocol(tt.protocol, tt.format), func(c *gin.Context) {
				c.Status(http.StatusNoContent)
			})

			w := httptest.NewRecorder()
			router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/", nil))

			require.Equal(t, http.StatusForbidden, w.Code)
			for _, value := range tt.contains {
				require.Contains(t, w.Body.String(), value)
			}
			require.Equal(t, gatewayhttp.OpsClientBusinessLimitedReasonLocalPolicyDenied, deniedReason)
		})
	}
}

// TestRequireGeminiGenerateContentProtocolOnlyGatesTextActions uses an empty
// group permission set for model actions. It checks generate, stream, and
// counting actions return PERMISSION_DENIED while an unrelated custom action
// reaches NoContent.
func TestRequireGeminiGenerateContentProtocolOnlyGatesTextActions(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
			Group: &routing.Group{AllowedProtocols: []protocolcore.ProtocolID{}},
		})
		c.Next()
	})
	router.POST("/v1beta/models/*modelAction", requireGeminiGenerateContentProtocol, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	for _, action := range []string{"generateContent", "streamGenerateContent", "countTokens"} {
		w := httptest.NewRecorder()
		path := "/v1beta/models/gemini-2.5-pro:" + action
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))

		require.Equal(t, http.StatusForbidden, w.Code, "action=%s", action)
		require.Contains(t, w.Body.String(), "PERMISSION_DENIED", "action=%s", action)
	}

	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/v1beta/models/gemini-2.5-pro:customAction", nil))
	require.Equal(t, http.StatusNoContent, w.Code)
}

// TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered requests Compact and
// Codex Responses aliases through assembled route fixtures. It checks each
// avoids NotFound, establishing route dispatch without asserting successful
// provider execution.
func TestGatewayRoutesOpenAIResponsesCompactPathIsRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/responses/compact",
		"/responses/compact",
		"/backend-api/codex/responses",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI responses handler", path)
	}
}

// TestGatewayRoutesQoderPathsAreRegistered sends Messages and Chat bodies
// through the Qoder-oriented fixture and aliases. It checks each route avoids
// NotFound rather than requiring configured provider execution to succeed.
func TestGatewayRoutesQoderPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformQoder)

	for _, tc := range []struct {
		path string // compatibility request alias
		body string // protocol request bytes
	}{
		{path: "/v1/messages", body: `{"model":"claude-sonnet-4-5","max_tokens":1,"messages":[{"role":"user","content":"hi"}]}`},
		{path: "/v1/chat/completions", body: `{"model":"gpt-5-codex","messages":[{"role":"user","content":"hi"}]}`},
		{path: "/chat/completions", body: `{"model":"gpt-5-codex","messages":[{"role":"user","content":"hi"}]}`},
	} {
		req := httptest.NewRequest(http.MethodPost, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Qoder handler", tc.path)
	}
}

// TestGatewayRoutesQoderResponsesSubpathsAreRejected requests Compact aliases
// under the Qoder-oriented group fixture. It checks Forbidden and
// protocol_not_allowed for each unsupported Responses subpath before execution.
func TestGatewayRoutesQoderResponsesSubpathsAreRejected(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformQoder)

	for _, path := range []string{
		"/v1/responses/compact",
		"/responses/compact",
		"/backend-api/codex/responses/compact",
	} {
		req := httptest.NewRequest(http.MethodPost, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code, "path=%s should reject unsupported Qoder Responses subpath", path)
		require.Contains(t, w.Body.String(), "protocol_not_allowed")
	}
}

// TestGatewayRoutesQoderResponsesWebSocketIsRejected requests GET Responses
// aliases under the Qoder-oriented group fixture. Each must return Forbidden
// and protocol_not_allowed instead of entering a WebSocket execution path.
func TestGatewayRoutesQoderResponsesWebSocketIsRejected(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformQoder)

	for _, path := range []string{
		"/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusForbidden, w.Code, "path=%s should reject Qoder Responses websocket", path)
		require.Contains(t, w.Body.String(), "protocol_not_allowed")
	}
}

// TestGatewayRoutesNonNativeResponsesWebSocketIsRejected requests Responses GET
// under the Anthropic-oriented route fixture. It checks Forbidden and
// protocol_not_allowed for the group's unsupported WebSocket protocol.
func TestGatewayRoutesNonNativeResponsesWebSocketIsRejected(t *testing.T) {
	router := newGatewayRoutesTestRouterWithOptions(&config.Config{}, capability.PlatformAnthropic)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/responses", nil))

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "protocol_not_allowed")
}

// TestGatewayRoutesOpenAIAlphaSearchPathsAreRegistered inspects the assembled
// POST route inventory. It checks versioned, bare, and Codex Alpha Search paths
// are present without issuing their execution requests.
func TestGatewayRoutesOpenAIAlphaSearchPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		if route.Method == http.MethodPost {
			registered[route.Path] = true
		}
	}

	for _, path := range []string{
		"/v1/alpha/search",
		"/alpha/search",
		"/backend-api/codex/alpha/search",
	} {
		require.True(t, registered[path], "POST %s should be registered", path)
	}
}

// TestGatewayRoutesAlphaSearchRejectsNonOpenAIGroup requests Alpha Search under
// the Grok-oriented group fixture. It checks Forbidden and
// protocol_not_allowed, limiting this scenario to observed group-protocol
// rejection.
func TestGatewayRoutesAlphaSearchRejectsNonOpenAIGroup(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformGrok)
	req := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(`{"model":"gpt-5.6-sol"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
	require.Contains(t, w.Body.String(), "protocol_not_allowed")
}

// TestGatewayRoutesOpenAIImagesPathsAreRegistered requests versioned and bare
// image-generation/edit aliases. It checks dispatch avoids NotFound for every
// path, without asserting successful image generation by the fixture.
func TestGatewayRoutesOpenAIImagesPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-image-2","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit OpenAI images handler", path)
	}
}

// TestGatewayRoutesAsyncImagesPathsAreRemoved inspects and requests retired
// asynchronous image patterns and concrete paths. It checks absence plus
// NotFound, while the independent batch-image submission and resource routes
// remain registered.
func TestGatewayRoutesAsyncImagesPathsAreRemoved(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	removed := []struct {
		method      string // retired request method
		routePath   string // retired registration pattern
		requestPath string // concrete retired path
	}{
		{method: http.MethodPost, routePath: "/v1/images/generations/async", requestPath: "/v1/images/generations/async"},
		{method: http.MethodPost, routePath: "/v1/images/edits/async", requestPath: "/v1/images/edits/async"},
		{method: http.MethodGet, routePath: "/v1/images/tasks/:task_id", requestPath: "/v1/images/tasks/task-123"},
		{method: http.MethodPost, routePath: "/images/generations/async", requestPath: "/images/generations/async"},
		{method: http.MethodPost, routePath: "/images/edits/async", requestPath: "/images/edits/async"},
		{method: http.MethodGet, routePath: "/images/tasks/:task_id", requestPath: "/images/tasks/task-123"},
	}
	for _, route := range removed {
		routeKey := route.method + " " + route.routePath
		require.False(t, registered[routeKey], "%s should not be registered", routeKey)

		w := httptest.NewRecorder()
		req := httptest.NewRequest(route.method, route.requestPath, nil)
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", route.method, route.requestPath)
	}

	// Gemini batch image jobs have separate routes.
	for _, route := range []string{
		"POST /v1/images/batches",
		"GET /v1/images/batches",
		"GET /v1/images/batches/models",
		"GET /v1/images/batches/:id",
		"GET /v1/images/batches/:id/items",
		"GET /v1/images/batches/:id/items/:custom_id/content",
		"GET /v1/images/batches/:id/download",
		"POST /v1/images/batches/:id/cancel",
		"DELETE /v1/images/batches/:id",
		"DELETE /v1/images/batches/:id/outputs",
	} {
		require.True(t, registered[route], "%s should remain registered", route)
	}
}

// TestGatewayRoutesBillingIntrospectionIsRemoved checks the retired public
// billing endpoint in the route inventory. It then requests that path and
// requires NotFound, preserving its removal from public API dispatch.
func TestGatewayRoutesBillingIntrospectionIsRemoved(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	for _, route := range router.Routes() {
		require.False(t, route.Method == http.MethodGet && route.Path == "/v1/sub2api/billing")
	}

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sub2api/billing", nil)
	router.ServeHTTP(w, req)
	require.Equal(t, http.StatusNotFound, w.Code)
}

// TestGatewayRoutesGrokImagesAndVideosPathsAreRegistered requests image/video
// creation and existing video-resource aliases under the Grok fixture. It
// checks dispatch avoids NotFound and the unsupported-platform message across
// both POST and GET paths.
func TestGatewayRoutesGrokImagesAndVideosPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformGrok)

	for _, path := range []string{
		"/v1/images/generations",
		"/v1/images/edits",
		"/images/generations",
		"/images/edits",
		"/v1/videos/generations",
		"/v1/videos",
		"/videos",
		"/videos/generations",
		"/v1/videos/edits",
		"/videos/edits",
		"/v1/videos/extensions",
		"/videos/extensions",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok-imagine","prompt":"draw a cat"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok media handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}

	for _, path := range []string{
		"/v1/videos/request-123",
		"/videos/request-123",
		"/v1/videos/generations/request-123",
		"/videos/generations/request-123",
		"/v1/videos/edits/request-123",
		"/videos/edits/request-123",
		"/v1/videos/extensions/request-123",
		"/videos/extensions/request-123",
		"/v1/videos/request-123/content",
		"/videos/request-123/content",
		"/v1/videos/generations/request-123/content",
		"/videos/generations/request-123/content",
		"/v1/videos/edits/request-123/content",
		"/videos/edits/request-123/content",
		"/v1/videos/extensions/request-123/content",
		"/videos/extensions/request-123/content",
	} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should hit Grok video handler", path)
		require.NotContains(t, w.Body.String(), "not supported for this platform")
	}
}

// TestGatewayRoutesVideosFollowProtocolAndResourceRules requests video creation
// and existing resource paths under the OpenAI-oriented fixture. It checks POST
// generation is denied by protocol policy while GET results omit the
// unsupported-platform message.
func TestGatewayRoutesVideosFollowProtocolAndResourceRules(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformOpenAI)

	for _, tc := range []struct {
		method string // resource request method
		path   string // generation or resource path
		body   string // supplied request body
	}{
		{http.MethodPost, "/v1/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/v1/videos", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/videos", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/videos/generations", `{"model":"grok-imagine-video-1.5","prompt":"waves"}`},
		{http.MethodPost, "/v1/videos/edits", `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://example.com/in.mp4"}}`},
		{http.MethodPost, "/videos/edits", `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://example.com/in.mp4"}}`},
		{http.MethodPost, "/v1/videos/extensions", `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://example.com/in.mp4"}}`},
		{http.MethodPost, "/videos/extensions", `{"model":"grok-imagine-video","prompt":"waves","video":{"url":"https://example.com/in.mp4"}}`},
		{http.MethodGet, "/v1/videos/request-123", ""},
		{http.MethodGet, "/videos/request-123", ""},
		{http.MethodGet, "/v1/videos/generations/request-123", ""},
		{http.MethodGet, "/videos/generations/request-123", ""},
		{http.MethodGet, "/v1/videos/edits/request-123", ""},
		{http.MethodGet, "/videos/edits/request-123", ""},
		{http.MethodGet, "/v1/videos/extensions/request-123", ""},
		{http.MethodGet, "/videos/extensions/request-123", ""},
		{http.MethodGet, "/v1/videos/request-123/content", ""},
		{http.MethodGet, "/videos/request-123/content", ""},
		{http.MethodGet, "/v1/videos/generations/request-123/content", ""},
		{http.MethodGet, "/videos/generations/request-123/content", ""},
		{http.MethodGet, "/v1/videos/edits/request-123/content", ""},
		{http.MethodGet, "/videos/edits/request-123/content", ""},
		{http.MethodGet, "/v1/videos/extensions/request-123/content", ""},
		{http.MethodGet, "/videos/extensions/request-123/content", ""},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		if tc.method == http.MethodPost {
			require.Equal(t, http.StatusForbidden, w.Code)
			require.Contains(t, w.Body.String(), "protocol_not_allowed")
		} else {
			require.NotContains(t, w.Body.String(), "not supported for this platform")
		}
	}
}

// TestGatewayRoutesGrokAllowsCLICompatibilityEntrypoints requests Messages,
// Chat, WS, Responses, and counting aliases under Grok-oriented fixtures. It
// checks route dispatch and platform presentation, while counting reaches the
// expected billing_service_error from incomplete dependencies.
func TestGatewayRoutesGrokAllowsCLICompatibilityEntrypoints(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformGrok)

	for _, tc := range []struct {
		method string // compatibility request method
		path   string // compatibility route alias
	}{
		{http.MethodPost, "/v1/messages"},
		{http.MethodPost, "/v1/chat/completions"},
		{http.MethodPost, "/chat/completions"},
		{http.MethodGet, "/v1/responses"},
		{http.MethodGet, "/responses"},
		{http.MethodGet, "/backend-api/codex/responses"},
	} {
		req := httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{"model":"grok"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.NotContains(t, w.Body.String(), "not supported for Grok groups")
	}

	countTokensRouter := newGatewayRoutesTestRouterWithConfig(&config.Config{Gateway: config.GatewayConfig{MaxRequestBodyBytes: 1024 * 1024}}, capability.PlatformGrok)
	for _, path := range []string{"/v1/messages/count_tokens", "/messages/count_tokens"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		countTokensRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, "path=%s", path)
		require.Contains(t, w.Body.String(), "billing_service_error")
	}

	for _, path := range []string{
		"/v1/responses",
		"/responses",
		"/backend-api/codex/responses",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok","input":"hi"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.NotEqual(t, http.StatusNotFound, w.Code, "path=%s should still reach Responses handler", path)
	}
}

// TestGatewayRoutesResponsesSubpathRejectsNonConformingSubpaths supplies
// traversal, encoded separators, fragments, and malformed Compact paths. It
// checks every request returns NotFound with the unsupported-subpath message
// before an execution handler can accept it.
func TestGatewayRoutesResponsesSubpathRejectsNonConformingSubpaths(t *testing.T) {
	router := newGatewayRoutesTestRouter()

	for _, path := range []string{
		"/v1/responses/../../x/y",
		"/v1/responses/..%2f..%2fx/y",
		"/v1/responses/%2e%2e/%2e%2e/x",
		"/responses/%2e%2e%2fx",
		"/backend-api/codex/responses/..%2f..%2fx",
		`/v1/responses/..\..\x`,
		"/v1/responses/%3fa=b",
		"/v1/responses/x%23frag",
		"/v1/responses/compact%2f..",
	} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"gpt-5"}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusNotFound, w.Code, "path=%s must be rejected at the edge", path)
		require.Contains(t, w.Body.String(), "Unsupported responses subpath", "path=%s", path)
	}
}

// TestGatewayRoutesOpenAICompatibleCountTokensPathIsRegistered requests the
// counting endpoint for four named platform cases. It checks each
// avoids NotFound, establishing shared route registration rather than
// successful token counting.
func TestGatewayRoutesOpenAICompatibleCountTokensPathIsRegistered(t *testing.T) {
	for _, platform := range []string{
		capability.PlatformOpenAI,
		capability.PlatformKimi,
		capability.PlatformZhipu,
		capability.PlatformDeepseek,
	} {
		t.Run(platform, func(t *testing.T) {
			router := newGatewayRoutesTestRouter(platform)
			req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(`{"model":"claude-sonnet-4-5","messages":[{"role":"user","content":"hi"}]}`))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)
			require.NotEqual(t, http.StatusNotFound, w.Code)
		})
	}
}

// RegisterUserRoutes mounts fixture user resources under JWT, backend-mode,
// panel, and audit guards. Supplied module handlers are registered in their
// route families, team receives step-up, and usage/creative routes receive the
// heavy-query limiter.
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *routeTestHandlers,
	jwtAuth identityhttp.JWTAuthMiddleware,
	auditLog servermiddleware.AuditLogMiddleware,
	stepUpAuth identityhttp.StepUpAuthMiddleware,
	settingService *admission.BackendMode,
	panelRateLimiter *servermiddleware.PanelRateLimiter,
) {
	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(identityhttp.BackendModeUserGuard(legacyBackendModeReader(settingService)))
	// Per-user panel limits bound database load from frequent requests.
	authenticated.Use(panelRateLimiter.Global())
	// Audit user mutations, including TOTP changes, step-up verification, and password changes.
	authenticated.Use(gin.HandlerFunc(auditLog))
	identityhttp.RegisterUserRoutes(authenticated, h.User, h.Totp, h.Passkey)
	promotionhttp.RegisterUserRoutes(authenticated, h.PromotionUser)
	keyhttp.RegisterUserRoutes(authenticated, h.APIKey)
	teamhttp.RegisterUserRoutes(authenticated, h.Team, gin.HandlerFunc(stepUpAuth))
	usagehttp.RegisterUserRoutes(authenticated, h.Usage, panelRateLimiter.Heavy())
	creativehttp.RegisterUserRoutes(authenticated, h.Creative, panelRateLimiter.Heavy())
	sitehttp.RegisterUserRoutes(authenticated, h.Announcement)
	billinghttp.RegisterUserRoutes(authenticated, h.Redeem, h.Subscription)
}

// TestProtocolAllPublicRoutesDeniedBeforeUpstream requests the listed public
// generation protocols and aliases with empty group permissions. It checks
// Forbidden for every path, and Gemini action dispatch uses a direct guard
// fixture with a terminal failure callback.
func TestProtocolAllPublicRoutesDeniedBeforeUpstream(t *testing.T) {
	paths := []struct {
		platform, method, path string // platform: unused scenario platform label; method: requested HTTP verb; path: public protocol endpoint
	}{
		{"openai", "POST", "/v1/messages"},
		{"openai", "POST", "/v1/responses"},
		{"openai", "POST", "/responses"},
		{"openai", "POST", "/backend-api/codex/responses"},
		{"openai", "POST", "/v1/chat/completions"},
		{"openai", "POST", "/chat/completions"},
		{"gemini", "POST", "/v1beta/models/gemini:generateContent"},
		{"gemini", "POST", "/v1beta/models/gemini:streamGenerateContent"},
		{"openai", "POST", "/v1/embeddings"},
		{"openai", "POST", "/embeddings"},
		{"openai", "POST", "/v1/images/generations"},
		{"openai", "POST", "/images/generations"},
		{"openai", "POST", "/v1/images/edits"},
		{"openai", "POST", "/images/edits"},
		{"gemini", "POST", "/v1/images/batches"},
		{"grok", "POST", "/v1/videos"},
		{"grok", "POST", "/videos"},
		{"grok", "POST", "/v1/videos/generations"},
		{"grok", "POST", "/videos/generations"},
		{"grok", "POST", "/v1/videos/edits"},
		{"grok", "POST", "/videos/edits"},
		{"grok", "POST", "/v1/videos/extensions"},
		{"grok", "POST", "/videos/extensions"},
		{"grok", "POST", "/v1/tts"},
		{"grok", "POST", "/tts"},
		{"grok", "POST", "/v1/stt"},
		{"grok", "POST", "/stt"},
		{"grok", "POST", "/v1/custom-voices"},
		{"grok", "POST", "/custom-voices"},
		{"grok", "GET", "/v1/realtime"},
		{"grok", "GET", "/realtime"},
		{"openai", "GET", "/v1/responses"},
		{"openai", "GET", "/responses"},
		{"openai", "GET", "/backend-api/codex/responses"},
		{"openai", "POST", "/v1/live"},
		{"openai", "POST", "/backend-api/codex/realtime/calls"},
		{"openai", "POST", "/v1/responses/compact"},
		{"openai", "POST", "/responses/compact"},
		{"openai", "POST", "/backend-api/codex/responses/compact"},
		{"openai", "POST", "/v1/alpha/search"},
		{"openai", "POST", "/alpha/search"},
		{"openai", "POST", "/backend-api/codex/alpha/search"},
		{"grok", "POST", "/v1/web_search"},
		{"grok", "POST", "/web_search"},
		{"grok", "POST", "/v1/x_search"},
		{"grok", "POST", "/x_search"},
		{"openai", "POST", "/v1/messages/count_tokens"},
		{"openai", "POST", "/messages/count_tokens"},
		{"openai", "POST", "/v1/responses/input_tokens"},
		{"gemini", "POST", "/v1beta/models/gemini:countTokens"},
	}
	for _, tc := range paths {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{ID: 1, AllowedProtocols: []protocolcore.ProtocolID{}})
			if strings.HasPrefix(tc.path, "/v1beta/") {
				// Install the Key fixture directly in the Gemini router so this branch
				// exercises action-specific protocol admission before the terminal handler, independently of authentication setup.
				router = gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{AllowedProtocols: []protocolcore.ProtocolID{}}})
				})
				router.POST("/v1beta/models/*modelAction", requireGeminiGenerateContentProtocol, func(c *gin.Context) { t.Fatal("disabled protocol reached handler") })
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
		})
	}
}

// TestProtocolAuxiliaryAndExistingJobs classifies existing job, model, usage,
// and Live resource operations. It checks those paths have no extended
// generation protocol, while custom-voice deletion retains its dedicated
// protocol identity.
func TestProtocolAuxiliaryAndExistingJobs(t *testing.T) {
	for _, path := range []string{"/v1/videos/id", "/v1/videos/id/content", "/v1/images/batches/id", "/v1/images/batches/id/download", "/v1/live/id", "/models", "/v1/usage"} {
		require.Empty(t, extendedRouteProtocol(http.MethodGet, path), path)
	}
	for _, path := range []string{"/v1/images/batches/id/cancel", "/v1/images/batches/id/outputs"} {
		require.Empty(t, extendedRouteProtocol(http.MethodPost, path), path)
		require.Empty(t, extendedRouteProtocol(http.MethodDelete, path), path)
	}
	require.Equal(t, protocolcore.ProtocolCustomVoices, extendedRouteProtocol(http.MethodDelete, "/v1/custom-voices/id"))
}

// TestProtocolRouteAliases compares protocol identities across bare, versioned,
// and Codex prefixes. It checks the expected mappings, rejects similar prefixes
// and wrong methods, and leaves Compact to its complete-path guard.
func TestProtocolRouteAliases(t *testing.T) {
	for _, tc := range []struct {
		method, path string                  // method: requested HTTP verb; path: base protocol alias
		want         protocolcore.ProtocolID // expected protocol identity
	}{
		{http.MethodPost, "/embeddings", protocolcore.ProtocolEmbeddings},
		{http.MethodPost, "/images/generations", protocolcore.ProtocolImagesGenerations},
		{http.MethodPost, "/images/edits", protocolcore.ProtocolImagesEdits},
		{http.MethodPost, "/images/batches", protocolcore.ProtocolImageBatches},
		{http.MethodPost, "/videos", protocolcore.ProtocolVideosGenerations},
		{http.MethodPost, "/videos/generations", protocolcore.ProtocolVideosGenerations},
		{http.MethodPost, "/videos/edits", protocolcore.ProtocolVideosEdits},
		{http.MethodPost, "/videos/extensions", protocolcore.ProtocolVideosExtensions},
		{http.MethodPost, "/tts", protocolcore.ProtocolTTS},
		{http.MethodPost, "/stt", protocolcore.ProtocolSTT},
		{http.MethodPost, "/custom-voices", protocolcore.ProtocolCustomVoices},
		{http.MethodGet, "/realtime", protocolcore.ProtocolVoiceRealtime},
		{http.MethodGet, "/responses", protocolcore.ProtocolResponsesWebSocket},
		{http.MethodPost, "/live", protocolcore.ProtocolLive},
		{http.MethodPost, "/realtime/calls", protocolcore.ProtocolLive},
		{http.MethodPost, "/responses/compact", protocolcore.ProtocolResponsesCompact},
		{http.MethodPost, "/alpha/search", protocolcore.ProtocolAlphaSearch},
		{http.MethodPost, "/web_search", protocolcore.ProtocolWebSearch},
		{http.MethodPost, "/x_search", protocolcore.ProtocolXSearch},
	} {
		for _, prefix := range []string{"", "/v1", "/backend-api/codex"} {
			t.Run(tc.method+prefix+tc.path, func(t *testing.T) {
				require.Equal(t, tc.want, routeProtocol(tc.method, prefix+tc.path))
			})
		}
	}
	for _, path := range []string{"/v10/responses", "/v1responses", "/backend-api/codexresponses"} {
		require.Empty(t, routeProtocol(http.MethodGet, path), path)
	}
	require.Empty(t, routeProtocol(http.MethodGet, "/v1/embeddings"))
	// Compact validates its complete path before Responses admission.
	require.Empty(t, extendedRouteProtocol(http.MethodPost, "/v1/responses/compact"))
}

// TestRemovedFeatureRoutesReturnNotFound mounts user and administrator fixtures
// with pass-through guards. It requests the retired import-default and
// data-sharing paths under four methods and checks every response remains
// NotFound.
func TestRemovedFeatureRoutesReturnNotFound(t *testing.T) {
	router := gin.New()
	// Embedded identity handlers need an outer receiver before method values can be registered.
	allHandlers := &routeTestHandlers{User: &identityhttp.UserHandler{}, Admin: &routeTestAdminHandlers{}}
	RegisterUserRoutes(
		router.Group("/api/v1"),
		allHandlers,
		identityhttp.JWTAuthMiddleware(func(c *gin.Context) { c.Next() }),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		identityhttp.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }),
		nil,
		nil,
	)
	RegisterAdminRoutes(
		router.Group("/api/v1"),
		allHandlers,
		identityhttp.AdminAuthMiddleware(func(c *gin.Context) { c.Next() }),
		servermiddleware.AuditLogMiddleware(func(c *gin.Context) { c.Next() }),
		identityhttp.StepUpAuthMiddleware(func(c *gin.Context) { c.Next() }),
		nil, func(c *gin.Context) { c.Status(http.StatusOK) })

	removedPath := "/api/v1/" + "data" + "-sharing"
	for _, path := range []string{
		"/api/v1/admin/settings/openai-oauth-import-defaults",
		removedPath,
		removedPath + "/export/download",
		"/api/v1/admin/" + "data" + "-sharing",
		"/api/v1/admin/" + "data" + "-sharing/exports/download",
	} {
		for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete} {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(method, path, nil)
			router.ServeHTTP(recorder, request)
			require.Equal(t, http.StatusNotFound, recorder.Code, "method=%s path=%s", method, path)
		}
	}
}
