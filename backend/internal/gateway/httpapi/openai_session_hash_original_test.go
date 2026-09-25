package httpapi

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/cespare/xxhash/v2"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestOpenAIGatewayService_GenerateSessionHash_Priority(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

	bodyWithKey := []byte(`{"prompt_cache_key":"ses_aaa"}`)

	// 1) session_id header wins
	c.Request.Header.Set("session_id", "sess-123")
	c.Request.Header.Set("conversation_id", "conv-456")
	h1 := GenerateOpenAISessionHash(c, bodyWithKey)
	if h1 == "" {
		t.Fatalf("expected non-empty hash")
	}

	// 2) conversation_id used when session_id absent
	c.Request.Header.Del("session_id")
	h2 := GenerateOpenAISessionHash(c, bodyWithKey)
	if h2 == "" {
		t.Fatalf("expected non-empty hash")
	}
	if h1 == h2 {
		t.Fatalf("expected different hashes for different keys")
	}

	// 3) prompt_cache_key used when both headers absent
	c.Request.Header.Del("conversation_id")
	h3 := GenerateOpenAISessionHash(c, bodyWithKey)
	if h3 == "" {
		t.Fatalf("expected non-empty hash")
	}
	if h2 == h3 {
		t.Fatalf("expected different hashes for different keys")
	}

	// 4) empty when no signals
	h4 := GenerateOpenAISessionHash(c, []byte(`{}`))
	if h4 != "" {
		t.Fatalf("expected empty hash when no signals")
	}
}

func TestOpenAIGatewayService_ClientSessionHeaderPriority(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	c.Set("api_key", &apikey.APIKey{ID: 901, Group: &routing.Group{Platform: capability.PlatformGrok}})

	headers := []struct {
		name  string
		value string
	}{
		{name: "session-id", value: "codex-session"},
		{name: "session_id", value: "generic-session"},
		{name: "conversation_id", value: "generic-conversation"},
		{name: OpenCodeSessionAffinityHeader, value: "opencode-affinity"},
		{name: OpenCodeSessionIDHeader, value: "opencode-session-id"},
		{name: OpenCodeNativeSessionHeader, value: "opencode-native-session"},
		{name: CodeBuddyConversationHeader, value: "codebuddy-conversation"},
		{name: GrokConversationIDHeader, value: "grok-conversation"},
	}
	for _, header := range headers {
		c.Request.Header.Set(header.name, header.value)
	}
	body := []byte(`{"prompt_cache_key":"body-session"}`)
	for _, header := range headers {
		require.Equal(t, header.value, ExplicitOpenAIRequestSessionID(c, body), header.name)
		require.Equal(t, fmt.Sprintf("%016x", xxhash.Sum64String(header.value)), GenerateExplicitOpenAISessionHash(c, body), header.name)
		if header.name != GrokConversationIDHeader {
			require.Equal(t, header.value, ExplicitOpenAISessionID(c, body), header.name)
		}
		c.Request.Header.Del(header.name)
	}
	require.Equal(t, "body-session", ExplicitOpenAIRequestSessionID(c, body))
}

func TestOpenAIGatewayService_CodexSessionIDKeepsReconnectHashStable(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
	c.Request.Header.Set("session-id", "codex-reconnect-session")
	warmup := []byte(`{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"generate":false,
		"tools":[{"type":"custom","name":"exec"}],
		"input":[{"role":"user","content":"warmup"}]
	}`)
	business := []byte(`{
		"type":"response.create",
		"model":"gpt-5.6-sol",
		"input":[{"role":"user","content":"install codex"}]
	}`)

	require.Equal(t, GenerateOpenAISessionHash(c, warmup), GenerateOpenAISessionHash(c, business))
	require.Equal(t, "codex-reconnect-session", ExplicitOpenAIRequestSessionID(c, business))
}

func TestOpenAIGatewayService_ClientSessionHeadersIgnorePerRequestIDs(t *testing.T) {

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	for name, value := range map[string]string{
		"X-Conversation-Request-ID": "request-rotates-every-turn",
		"X-Conversation-Message-ID": "message-rotates-every-turn",
		"X-Request-ID":              "generic-request-id",
	} {
		c.Request.Header.Set(name, value)
	}
	require.Empty(t, ExplicitOpenAIHeaderSessionID(c))
	require.Empty(t, ExplicitOpenAIRequestSessionID(c, nil))
	require.Empty(t, GenerateExplicitOpenAISessionHash(c, nil))
}

func TestOpenAIGatewayService_GenerateSessionHash_UsesXXHash64(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

	c.Request.Header.Set("session_id", "sess-fixed-value")

	got := GenerateOpenAISessionHash(c, nil)
	want := fmt.Sprintf("%016x", xxhash.Sum64String("sess-fixed-value"))
	require.Equal(t, want, got)
}

func TestOpenAIGatewayService_GenerateSessionHash_AttachesLegacyHashToContext(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)

	c.Request.Header.Set("session_id", "sess-legacy-check")

	sessionHash := GenerateOpenAISessionHash(c, nil)
	require.NotEmpty(t, sessionHash)
	require.NotNil(t, c.Request)
	require.NotNil(t, c.Request.Context())
	require.NotEmpty(t, requeststate.OpenAILegacySessionHashFromContext(c.Request.Context()))
}

func TestOpenAIGatewayService_GenerateExplicitSessionHash_SkipsContentFallback(t *testing.T) {
	body := []byte(`{"model":"gpt-image-2","prompt":"draw a cat"}`)

	t.Run("stateless image body stays unstuck", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

		require.Empty(t, GenerateExplicitOpenAISessionHash(c, body))
		require.Empty(t, requeststate.OpenAILegacySessionHashFromContext(c.Request.Context()))
	})

	t.Run("prompt_cache_key is explicit", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)

		got := GenerateExplicitOpenAISessionHash(c, []byte(`{"model":"gpt-image-2","prompt_cache_key":"image-session"}`))
		require.Equal(t, fmt.Sprintf("%016x", xxhash.Sum64String("image-session")), got)
		require.NotEmpty(t, requeststate.OpenAILegacySessionHashFromContext(c.Request.Context()))
	})

	t.Run("header overrides body", func(t *testing.T) {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
		c.Request.Header.Set("session_id", "header-session")

		got := GenerateExplicitOpenAISessionHash(c, []byte(`{"prompt_cache_key":"body-session"}`))
		require.Equal(t, fmt.Sprintf("%016x", xxhash.Sum64String("header-session")), got)
	})
}

func TestOpenAIGatewayService_GenerateSessionHashWithFallback(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/responses", nil)
	seed := "openai_ws_ingress:9:100:200"

	got := GenerateOpenAISessionHashWithFallback(c, []byte(`{}`), seed)
	want := fmt.Sprintf("%016x", xxhash.Sum64String(seed))
	require.Equal(t, want, got)
	require.NotEmpty(t, requeststate.OpenAILegacySessionHashFromContext(c.Request.Context()))

	empty := GenerateOpenAISessionHashWithFallback(c, []byte(`{}`), "   ")
	require.Equal(t, "", empty)
}

func TestOpenAIGatewayService_GenerateSessionHash_ContentFallback(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)

	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"Hello"}]}`)

	hash := GenerateOpenAISessionHash(c, body)
	require.NotEmpty(t, hash, "content-based fallback should produce a hash")

	hash2 := GenerateOpenAISessionHash(c, body)
	require.Equal(t, hash, hash2, "same content should produce same hash")

	bodyExtended := []byte(`{"model":"gpt-5.4","messages":[{"role":"system","content":"You are helpful."},{"role":"user","content":"Hello"},{"role":"assistant","content":"Hi!"},{"role":"user","content":"How are you?"}]}`)
	hashExtended := GenerateOpenAISessionHash(c, bodyExtended)
	require.Equal(t, hash, hashExtended, "hash should be stable across later turns")

	bodyDifferent := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Different question"}]}`)
	hashDifferent := GenerateOpenAISessionHash(c, bodyDifferent)
	require.NotEqual(t, hash, hashDifferent, "different content should produce different hash")
}

func TestOpenAIGatewayService_GenerateSessionHash_ExplicitSignalWinsOverContent(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	body := []byte(`{"model":"gpt-5.4","messages":[{"role":"user","content":"Hello"}]}`)

	contentHash := GenerateOpenAISessionHash(c, body)
	require.NotEmpty(t, contentHash)

	c.Request.Header.Set("session_id", "explicit-session")
	explicitHash := GenerateOpenAISessionHash(c, body)
	require.NotEmpty(t, explicitHash)
	require.NotEqual(t, contentHash, explicitHash, "explicit session_id should override content fallback")
}

func TestOpenAIGatewayService_GenerateSessionHash_EmptyBodyStillEmpty(t *testing.T) {

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/openai/v1/chat/completions", nil)
	require.Empty(t, GenerateOpenAISessionHash(c, []byte(`{}`)))
	require.Empty(t, GenerateOpenAISessionHash(c, nil))
}
