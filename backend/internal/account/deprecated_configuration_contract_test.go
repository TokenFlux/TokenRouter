//go:build unit

package account_test

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/stretchr/testify/require"
)

// 所有旧探测值只用于清理，不得成为保存后的配置或清空其它配置的指令。
func TestLegacyOpenAIConfigurationInputBoundary(t *testing.T) {
	for _, value := range []any{nil, false, true, "unsupported", []any{1}, map[string]any{"invalid": true}} {
		extra := map[string]any{}
		for _, key := range accountcore.DeprecatedOpenAIAccountExtraKeys {
			extra[key] = value
		}
		normalized, replace := accountcore.NormalizeDeprecatedAccountExtraUpdate(extra)
		require.False(t, replace)
		require.Nil(t, normalized)
		extra["keep"] = map[string]any{"enabled": false}
		extra["openai_compact_mode"] = " AUTO "
		extra[accountcore.OpenAINativeCompactionV2ModeExtraKey] = "force_off"
		normalized, replace = accountcore.NormalizeDeprecatedAccountExtraUpdate(extra)
		require.True(t, replace)
		require.Equal(t, map[string]any{"keep": map[string]any{"enabled": false}, "openai_compact_mode": "force_on", accountcore.OpenAINativeCompactionV2ModeExtraKey: "force_off"}, normalized)
		require.Equal(t, " AUTO ", extra["openai_compact_mode"], "边界处理不得修改调用方对象")
	}
	normalized, replace := accountcore.NormalizeDeprecatedAccountExtraUpdate(map[string]any{})
	require.True(t, replace, "显式空对象仍表示清空")
	require.Empty(t, normalized)
}
