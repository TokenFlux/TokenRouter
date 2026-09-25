package app

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/identity"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	protocolcore "github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newGatewayRoutesTestRouter(platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithOptions(&config.Config{}, platform...)
}

func newGatewayRoutesTestRouterWithConfig(cfg *config.Config, platform ...string) *gin.Engine {
	return newGatewayRoutesTestRouterWithOptions(cfg, platform...)
}

// newGatewayRoutesTestRouterWithOptions 以原生 HTTP 入口验证配置和平台路由。
func newGatewayRoutesTestRouterWithOptions(cfg *config.Config, platform ...string) *gin.Engine {
	groupPlatform := capability.PlatformOpenAI
	if len(platform) > 0 && platform[0] != "" {
		groupPlatform = platform[0]
	}
	groupID := int64(1)
	// 普通路由测试模拟已开启全部受支持协议；空集合由专门的门禁测试覆盖。
	protocols := []protocolcore.ProtocolID{
		protocolcore.ProtocolAnthropicMessages,
		protocolcore.ProtocolOpenAIResponses,
		protocolcore.ProtocolOpenAIChatCompletions,
	}
	if groupPlatform == capability.PlatformGemini || groupPlatform == capability.PlatformAntigravity {
		protocols = append(protocols, protocolcore.ProtocolGeminiGenerateContent)
	}
	return newGatewayRoutesTestRouterWithGroup(cfg, &routing.Group{
		ID:               groupID,
		Platform:         groupPlatform,
		AllowedProtocols: protocols,
	})
}

// newGatewayRoutesTestRouterWithGroup 允许测试显式控制 nil 与空协议集合。
func newGatewayRoutesTestRouterWithGroup(cfg *config.Config, group *routing.Group, models ...*gatewayhttp.ModelsHandler) *gin.Engine {

	router := gin.New()

	var modelsHTTP *gatewayhttp.ModelsHandler
	if len(models) > 0 {
		modelsHTTP = models[0]
	}
	RegisterGatewayRoutes(
		router,
		&routeTestHandlers{
			TextEnabled:   true,
			ModelsHTTP:    modelsHTTP,
			OpenAIEnabled: true,
		},
		keyhttp.APIKeyAuthMiddleware(func(c *gin.Context) {
			groupID := group.ID
			c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
				User:    &identity.User{ID: 1, Status: billing.StatusActive, Concurrency: 1},
				GroupID: &groupID,
				Group:   group,
			})
			c.Set(string(authctx.ContextKeyUser), authctx.AuthSubject{UserID: 1, Concurrency: 1})
			c.Next()
		}),
		nil,
		nil,
		nil,
		nil,
		cfg,
	)

	return router
}

type protocolGateTrackingReader struct {
	read bool
}

func (r *protocolGateTrackingReader) Read(_ []byte) (int, error) {
	r.read = true
	return 0, io.EOF
}

func TestGatewayRoutesClientProtocolGateRejectsAliasesBeforeReadingBody(t *testing.T) {
	tests := []struct {
		name      string
		platform  string
		protocols []protocolcore.ProtocolID
		paths     []string
		code      string
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
				Platform:         tt.platform,
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

// 不支持 count_tokens 的平台固定返回 404，不受 Messages 协议开关影响。
func TestGatewayRoutesUnsupportedCountTokensBypassesProtocolGate(t *testing.T) {
	tests := []struct {
		name     string
		platform string
		path     string
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
				Platform:         tt.platform,
				AllowedProtocols: []protocolcore.ProtocolID{},
			})
			reader := &protocolGateTrackingReader{}
			req := httptest.NewRequest(http.MethodPost, tt.path, reader)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			router.ServeHTTP(w, req)

			require.Equal(t, http.StatusNotFound, w.Code)
			require.Contains(t, w.Body.String(), "not_found_error")
			require.NotContains(t, w.Body.String(), "protocol_not_allowed")
			require.False(t, reader.read, "unsupported count_tokens must not read request body")
		})
	}
}

func TestGatewayRoutesResponsesSubpathGuardRunsBeforeProtocolGate(t *testing.T) {
	groupID := int64(1)
	router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, &routing.Group{
		ID:       groupID,
		Platform: capability.PlatformQoder,
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

func TestRequireGroupClientProtocolUsesNativeErrorEnvelopes(t *testing.T) {
	tests := []struct {
		name     string
		protocol protocolcore.ProtocolID
		format   groupClientProtocolErrorFormat
		contains []string
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

func TestRequireGeminiGenerateContentProtocolOnlyGatesTextActions(t *testing.T) {
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Set(string(keyhttp.ContextKeyAPIKey), &apikey.APIKey{
			Group: &routing.Group{Platform: capability.PlatformQoder, AllowedProtocols: []protocolcore.ProtocolID{}},
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

func TestGatewayRoutesQoderPathsAreRegistered(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformQoder)

	for _, tc := range []struct {
		path string
		body string
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
		require.Equal(t, http.StatusNotFound, w.Code, "path=%s should reject unsupported Qoder Responses subpath", path)
		require.Contains(t, w.Body.String(), "Qoder Responses subpaths are not supported")
	}
}

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
		require.Equal(t, http.StatusNotFound, w.Code, "path=%s should reject Qoder Responses websocket", path)
		require.Contains(t, w.Body.String(), "Qoder Responses WebSocket is not supported")
	}
}

func TestGatewayRoutesNonNativeResponsesWebSocketIsRejected(t *testing.T) {
	router := newGatewayRoutesTestRouterWithOptions(&config.Config{}, capability.PlatformAnthropic)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/responses", nil))

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "not supported for this upstream platform")
}

// Alpha Search 的三种公开路径都必须注册到 OpenAI 专用 handler。
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

// 非 OpenAI 分组不能通过通用 /v1 路由调用 Codex Alpha Search。
func TestGatewayRoutesAlphaSearchRejectsNonOpenAIGroup(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformGrok)
	req := httptest.NewRequest(http.MethodPost, "/v1/alpha/search", strings.NewReader(`{"model":"gpt-5.6-sol"}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusNotFound, w.Code)
	require.Contains(t, w.Body.String(), "only available for OpenAI groups")
}

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

// TestGatewayRoutesAsyncImagesPathsAreRemoved 锁定自研异步图片接口不再暴露。
func TestGatewayRoutesAsyncImagesPathsAreRemoved(t *testing.T) {
	router := newGatewayRoutesTestRouter()
	registered := make(map[string]bool)
	for _, route := range router.Routes() {
		registered[route.Method+" "+route.Path] = true
	}

	removed := []struct {
		method      string
		routePath   string
		requestPath string
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

	// Gemini 批量图片作业是独立功能，移除自研异步接口后仍须保留。
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

// TestGatewayRoutesBillingIntrospectionIsRemoved 锁定旧版公开账单自省接口不再注册。
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

func TestGatewayRoutesNonGrokVideosAreRejectedAtPlatformGate(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformOpenAI)

	for _, tc := range []struct {
		method string
		path   string
		body   string
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
		require.Equal(t, http.StatusNotFound, w.Code, "method=%s path=%s", tc.method, tc.path)
		require.Contains(t, w.Body.String(), "Videos API is not supported for this platform")
	}
}

func TestGatewayRoutesGrokAllowsCLICompatibilityEntrypoints(t *testing.T) {
	router := newGatewayRoutesTestRouter(capability.PlatformGrok)

	for _, tc := range []struct {
		method string
		path   string
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

	countTokensRouter := newGatewayRoutesTestRouterWithConfig(&config.Config{
		Gateway: config.GatewayConfig{MaxBodySize: 1024 * 1024},
	}, capability.PlatformGrok)
	for _, path := range []string{"/v1/messages/count_tokens", "/messages/count_tokens"} {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"model":"grok","messages":[{"role":"user","content":"hi"}]}`))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()

		countTokensRouter.ServeHTTP(w, req)
		require.Equal(t, http.StatusOK, w.Code, "path=%s", path)
		var response struct {
			InputTokens int `json:"input_tokens"`
		}
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response), "path=%s", path)
		require.Positive(t, response.InputTokens, "path=%s", path)
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

// TestGatewayRoutesResponsesSubpathRejectsNonConformingSubpaths 端到端锁定不变式：
// /responses/*subpath 的子路径会被转发到上游同名端点之后，因此不合规的子路径必须
// 在入口就被拒绝，不得进入调度与转发流程。
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
