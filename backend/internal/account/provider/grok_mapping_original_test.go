package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

// TestGrokAccountModelMappingRemainsExplicit 验证平台内置别名不会重新混入账号配置。
func TestGrokAccountModelMappingRemainsExplicit(t *testing.T) {
	tests := []struct {
		name        string
		credentials map[string]any
		want        map[string]string
	}{
		{name: "missing credentials"},
		{name: "missing mapping", credentials: map[string]any{}},
		{name: "empty mapping", credentials: map[string]any{"model_mapping": map[string]any{}}},
		{name: "invalid mapping", credentials: map[string]any{"model_mapping": map[string]any{"grok": 45}}},
		{
			name: "explicit mapping is preserved",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"grok":         "grok-4.3",
					"client-alias": "grok-latest",
				},
			},
			want: map[string]string{
				"grok":         "grok-4.3",
				"client-alias": "grok-latest",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			account := &accountcore.Record{Platform: capability.PlatformGrok, Credentials: test.credentials}
			require.Equal(t, test.want, accountcore.ResolveModelMapping(account, ModelDefaults(

			// TestGrokWhitelistRunsBeforeBuiltinNormalization 验证白名单只检查账号映射后的模型。
			)))
		})
	}
}

func TestGrokWhitelistRunsBeforeBuiltinNormalization(t *testing.T) {
	unrestricted := &accountcore.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	require.True(t, unrestricted.IsModelSupported("custom-grok-model", ModelDefaults(), ModelRules(unrestricted)))
	require.True(t, unrestricted.IsModelSupported("grok", ModelDefaults(), ModelRules(unrestricted)))

	strict := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, strict.IsModelSupported("grok-4.5", ModelDefaults(), ModelRules(strict)))
	require.False(t, strict.IsModelSupported("grok", ModelDefaults(), ModelRules(strict)))

	mapped := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"grok": "grok-4.5"},
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, mapped.IsModelSupported("grok", ModelDefaults(), ModelRules(mapped)))

	legacy := &accountcore.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
		},
	}
	require.True(t, legacy.IsModelSupported("grok-4.5", ModelDefaults(), ModelRules(

		// TestGrokFinalUpstreamModelNormalization 验证 OAuth 和 API Key 共用 Grok 最终标准化。
		legacy)))
	require.False(t, legacy.IsModelSupported("grok", ModelDefaults(), ModelRules(legacy)))
}
