//go:build unit

package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func newSessionHeaderContext(t *testing.T, headers map[string]string) *gin.Context {
	t.Helper()

	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	c.Request = req
	return c
}

func TestExtractClientSessionID_NilContext(t *testing.T) {
	require.Equal(t, "", ExtractClientSessionID(nil))
}

func TestExtractClientSessionID_NilRequest(t *testing.T) {
	require.Equal(t, "", ExtractClientSessionID(&gin.Context{}))
}

func TestExtractClientSessionID_AbsentReturnsEmpty(t *testing.T) {
	c := newSessionHeaderContext(t, nil)
	require.Equal(t, "", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_SupportedHeaders(t *testing.T) {
	tests := []struct {
		name   string
		header string
		value  string
	}{
		{"session_id", "session_id", "sess-A"},
		{"conversation_id", "conversation_id", "conv-B"},
		{"X-Session-Affinity", OpenCodeSessionAffinityHeader, "aff-C"},
		{"X-Session-Id", OpenCodeSessionIDHeader, "sid-D"},
		{"X-OpenCode-Session", OpenCodeNativeSessionHeader, "oc-E"},
		{"X-Conversation-ID", CodeBuddyConversationHeader, "cb-F"},
		{"X-Claude-Code-Session-Id", ClaudeCodeSessionHeader, "cc-G"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newSessionHeaderContext(t, map[string]string{tc.header: tc.value})
			require.Equal(t, tc.value, ExtractClientSessionID(c))
		})
	}
}

func TestExtractClientSessionID_HeaderPrecedence(t *testing.T) {
	// session_id 的优先级高于 conversation_id 和各类 X-* 请求头。
	c := newSessionHeaderContext(t, map[string]string{
		"session_id":      "primary",
		"conversation_id": "secondary", OpenCodeSessionIDHeader: "tertiary", CodeBuddyConversationHeader: "quaternary",
	})
	require.Equal(t, "primary", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_Sanitizes(t *testing.T) {
	c := newSessionHeaderContext(t, map[string]string{OpenCodeSessionIDHeader: "  clean-123  "})
	require.Equal(t, "clean-123", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_IgnoresNonSessionHeaders(t *testing.T) {
	// prompt_cache_key、请求/消息 ID，以及非 Grok 请求携带的 Grok 对话头，
	// 均不得持久化为 session_id。
	c := newSessionHeaderContext(t, map[string]string{
		"prompt_cache_key": "cache-key-should-not-persist",
		"X-Request-Id":     "req-should-not-persist",
		"x-grok-conv-id":   "grok-conv-should-not-persist",
	})
	require.Equal(t, "", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_GrokConversationHeader(t *testing.T) {
	c := newSessionHeaderContext(t, map[string]string{GrokConversationIDHeader: "grok-native-session"})
	c.Set("api_key", &apikey.APIKey{
		ID:    42,
		Group: &routing.Group{Platform: capability.PlatformGrok},
	})

	require.Equal(t, "grok-native-session", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_EmptyForcedRouteFallsBackToGrokGroup(t *testing.T) {
	c := newSessionHeaderContext(t, map[string]string{GrokConversationIDHeader: "grok-group-session"})
	c.Set("api_key", &apikey.APIKey{
		ID:    44,
		Group: &routing.Group{Platform: capability.PlatformGrok},
	})
	c.Request = c.Request.WithContext(apikey.WithForcePlatform(context.Background(), ""))

	require.Equal(t, "grok-group-session", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_GrokConversationHeaderForForcedRoute(t *testing.T) {
	c := newSessionHeaderContext(t, map[string]string{GrokConversationIDHeader: "grok-composite-session"})
	c.Set("api_key", &apikey.APIKey{
		ID:    43,
		Group: &routing.Group{Platform: capability.PlatformAnthropic},
	})
	c.Request = c.Request.WithContext(apikey.WithForcePlatform(context.Background(), capability.PlatformGrok))

	require.Equal(t, "grok-composite-session", ExtractClientSessionID(c))
}

func TestExtractClientSessionID_InjectionHeaderDropped(t *testing.T) {
	// 支持的请求头一旦携带 CRLF 注入内容，必须整值拒绝，不能清理后持久化。
	c := newSessionHeaderContext(t, map[string]string{"session_id": "abc"})
	c.Request.Header.Set("session_id", "abc\r\nX-Injected: 1")
	require.Equal(t, "", ExtractClientSessionID(c))
}
