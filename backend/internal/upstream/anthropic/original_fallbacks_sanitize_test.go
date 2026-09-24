//go:build unit

package anthropic_test

import (
	"testing"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestSanitizeAnthropicBodyForBetaTokens_NoFallbackFieldsNoChange(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20")
	require.False(t, changed)
	require.Equal(t, string(body), string(out))
}

func TestSanitizeAnthropicBodyForBetaTokens_FallbacksKeptWhenBetaPresent(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","fallbacks":"default","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body,
		"claude-code-20250219,oauth-2025-04-20,server-side-fallback-2026-07-01")
	require.False(t, changed, "客户端 header 已带 server-side-fallback beta → 字段保留（不过度删除）")
	require.True(t, gjson.GetBytes(out, "fallbacks").Exists())
	require.Equal(t, "default", gjson.GetBytes(out, "fallbacks").String())
}

func TestSanitizeAnthropicBodyForBetaTokens_FallbacksStrippedWhenBetaMissing(t *testing.T) {
	// 客户端透传的两种形态：字符串 "default" 与模型数组
	for name, fallbacks := range map[string]string{
		"string_default": `"fallbacks":"default"`,
		"model_array":    `"fallbacks":["claude-opus-4-6","claude-sonnet-4-6"]`,
	} {
		t.Run(name, func(t *testing.T) {
			body := []byte(`{"model":"claude-haiku-4-5",` + fallbacks + `,"messages":[]}`)
			// 模拟 OAuth mimic / 默认 API-key beta：只有 oauth/interleaved，无 fallback beta
			out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body,
				"oauth-2025-04-20,interleaved-thinking-2025-05-14")
			require.True(t, changed)
			require.False(t, gjson.GetBytes(out, "fallbacks").Exists(),
				"header 不含 server-side-fallback beta 时必须 strip fallbacks，否则上游 400")
			require.True(t, gjson.GetBytes(out, "messages").Exists(), "strip 不得误伤其他字段")
		})
	}
}

func TestSanitizeAnthropicBodyForBetaTokens_FallbacksStrippedWhenHeaderEmpty(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","fallbacks":"default","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "")
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "fallbacks").Exists())
}

func TestSanitizeAnthropicBodyForBetaTokens_FallbackCreditTokenStrippedWhenCreditBetaMissing(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","fallback_credit_token":"tok_123","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20,interleaved-thinking-2025-05-14")
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "fallback_credit_token").Exists(),
		"缺 credit/fallback beta 时必须 strip fallback_credit_token")
}

func TestSanitizeAnthropicBodyForBetaTokens_FallbackCreditTokenKeptWithAnyAcceptedBeta(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","fallback_credit_token":"tok_123","messages":[]}`)
	// 三个 beta token 任意一个在 header 中都必须保留字段
	for _, beta := range []string{
		claude.BetaServerSideFallback,
		claude.BetaFallbackCredit,
		claude.BetaFallbackCreditLegacy,
	} {
		out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "oauth-2025-04-20,"+beta)
		require.Falsef(t, changed, "header 含 %s 时 fallback_credit_token 必须保留", beta)
		require.Truef(t, gjson.GetBytes(out, "fallback_credit_token").Exists(),
			"header 含 %s 时 fallback_credit_token 必须保留", beta)
	}
}

// ★ 组合场景：只带 context-management beta → 剥 fallbacks，保留 context_management
// （守住"早退导致 fallbacks 漏洗"与"过度删除 context_management"两个方向的回归）

func TestSanitizeAnthropicBodyForBetaTokens_StripsFallbacksKeepsContextManagement(t *testing.T) {
	body := []byte(`{"model":"claude-haiku-4-5","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"fallbacks":"default","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body, "context-management-2025-06-27")
	require.True(t, changed)
	require.False(t, gjson.GetBytes(out, "fallbacks").Exists(),
		"header 只有 context-management beta → fallbacks 必须 strip")
	require.True(t, gjson.GetBytes(out, "context_management").Exists(),
		"context-management beta 在 header 中 → context_management 不得被误删")
}

func TestSanitizeAnthropicBodyForBetaTokens_KeepsBothWhenBothBetasPresent(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-7","context_management":{"edits":[{"type":"clear_thinking_20251015"}]},"fallbacks":"default","fallback_credit_token":"tok_123","messages":[]}`)
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens(body,
		"context-management-2025-06-27,server-side-fallback-2026-07-01")
	require.False(t, changed, "两个 beta 都在 header 中 → 所有字段保留")
	require.True(t, gjson.GetBytes(out, "context_management").Exists())
	require.True(t, gjson.GetBytes(out, "fallbacks").Exists())
	require.True(t, gjson.GetBytes(out, "fallback_credit_token").Exists())
}

func TestSanitizeAnthropicBodyForBetaTokens_EmptyBodyUnchanged(t *testing.T) {
	out, changed := claude.SanitizeAnthropicBodyForBetaTokens([]byte{}, "server-side-fallback-2026-07-01")
	require.False(t, changed)
	require.Empty(t, out)

	out, changed = claude.SanitizeAnthropicBodyForBetaTokens(nil, "server-side-fallback-2026-07-01")
	require.False(t, changed)
	require.Empty(t, out)
}

// ============================================================================
// buildUpstreamRequest 端到端
// 挡住未来某人忘调 sanitize / 将 sanitize 挪到 CCH 之后 等 regression。
// ============================================================================

// OAuth mimic：FullClaudeCodeMimicryBetas 不含 fallback beta → body.fallbacks
// 必须被 strip，且 outgoing anthropic-beta 不得注入 server-side-fallback beta
// （剥字段，不注入 beta）。
