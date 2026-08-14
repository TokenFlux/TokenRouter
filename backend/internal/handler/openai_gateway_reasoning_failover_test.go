package handler

import (
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// kiroReasoningCanonicalBody 模拟带有 Kiro 专属加密 reasoning 项的 Responses 请求。
const kiroReasoningCanonicalBody = `{"model":"gpt-5.1","stream":false,"input":[` +
	`{"type":"message","role":"user","content":"hello"},` +
	`{"type":"reasoning","id":"rs_kiro_abc123","encrypted_content":"ENC_BLOB","summary":[{"type":"summary_text","text":"thinking"}]},` +
	`{"type":"message","role":"assistant","content":"hi"}` +
	`]}`

func newOpenAIPassthroughAccount(id int64, passthrough bool) *service.Account {
	return &service.Account{
		ID:       id,
		Platform: service.PlatformOpenAI,
		Type:     service.AccountTypeAPIKey,
		Extra:    map[string]any{"openai_passthrough": passthrough},
	}
}

func reasoningItemCount(t *testing.T, body []byte) int {
	t.Helper()
	count := 0
	gjson.GetBytes(body, "input").ForEach(func(_, item gjson.Result) bool {
		if item.Get("type").String() == "reasoning" {
			count++
		}
		return true
	})
	return count
}

// TestDeriveOpenAIForwardAttemptBody_CrossModeStripsKiroReasoning 验证透传账号切换到
// 非透传账号时会移除整个加密 reasoning 项，同时保持 canonical 请求体不变。
func TestDeriveOpenAIForwardAttemptBody_CrossModeStripsKiroReasoning(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)
	kiro := newOpenAIPassthroughAccount(1, true)
	bedrock := newOpenAIPassthroughAccount(2, false)
	state := &openAIPassthroughFailoverState{}

	firstBody := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
	require.Equal(t, 1, reasoningItemCount(t, firstBody), "首次透传尝试必须保留 reasoning")
	require.Equal(t, "ENC_BLOB", gjson.GetBytes(firstBody, "input.1.encrypted_content").String())
	require.Equal(t, "rs_kiro_abc123", gjson.GetBytes(firstBody, "input.1.id").String())
	require.JSONEq(t, kiroReasoningCanonicalBody, string(firstBody))

	secondBody := h.deriveOpenAIForwardAttemptBody(nil, canonical, bedrock, state)
	require.Equal(t, 0, reasoningItemCount(t, secondBody), "跨模式尝试必须删除整个 reasoning 项")
	require.False(t, gjson.GetBytes(secondBody, "input.#(encrypted_content)").Exists())
	require.NotContains(t, string(secondBody), "rs_kiro_abc123")
	require.NotContains(t, string(secondBody), "ENC_BLOB")
	require.Equal(t, 2, int(gjson.GetBytes(secondBody, "input.#").Int()))
	require.Equal(t, "hello", gjson.GetBytes(secondBody, "input.0.content").String())
	require.Equal(t, "hi", gjson.GetBytes(secondBody, "input.1.content").String())
	require.JSONEq(t, kiroReasoningCanonicalBody, string(canonical))
}

// TestDeriveOpenAIForwardAttemptBody_SameModePreservesReasoning 验证同模式重试和切换到
// 透传账号时都继续使用完整的 canonical reasoning。
func TestDeriveOpenAIForwardAttemptBody_SameModePreservesReasoning(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)

	t.Run("non_passthrough_to_non_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(10, false), state)
		second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(11, false), state)
		require.Equal(t, 1, reasoningItemCount(t, first))
		require.Equal(t, 1, reasoningItemCount(t, second))
		require.JSONEq(t, kiroReasoningCanonicalBody, string(second))
	})

	t.Run("passthrough_to_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(20, true), state)
		second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(21, true), state)
		require.Equal(t, 1, reasoningItemCount(t, first))
		require.Equal(t, 1, reasoningItemCount(t, second))
	})

	t.Run("non_passthrough_to_passthrough", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		_ = h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(30, false), state)
		second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(31, true), state)
		require.Equal(t, 1, reasoningItemCount(t, second))
	})

	t.Run("same_account_pool_retry", func(t *testing.T) {
		state := &openAIPassthroughFailoverState{}
		kiro := newOpenAIPassthroughAccount(40, true)
		_ = h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
		retry := h.deriveOpenAIForwardAttemptBody(nil, canonical, kiro, state)
		require.Equal(t, 1, reasoningItemCount(t, retry))
	})
}

// TestDeriveOpenAIForwardAttemptBody_SanitizationSticksAcrossBedrockRetries 验证跨模式后
// 所有后续非透传账号重试都会继续使用清理后的请求体。
func TestDeriveOpenAIForwardAttemptBody_SanitizationSticksAcrossBedrockRetries(t *testing.T) {
	h := &OpenAIGatewayHandler{}
	canonical := []byte(kiroReasoningCanonicalBody)
	state := &openAIPassthroughFailoverState{}

	first := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(50, true), state)
	second := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(51, false), state)
	retry := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(51, false), state)
	nextAccount := h.deriveOpenAIForwardAttemptBody(nil, canonical, newOpenAIPassthroughAccount(52, false), state)

	require.Equal(t, 1, reasoningItemCount(t, first))
	require.Equal(t, 0, reasoningItemCount(t, second))
	require.Equal(t, 0, reasoningItemCount(t, retry))
	require.Equal(t, 0, reasoningItemCount(t, nextAccount))
	require.JSONEq(t, kiroReasoningCanonicalBody, string(canonical))
}
