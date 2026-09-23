package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIMessagesMetadataSession_DoesNotDerivePromptCacheKey(t *testing.T) {
	body := []byte(`{"model":"claude-sonnet-4-5","metadata":{"user_id":"claude-code-session"},"messages":[{"role":"user","content":"hello"}]}`)

	sessionHash, promptCacheKey := metadataSessionForTest(nil, "", "", "claude-sonnet-4-5", body)

	require.NotEmpty(t, sessionHash)
	require.Empty(t, promptCacheKey)
}

func TestResolveOpenAIMessagesMetadataSession_PreservesExplicitPromptCacheKey(t *testing.T) {
	body := []byte(`{"metadata":{"user_id":"claude-code-session"}}`)

	sessionHash, promptCacheKey := metadataSessionForTest(nil, "", "explicit-cache", "claude-sonnet-4-5", body)

	require.NotEmpty(t, sessionHash)
	require.Equal(t, "explicit-cache", promptCacheKey)
}

func TestResolveOpenAIMessagesMetadataSession_ClaudeCodeHeaderOverridesContentFallback(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "claude-session-001")

	body1 := []byte(`{"model":"gpt-5.6-sol","system":"parent","messages":[{"role":"user","content":"parent task"}]}`)
	body2 := []byte(`{"model":"gpt-5.6-sol","system":"subagent","messages":[{"role":"user","content":"child task"}]}`)

	contentHash1 := GenerateOpenAISessionHash(c, body1)
	contentHash2 := GenerateOpenAISessionHash(c, body2)
	require.NotEqual(t, contentHash1, contentHash2, "different bodies should prove the content fallback differs")

	hash1, cacheKey1 := metadataSessionForTest(c, contentHash1, "", "gpt-5.6-sol", body1)
	hash2, cacheKey2 := metadataSessionForTest(c, contentHash2, "", "gpt-5.6-sol", body2)
	want, _ := scheduler.DeriveSessionHashes("claude-session-001")
	require.Equal(t, want, hash1)
	require.Equal(t, hash1, hash2, "the same Claude Code session must keep one sticky account across changed turn bodies")
	require.Empty(t, cacheKey1, "routing-only fix must not create an upstream prompt cache key")
	require.Empty(t, cacheKey2, "routing-only fix must not create an upstream prompt cache key")
}

func TestResolveOpenAIMessagesMetadataSession_OpenAISignalWinsOverClaudeHeader(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "claude-session-001")

	hash, cacheKey := metadataSessionForTest(c, "content-hash", "explicit-openai-session", "gpt-5.6-sol", []byte(`{"metadata":{"user_id":"opaque"}}`))
	require.Equal(t, "content-hash", hash, "existing OpenAI session resolution must remain authoritative")
	require.Equal(t, "explicit-openai-session", cacheKey)
}

func TestResolveOpenAIMessagesMetadataSession_BlankClaudeHeaderKeepsContentFallback(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	c.Request.Header.Set("X-Claude-Code-Session-Id", "   ")

	hash, cacheKey := metadataSessionForTest(c, "content-hash", "", "gpt-5.6-sol", []byte(`{"metadata":{"user_id":"opaque"}}`))
	require.Equal(t, "content-hash", hash)
	require.Empty(t, cacheKey)
}

// 请求 Header 与内容种子的组合仍验证实际原生函数。
func metadataSessionForTest(c *gin.Context, hash, key, model string, body []byte) (string, string) {
	return session.MessagesMetadataSession(ClaudeCodeSessionIDFromHeader(c), hash, key, model, body)
}
