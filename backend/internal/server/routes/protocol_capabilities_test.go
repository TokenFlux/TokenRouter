package routes

import (
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/domain"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

// 逐入口执行真实路由；禁用时不进入缺少上游依赖的 handler，所有别名共用同一准入。
func TestProtocolAllPublicRoutesDeniedBeforeUpstream(t *testing.T) {
	paths := []struct{ platform, method, path string }{
		{"openai", "POST", "/v1/messages"}, {"openai", "POST", "/v1/responses"}, {"openai", "POST", "/responses"}, {"openai", "POST", "/backend-api/codex/responses"},
		{"openai", "POST", "/v1/chat/completions"}, {"openai", "POST", "/chat/completions"}, {"gemini", "POST", "/v1beta/models/gemini:generateContent"}, {"gemini", "POST", "/v1beta/models/gemini:streamGenerateContent"},
		{"openai", "POST", "/v1/embeddings"}, {"openai", "POST", "/embeddings"}, {"openai", "POST", "/v1/images/generations"}, {"openai", "POST", "/images/generations"}, {"openai", "POST", "/v1/images/edits"}, {"openai", "POST", "/images/edits"},
		{"gemini", "POST", "/v1/images/batches"}, {"grok", "POST", "/v1/videos"}, {"grok", "POST", "/videos"}, {"grok", "POST", "/v1/videos/generations"}, {"grok", "POST", "/videos/generations"}, {"grok", "POST", "/v1/videos/edits"}, {"grok", "POST", "/videos/edits"}, {"grok", "POST", "/v1/videos/extensions"}, {"grok", "POST", "/videos/extensions"},
		{"grok", "POST", "/v1/tts"}, {"grok", "POST", "/tts"}, {"grok", "POST", "/v1/stt"}, {"grok", "POST", "/stt"}, {"grok", "POST", "/v1/custom-voices"}, {"grok", "POST", "/custom-voices"}, {"grok", "GET", "/v1/realtime"}, {"grok", "GET", "/realtime"},
		{"openai", "GET", "/v1/responses"}, {"openai", "GET", "/responses"}, {"openai", "GET", "/backend-api/codex/responses"}, {"openai", "POST", "/v1/live"}, {"openai", "POST", "/backend-api/codex/realtime/calls"},
		{"openai", "POST", "/v1/responses/compact"}, {"openai", "POST", "/responses/compact"}, {"openai", "POST", "/backend-api/codex/responses/compact"}, {"openai", "POST", "/v1/alpha/search"}, {"openai", "POST", "/alpha/search"}, {"openai", "POST", "/backend-api/codex/alpha/search"},
		{"grok", "POST", "/v1/web_search"}, {"grok", "POST", "/web_search"}, {"grok", "POST", "/v1/x_search"}, {"grok", "POST", "/x_search"},
		{"openai", "POST", "/v1/messages/count_tokens"}, {"openai", "POST", "/messages/count_tokens"}, {"openai", "POST", "/v1/responses/input_tokens"}, {"gemini", "POST", "/v1beta/models/gemini:countTokens"},
	}
	for _, tc := range paths {
		t.Run(tc.method+tc.path, func(t *testing.T) {
			router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, nil, &service.Group{ID: 1, Platform: tc.platform, AllowedProtocols: []service.GroupClientProtocol{}})
			if strings.HasPrefix(tc.path, "/v1beta/") {
				// Gemini 鉴权使用单独中间件；此处在鉴权后注入分组，独立验证动作分派。
				router = gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &service.APIKey{Group: &service.Group{Platform: "gemini", AllowedProtocols: []service.GroupClientProtocol{}}})
				})
				router.POST("/v1beta/models/*modelAction", requireGeminiGenerateContentProtocol, func(c *gin.Context) { t.Fatal("disabled protocol reached handler") })
			}
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(tc.method, tc.path, strings.NewReader(`{}`)))
			require.Equal(t, http.StatusForbidden, recorder.Code, recorder.Body.String())
		})
	}
}

func TestProtocolAuxiliaryAndExistingJobs(t *testing.T) {
	for _, path := range []string{"/v1/videos/id", "/v1/videos/id/content", "/v1/images/batches/id", "/v1/images/batches/id/download", "/v1/live/id", "/models", "/v1/usage"} {
		require.Empty(t, extendedRouteProtocol(http.MethodGet, path), path)
	}
	for _, path := range []string{"/v1/images/batches/id/cancel", "/v1/images/batches/id/outputs"} {
		require.Empty(t, extendedRouteProtocol(http.MethodPost, path), path)
		require.Empty(t, extendedRouteProtocol(http.MethodDelete, path), path)
	}
	require.Equal(t, domain.ProtocolCustomVoices, extendedRouteProtocol(http.MethodDelete, "/v1/custom-voices/id"))
}
