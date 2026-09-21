package app

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
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
			router := newGatewayRoutesTestRouterWithGroup(&config.Config{}, nil, &routing.Group{ID: 1, Platform: tc.platform, AllowedProtocols: []protocol.ProtocolID{}})
			if strings.HasPrefix(tc.path, "/v1beta/") {
				// Gemini 鉴权使用单独中间件；此处在鉴权后注入分组，独立验证动作分派。
				router = gin.New()
				router.Use(func(c *gin.Context) {
					c.Set(string(middleware.ContextKeyAPIKey), &apikey.APIKey{Group: &routing.Group{Platform: "gemini", AllowedProtocols: []protocol.ProtocolID{}}})
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
	require.Equal(t, protocol.ProtocolCustomVoices, extendedRouteProtocol(http.MethodDelete, "/v1/custom-voices/id"))
}

// 别名复用相同协议；相似前缀和错误方法不能命中合法入口。
func TestProtocolRouteAliases(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         protocol.ProtocolID
	}{
		{http.MethodPost, "/embeddings", protocol.ProtocolEmbeddings},
		{http.MethodPost, "/images/generations", protocol.ProtocolImagesGenerations},
		{http.MethodPost, "/images/edits", protocol.ProtocolImagesEdits},
		{http.MethodPost, "/images/batches", protocol.ProtocolImageBatches},
		{http.MethodPost, "/videos", protocol.ProtocolVideosGenerations},
		{http.MethodPost, "/videos/generations", protocol.ProtocolVideosGenerations},
		{http.MethodPost, "/videos/edits", protocol.ProtocolVideosEdits},
		{http.MethodPost, "/videos/extensions", protocol.ProtocolVideosExtensions},
		{http.MethodPost, "/tts", protocol.ProtocolTTS},
		{http.MethodPost, "/stt", protocol.ProtocolSTT},
		{http.MethodPost, "/custom-voices", protocol.ProtocolCustomVoices},
		{http.MethodGet, "/realtime", protocol.ProtocolVoiceRealtime},
		{http.MethodGet, "/responses", protocol.ProtocolResponsesWebSocket},
		{http.MethodPost, "/live", protocol.ProtocolLive},
		{http.MethodPost, "/realtime/calls", protocol.ProtocolLive},
		{http.MethodPost, "/responses/compact", protocol.ProtocolResponsesCompact},
		{http.MethodPost, "/alpha/search", protocol.ProtocolAlphaSearch},
		{http.MethodPost, "/web_search", protocol.ProtocolWebSearch},
		{http.MethodPost, "/x_search", protocol.ProtocolXSearch},
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
	// Compact 只由完成路径校验后的 Responses 门禁负责，避免重复检查。
	require.Empty(t, extendedRouteProtocol(http.MethodPost, "/v1/responses/compact"))
}
