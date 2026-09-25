//go:build unit

package bedrock_test

import (
	"testing"

	claude "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestPrepareBedrockRequestBodyWithTokens_FallbacksRequireSupportedBeta(t *testing.T) {
	modelID := "us.anthropic.claude-opus-4-6-v1"

	t.Run("strips fallbacks when final tokens omit server-side-fallback beta", func(t *testing.T) {
		input := `{
			"messages":[{"role":"user","content":"hi"}],
			"max_tokens":100,
			"fallbacks":"default",
			"fallback_credit_token":"tok_123"
		}`
		betaTokens := []string{"context-1m-2025-08-07"}

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, betaTokens, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
		assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
		assert.Equal(t, "hi", gjson.GetBytes(result, "messages.0.content").String())
		assert.Equal(t, int64(100), gjson.GetBytes(result, "max_tokens").Int())
	})

	t.Run("strips fallbacks even when client passes server-side-fallback token (not whitelisted)", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100,"fallbacks":"default"}`

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens(
			[]byte(input), modelID, []string{claude.BetaServerSideFallback}, false,
		)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists(),
			"Bedrock 白名单不含 fallback beta → 条件 strip 总会剥除（预期）")
		for _, name := range bedrockAnthropicBetaNames(result) {
			assert.NotEqual(t, claude.BetaServerSideFallback, name,
				"fallback beta 不得进入 Bedrock anthropic_beta 白名单输出")
		}
	})

	t.Run("leaves body without fallback fields otherwise intact", func(t *testing.T) {
		input := `{"messages":[{"role":"user","content":"hi"}],"max_tokens":100}`

		result, err := bedrock.PrepareBedrockRequestBodyWithTokens([]byte(input), modelID, nil, false)
		require.NoError(t, err)

		assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
		assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
		assert.False(t, gjson.GetBytes(result, "context_management").Exists())
		assert.Equal(t, "hi", gjson.GetBytes(result, "messages.0.content").String())
	})
}

// sanitizeBedrockCCFields：fallbacks / fallback_credit_token 是 Anthropic 直连
// beta API 专有字段，Bedrock 无对应 beta → 无条件剥除。

func TestSanitizeBedrockCCFields_StripsFallbacksUnconditionally(t *testing.T) {
	body := []byte(`{"model":"claude-opus-4-6","context_management":{"edits":[]},"fallbacks":"default","fallback_credit_token":"tok_123","messages":[]}`)
	result := bedrock.SanitizeBedrockCCFields(body)

	assert.False(t, gjson.GetBytes(result, "fallbacks").Exists())
	assert.False(t, gjson.GetBytes(result, "fallback_credit_token").Exists())
	assert.False(t, gjson.GetBytes(result, "context_management").Exists())
	assert.True(t, gjson.GetBytes(result, "messages").Exists())
}
