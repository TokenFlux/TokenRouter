package provider

import (
	"testing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAccountIsModelSupported_QoderMappingWhitelistSemantics(t *testing.T) {
	tests := []struct {
		name           string
		credentials    map[string]any
		requestedModel string
		expected       bool
	}{
		{
			name: "mapping only does not restrict unmatched request model",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
				},
			},
			requestedModel: "auto",
			expected:       true,
		},
		{
			name: "explicit empty whitelist keeps mapping unrestricted",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
				},
				"model_whitelist": []any{},
			},
			requestedModel: "auto",
			expected:       true,
		},
		{
			name: "whitelist allows mapped final route key",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
				},
				"model_whitelist": []any{"ultimate"},
			},
			requestedModel: "claude-opus-4-6",
			expected:       true,
		},
		{
			name: "whitelist rejects final model miss",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
				},
				"model_whitelist": []any{"ultimate"},
			},
			requestedModel: "auto",
			expected:       false,
		},
		{
			name: "whitelist public alias matches raw route key",
			credentials: map[string]any{
				"model_whitelist": []any{"claude-opus-4-6"},
			},
			requestedModel: "ultimate",
			expected:       true,
		},
		{
			name: "legacy raw self mapping is not treated as a whitelist",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"ultimate": "ultimate",
				},
			},
			requestedModel: "auto",
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &accountcore.Record{
				Platform:    capability.PlatformQoder,
				Credentials: tt.credentials,
			}

			require.Equal(t, tt.expected, account.IsModelSupported(tt.requestedModel, ModelDefaults(), ModelRules(account)))
		})
	}
}

func TestAccountGetConfiguredRequestModels_QoderMappingWhitelistSemantics(t *testing.T) {
	mappingOnly := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
		},
	}
	require.Equal(t, []string{"claude-opus-4-6"}, mappingOnly.GetConfiguredRequestModels(ModelDefaults()))

	withWhitelist := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
			"model_whitelist": []any{"ultimate"},
		},
	}
	require.Equal(t, []string{"claude-opus-4-6"}, withWhitelist.GetConfiguredRequestModels(ModelDefaults()))

	whitelistOnly := &accountcore.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_whitelist": []any{"claude-opus-4-6", "glm-5.2"},
		},
	}
	require.Equal(t, []string{"claude-opus-4-6", "glm-5.2"}, whitelistOnly.GetConfiguredRequestModels(ModelDefaults()))
}

func TestAccountIsModelSupported_QoderSiteCompatibility(t *testing.T) {
	global := &accountcore.Record{Platform: capability.PlatformQoder, Credentials: map[string]any{"site": "global"}}
	cn := &accountcore.Record{Platform: capability.PlatformQoder, Credentials: map[string]any{"site": "cn"}}

	require.True(t, global.IsModelSupported("claude-opus-4-6", ModelDefaults(), ModelRules(global)))
	require.False(t, cn.IsModelSupported("claude-opus-4-6", ModelDefaults(), ModelRules(cn)))
	require.False(t, global.IsModelSupported("qwen3.6-flash", ModelDefaults(), ModelRules(global)))
	require.True(t, cn.IsModelSupported("qwen3.6-flash", ModelDefaults(), ModelRules(cn)))
	require.False(t, global.IsModelSupported("q36fmodel", ModelDefaults(), ModelRules(global)))
	require.True(t, cn.IsModelSupported("q36fmodel", ModelDefaults(), ModelRules(cn)))
	require.True(t, global.IsModelSupported("mmodel", ModelDefaults(), ModelRules(global)))
	require.True(t, cn.IsModelSupported("mmodel", ModelDefaults(), ModelRules(cn)))
	require.True(t, global.IsModelSupported("unknown-raw-key", ModelDefaults(), ModelRules(global)))

	cn.Credentials["model_mapping"] = map[string]any{"claude-opus-4-6": "ultimate"}
	require.True(t, cn.IsModelSupported("claude-opus-4-6", ModelDefaults(), ModelRules(cn)), "显式账号 mapping 应覆盖站点默认限制")
}
