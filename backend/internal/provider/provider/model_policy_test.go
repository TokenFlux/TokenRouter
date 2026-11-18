package provider

import (
	"net/http"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	acct "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/qodermodel"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// TestProviderSupportsOpenAIEndpointCapability compares authentication
// defaults, explicit capabilities, legacy route metadata, and unsupported
// names. Endpoint eligibility must respect configured disablement while
// applying the defined OAuth and API-key defaults.
func TestProviderSupportsOpenAIEndpointCapability(t *testing.T) {
	t.Run("OpenAI APIKey 默认兼容 chat、embeddings 和 alpha search", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityEmbeddings))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityAlphaSearch))
	})

	t.Run("OpenAI OAuth 默认仅兼容 chat", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("alpha search 允许 OpenAI OAuth/PAT 与 APIKey 提供商，拒绝 Grok", func(t *testing.T) {
		// OAuth/PAT search uses the Codex endpoint; API key search uses the configured /v1/alpha/search endpoint.
		// Both credential types must remain eligible for standalone search.
		apiKey := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
		}
		oauth := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
		}
		grok := &acct.Record{
			Platform: capability.PlatformGrok,
			Type:     capability.ProviderTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(apiKey, acct.OpenAIEndpointCapabilityAlphaSearch))
		require.True(t, SupportsOpenAIEndpoint(oauth, acct.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(grok, acct.OpenAIEndpointCapabilityAlphaSearch))
	})

	t.Run("显式列表支持同时声明 chat 和 embeddings", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation", "embeddings"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("显式列表只声明 chat 时不支持 embeddings", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
		// Text-generation capability also permits Alpha Search for OAuth and API key providers.
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityAlphaSearch))
		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("OAuth 显式列表沿用 chat 能力放行 alpha search", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"text_generation"},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityAlphaSearch))
	})

	// Legacy OAuth records can contain empty capability containers; treat these as unconfigured.
	for _, emptyCapabilities := range []struct {
		name  string // provider model policy scenario label
		value any    // supplied capability container value
	}{
		{name: "map[string]any", value: map[string]any{}},
		{name: "[]any", value: []any{}},
		{name: "[]string", value: []string{}},
	} {
		t.Run("OAuth 空能力 "+emptyCapabilities.name, func(t *testing.T) {
			provider := &acct.Record{
				Platform: capability.PlatformOpenAI,
				Type:     capability.ProviderTypeOAuth,
				Credentials: map[string]any{
					acct.OpenAIWorkloadCapabilitiesCredentialKey: emptyCapabilities.value,
				},
			}

			require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
			require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
		})
	}

	t.Run("API Key 空能力仍表示显式禁用", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				acct.OpenAIWorkloadCapabilitiesCredentialKey: []any{},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("SetupToken 空能力与 OAuth 一样回退为未配置", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeSetupToken,
			Credentials: map[string]any{
				acct.OpenAIWorkloadCapabilitiesCredentialKey: map[string]any{},
			},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("非空全 false 能力仍按显式禁用处理", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				acct.OpenAIWorkloadCapabilitiesCredentialKey: map[string]any{
					string(acct.OpenAIEndpointCapabilityTextGeneration): false,
				},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("类型异常仍按已配置但不含能力处理", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Credentials: map[string]any{
				acct.OpenAIWorkloadCapabilitiesCredentialKey: "text_generation",
			},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("显式数组支持单独关闭文本生成并开启 embeddings", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"embeddings"},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityEmbeddings))
	})

	t.Run("未知能力不应默认放行", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapability("unknown")))
	})

	t.Run("responses 能力：未探测的 APIKey 默认放行", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：历史探测不再排除 APIKey", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"openai_responses_probe_status": "unsupported"},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityTextGeneration))
	})

	t.Run("responses 能力：探测确认支持的 APIKey 放行", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"openai_responses_probe_status": "supported"},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：force_chat_completions 覆盖排除 APIKey", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Extra:    map[string]any{"openai_text_route_mode": "force_chat_completions"},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：OAuth 提供商不受探测标记影响", func(t *testing.T) {
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeOAuth,
			Extra:    map[string]any{"openai_responses_probe_status": "unsupported"},
		}

		require.True(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
	})

	t.Run("responses 能力：仍需通过 chat_completions 配置集校验", func(t *testing.T) {
		// An embeddings-only workload list excludes Responses despite its default protocol support.
		provider := &acct.Record{
			Platform: capability.PlatformOpenAI,
			Type:     capability.ProviderTypeAPIKey,
			Credentials: map[string]any{
				"openai_workload_capabilities": []any{"embeddings"},
			},
		}

		require.False(t, SupportsOpenAIEndpoint(provider, acct.OpenAIEndpointCapabilityResponses))
	})
}

// TestGrokProviderModelMappingRemainsExplicit reads missing, invalid, and
// supplied Grok mappings. Resolution must return only usable saved rules,
// leaving absent or invalid mappings empty and configured aliases intact.
func TestGrokProviderModelMappingRemainsExplicit(t *testing.T) {
	tests := []struct {
		name        string            // provider model policy scenario label
		credentials map[string]any    // supplied provider credential configuration
		want        map[string]string // expected saved mapping rules
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
			provider := &acct.Record{Platform: capability.PlatformGrok, Credentials: test.credentials}
			require.Equal(t, test.want, acct.ResolveModelMapping(provider, ModelDefaults()))
		})
	}
}

// TestGrokWhitelistRunsBeforeBuiltinNormalization compares unrestricted,
// explicit-whitelist, and mapped Grok records. Admission must check the
// configured final name before platform normalization can turn an otherwise
// rejected alias into another model.
func TestGrokWhitelistRunsBeforeBuiltinNormalization(t *testing.T) {
	unrestricted := &acct.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}
	require.True(t, unrestricted.IsModelSupported("custom-grok-model", ModelDefaults(), ModelRules(unrestricted)))
	require.True(t, unrestricted.IsModelSupported("grok", ModelDefaults(), ModelRules(unrestricted)))

	strict := &acct.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, strict.IsModelSupported("grok-4.5", ModelDefaults(), ModelRules(strict)))
	require.False(t, strict.IsModelSupported("grok", ModelDefaults(), ModelRules(strict)))

	mapped := &acct.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping":   map[string]any{"grok": "grok-4.5"},
			"model_whitelist": []any{"grok-4.5"},
		},
	}
	require.True(t, mapped.IsModelSupported("grok", ModelDefaults(), ModelRules(mapped)))

	legacy := &acct.Record{
		Platform: capability.PlatformGrok,
		Credentials: map[string]any{
			"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
		},
	}
	require.True(t, legacy.IsModelSupported("grok-4.5", ModelDefaults(), ModelRules(

		legacy)))
	require.True(t, legacy.IsModelSupported("grok", ModelDefaults(), ModelRules(legacy)))
}

// TestGrokMediaCapabilityKeepsOnlyUnobservedOAuthAsProbeCandidate compares
// missing billing evidence with a partial billing observation. Only the
// unobserved record must remain a media-probe candidate under endpoint
// capability rules.
func TestGrokMediaCapabilityKeepsOnlyUnobservedOAuthAsProbeCandidate(t *testing.T) {
	unobserved := &acct.Record{Platform: capability.PlatformGrok, Type: capability.ProviderTypeOAuth}
	eligible, reason := acct.GrokMediaGenerationEligibility(unobserved, GrokTierRules())
	require.False(t, eligible)
	require.Equal(t, "billing_unobserved", reason)
	require.True(t, SupportsOpenAIEndpoint(unobserved, acct.OpenAIEndpointCapabilityGrokMediaGeneration))

	inconclusive := &acct.Record{
		Platform: capability.PlatformGrok,
		Type:     capability.ProviderTypeOAuth,
		Extra: map[string]any{acct.GrokUsageBillingExtraKey: &xai.BillingSummary{
			StatusCode: http.StatusOK,
			Partial:    true,
		}},
	}
	require.False(t, SupportsOpenAIEndpoint(inconclusive, acct.OpenAIEndpointCapabilityGrokMediaGeneration))
}

// TestMixedGroupProviderModelScope checks Anthropic and OpenAI records with
// unknown and cross-platform names. Empty scope stays unrestricted, while
// explicit final-model whitelists constrain mapped results and omit wildcard
// patterns from enumeration.
func TestMixedGroupProviderModelScope(t *testing.T) {
	// Empty configuration accepts unknown models; a whitelist restricts the final mapped name.
	claude := "claude-sonnet-4-6"
	gpt := "gpt-5.4"
	for _, tc := range []struct {
		platform string // provider platform under test
		own      string // platform-associated model example
		other    string // other platform model example
	}{
		{acct.PlatformAnthropic, claude, gpt},
		{acct.PlatformOpenAI, gpt, claude},
	} {
		t.Run(tc.platform, func(t *testing.T) {
			value := &acct.Record{Platform: tc.platform, Type: acct.ProviderTypeAPIKey}
			require.True(t, value.IsModelSupported(tc.own, ModelDefaults(), ModelRules(value)))
			require.True(t, value.IsModelSupported(tc.other, ModelDefaults(), ModelRules(value)))
			require.True(t, value.IsModelSupported("unknown-model", ModelDefaults(), ModelRules(value)))
			value.Credentials = map[string]any{"model_mapping": map[string]any{"custom-alias": "custom-upstream"}}
			require.True(t, value.IsModelSupported("custom-alias", ModelDefaults(), ModelRules(value)))
			value.Credentials["model_whitelist"] = []string{"custom-*"}
			require.True(t, value.IsModelSupported("custom-model", ModelDefaults(), ModelRules(value)))
			require.False(t, value.IsModelSupported(tc.own, ModelDefaults(), ModelRules(value)))
			require.NotContains(t, value.GetConfiguredRequestModels(ModelDefaults()), "custom-*")
			value.Credentials["model_whitelist"] = []string{"*"}
			require.True(t, value.IsModelSupported(tc.other, ModelDefaults(), ModelRules(value)))
			require.NotContains(t, value.GetConfiguredRequestModels(ModelDefaults()), "*")
		})
	}
}

// TestGrokProviderModelMappingRemainsIndependentFromRuntimeSettings changes the
// global Grok text default and reads an unrelated request model. Mapping
// resolution must retain that request spelling regardless of the runtime
// default.
func TestGrokProviderModelMappingRemainsIndependentFromRuntimeSettings(t *testing.T) {
	original := xai.RuntimeDefaultTextModel()
	t.Cleanup(func() { xai.SetRuntimeDefaultTextModel(original) })
	provider := &acct.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}

	xai.SetRuntimeDefaultTextModel("")
	requireMappedModel(t, provider, "claude-sonnet-4-5", "claude-sonnet-4-5")

	xai.SetRuntimeDefaultTextModel("grok-build-0.1")
	requireMappedModel(t, provider, "claude-sonnet-4-5", "claude-sonnet-4-5")
}

// TestProviderIsModelSupported combines absent scope, aliases, terminal
// wildcards, explicit final whitelists, and Qoder routes. Admission must apply
// one mapping before scope and platform checks while allowing unmatched
// requests in unrestricted records.
func TestProviderIsModelSupported(t *testing.T) {
	tests := []struct {
		name           string         // provider model policy scenario label
		platform       string         // provider platform under test
		credentials    map[string]any // supplied provider credential configuration
		requestedModel string         // request model under evaluation
		expected       bool           // expected model admission result
	}{
		// An empty whitelist accepts specific models absent from the catalogue.
		{
			name:           "missing mapping allows unknown model",
			credentials:    nil,
			requestedModel: "any-model",
			expected:       true,
		},
		{
			name:           "empty mapping allows unknown model",
			credentials:    map[string]any{},
			requestedModel: "any-model",
			expected:       true,
		},

		{
			name: "exact match supported",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-sonnet-4-5": "target-model",
				},
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       true,
		},
		{
			name: "exact mapping miss is allowed as passthrough without whitelist",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-sonnet-4-5": "target-model",
				},
			},
			requestedModel: "claude-opus-4-5",
			expected:       true,
		},

		{
			name: "wildcard match supported",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-*": "claude-sonnet-4-5",
				},
			},
			requestedModel: "claude-opus-4-5-thinking",
			expected:       true,
		},
		{
			name:     "gemini customtools alias matches normalized mapping",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview": "gemini-3.1-pro-preview",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expected:       true,
		},
		{
			name: "wildcard mapping miss is allowed as passthrough without whitelist",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-*": "claude-sonnet-4-5",
				},
			},
			requestedModel: "gemini-3-flash",
			expected:       true,
		},
		{
			name: "mapping is checked before final whitelist",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-a": "model-b",
				},
				"model_whitelist": []any{"model-b", "model-c"},
			},
			requestedModel: "model-a",
			expected:       true,
		},
		{
			name: "mapped final model must also be in whitelist when whitelist exists",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-a": "model-b",
				},
				"model_whitelist": []any{"model-c"},
			},
			requestedModel: "model-a",
			expected:       false,
		},
		{
			name: "final whitelist model is also directly requestable as implicit passthrough",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-a": "model-b",
				},
				"model_whitelist": []any{"model-b", "model-c"},
			},
			requestedModel: "model-c",
			expected:       true,
		},
		{
			name: "mapping without explicit whitelist still allows mapped request",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-a": "model-b",
				},
			},
			requestedModel: "model-a",
			expected:       true,
		},
		{
			name: "mapping without explicit whitelist allows unmatched request as passthrough",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-a": "model-b",
				},
			},
			requestedModel: "model-b",
			expected:       true,
		},
		{
			name: "explicit empty whitelist disables legacy self-mapping fallback",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"model-b": "model-b",
				},
				"model_whitelist": []any{},
			},
			requestedModel: "model-c",
			expected:       true,
		},
		{
			name:           "qoder mapping absent accepts an observed public alias",
			platform:       capability.PlatformQoder,
			credentials:    nil,
			requestedModel: "qwen3.8-max",
			expected:       true,
		},
		{
			name:           "qoder mapping absent does not reject raw upstream key",
			platform:       capability.PlatformQoder,
			credentials:    nil,
			requestedModel: "ultimate",
			expected:       true,
		},
		{
			name:           "qoder global accepts qwen 38 public alias",
			platform:       capability.PlatformQoder,
			credentials:    map[string]any{"site": "global"},
			requestedModel: "qwen3.8-max",
			expected:       true,
		},
		{
			name:           "qoder global accepts qwen 38 raw route",
			platform:       capability.PlatformQoder,
			credentials:    map[string]any{"site": "global"},
			requestedModel: "qmodel_38max",
			expected:       true,
		},
		{
			name:           "qoder cn accepts qwen 38 public alias",
			platform:       capability.PlatformQoder,
			credentials:    map[string]any{"site": "cn"},
			requestedModel: "qwen3.8-max",
			expected:       true,
		},
		{
			name:           "qoder cn accepts qwen 38 raw route",
			platform:       capability.PlatformQoder,
			credentials:    map[string]any{"site": "cn"},
			requestedModel: "qmodel_38max",
			expected:       true,
		},
		{
			name:     "qoder preview requires explicit mapping when route whitelist is used",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"site":            "cn",
				"model_whitelist": []any{"qmodel_38max"},
			},
			requestedModel: "qwen3.8-max-preview",
			expected:       false,
		},
		{
			name:     "qoder explicit preview mapping remains compatible",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"site": "cn",
				"model_mapping": map[string]any{
					"qwen3.8-max-preview": "qmodel_38max",
				},
				"model_whitelist": []any{"qmodel_38max"},
			},
			requestedModel: "qwen3.8-max-preview",
			expected:       true,
		},
		{
			name:     "qoder mapping only does not restrict unmatched request model",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
					"auto":            "auto",
				},
				"model_whitelist": []any{},
			},
			requestedModel: "glm-5.3",
			expected:       true,
		},
		{
			name:     "qoder whitelist allows mapped final route key",
			platform: capability.PlatformQoder,
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
			name:     "qoder whitelist rejects unmatched final model",
			platform: capability.PlatformQoder,
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
			name:     "qoder whitelist accepts public alias for final route key",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-opus-4-6": "ultimate",
				},
				"model_whitelist": []any{"claude-opus-4-6"},
			},
			requestedModel: "ultimate",
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			attachQoderModelPolicyFixture(provider)
			result := provider.IsModelSupported(tt.requestedModel, ModelDefaults(), ModelRules(provider))
			if result != tt.expected {
				t.Errorf("IsModelSupported(%q) = %v, want %v", tt.requestedModel, result, tt.expected)
			}
		})
	}
}

// TestProviderGetConfiguredRequestModels enumerates aliases, targets, whitelist
// names, and finite Qoder defaults. The result must contain permitted concrete
// candidates and keep advertised defaults consistent with provider admission.
func TestProviderGetConfiguredRequestModels(t *testing.T) {
	tests := []struct {
		name        string         // provider model policy scenario label
		platform    string         // provider platform under test
		credentials map[string]any // supplied provider credential configuration
		expected    []string       // expected concrete candidate names
		// includeDefaults adds platform candidates to membership assertions. These
		// cases also check that every returned model passes the provider's admission
		// rules.
		includeDefaults bool
	}{
		{
			name: "mapping lists its explicit alias and target",
			credentials: map[string]any{
				"model_mapping": map[string]any{"model-a": "model-b"},
			},
			expected: []string{"model-a", "model-b"},
		},
		{
			name: "explicit whitelist returns whitelist and mapping keys",
			credentials: map[string]any{
				"model_mapping":   map[string]any{"model-a": "model-b"},
				"model_whitelist": []any{"model-b", "model-c"},
			},
			expected: []string{"model-a", "model-b", "model-c"},
		},
		{
			name: "empty whitelist keeps explicit alias and target",
			credentials: map[string]any{
				"model_mapping":   map[string]any{"model-a": "model-b"},
				"model_whitelist": []any{},
			},
			expected: []string{"model-a", "model-b"},
		},
		{
			name:            "qoder mapping lists defaults and explicit aliases",
			includeDefaults: true,
			platform:        capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"},
			},
			expected: []string{"claude-opus-4-6", "ultimate"},
		},
		{
			name:     "qoder explicit mapping returns mapping keys for model list display",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping":   map[string]any{"claude-opus-4-6": "ultimate"},
				"model_whitelist": []any{"ultimate"},
			},
			expected: []string{"claude-opus-4-6", "ultimate"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			attachQoderModelPolicyFixture(provider)
			result := provider.GetConfiguredRequestModels(ModelDefaults())
			if tt.includeDefaults {
				for _, model := range append(DefaultProviderModels(provider), tt.expected...) {
					if !slices.Contains(result, model) {
						t.Fatalf("missing configured model %q in %v", model, result)
					}
				}
				for _, model := range result {
					if !provider.IsModelSupported(model, ModelDefaults(), ModelRules(provider)) {
						t.Fatalf("advertised unavailable model %q", model)
					}
				}
				return
			}
			if !reflect.DeepEqual(result, tt.expected) {
				t.Fatalf("GetConfiguredRequestModels() = %#v, want %#v", result, tt.expected)
			}
		})
	}
}

// TestProviderGetMappedModel compares exact, wildcard, and unmatched rules,
// including Gemini suffix spellings. Resolution must return the expected
// configured target or untouched request name under the current mapping rules.
func TestProviderGetMappedModel(t *testing.T) {
	tests := []struct {
		name           string         // provider model policy scenario label
		platform       string         // provider platform under test
		credentials    map[string]any // supplied provider credential configuration
		requestedModel string         // request model under evaluation
		expected       string         // expected final model target
	}{
		{
			name:           "no mapping returns original",
			credentials:    nil,
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-4-5",
		},
		{
			name:           "no mapping preserves gemini customtools model",
			platform:       capability.PlatformGemini,
			credentials:    nil,
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expected:       "gemini-3.1-pro-preview-customtools",
		},

		{
			name: "exact match",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-sonnet-4-5": "target-model",
				},
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       "target-model",
		},

		{
			name: "wildcard longest match",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"claude-*":        "claude-default",
					"claude-sonnet-*": "claude-sonnet-mapped",
				},
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-mapped",
		},

		{
			name:     "gemini customtools alias resolves through normalized mapping",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview": "gemini-3.1-pro-preview",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expected:       "gemini-3.1-pro-preview-customtools",
		},
		{
			name:     "gemini customtools exact mapping wins over normalized fallback",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview":             "gemini-3.1-pro-preview",
					"gemini-3.1-pro-preview-customtools": "gemini-3.1-pro-preview-customtools",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expected:       "gemini-3.1-pro-preview-customtools",
		},
		{
			name: "no match returns original",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-*": "gemini-mapped",
				},
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-4-5",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result, _ := acct.ResolveMappedModel(acct.ResolveModelMapping(provider, ModelDefaults()), tt.requestedModel)
			if result != tt.expected {
				t.Errorf("GetMappedModel(%q) = %q, want %q", tt.requestedModel, result, tt.expected)
			}
		})
	}
}

// TestProviderGetModelMapping_AntigravityNormalizesGemini31ProAliases reads
// saved Antigravity Gemini aliases and targets. The returned mapping must
// retain every saved value and contain exactly the configured number of
// entries.
func TestProviderGetModelMapping_AntigravityNormalizesGemini31ProAliases(t *testing.T) {
	raw := map[string]any{"gemini-pro-agent": "gemini-pro-agent", "gemini-3.1-pro-high": "gemini-3.1-pro-high", "gemini-3.1-pro-preview": "gemini-3.1-pro-high"}
	provider := &acct.Record{Platform: capability.PlatformAntigravity, Credentials: map[string]any{"model_mapping": raw}}
	mapping := acct.ResolveModelMapping(provider, ModelDefaults())
	if len(mapping) != len(raw) {
		t.Fatalf("读取映射时不应补项: %v", mapping)
	}
	for source, target := range raw {
		if mapping[source] != target {
			t.Fatalf("显式映射被改写: %s", source)
		}
	}
}

// TestProviderGetModelMapping_AntigravityPreservesGemini31ProOverrides supplies
// saved Gemini aliases for an Antigravity provider. Mapping reads retain the
// configured entries, and the forwarding model resolves to the saved target.
func TestProviderGetModelMapping_AntigravityPreservesGemini31ProOverrides(t *testing.T) {
	raw := map[string]any{"gemini-pro-agent": "custom-model", "gemini-3.1-pro-high": "gemini-pro-agent"}
	provider := &acct.Record{Platform: capability.PlatformAntigravity, Credentials: map[string]any{"model_mapping": raw}}
	mapping := acct.ResolveModelMapping(provider, ModelDefaults())
	if len(mapping) != len(raw) {
		t.Fatalf("读取映射时不应补项: %v", mapping)
	}
	for source, target := range raw {
		if mapping[source] != target {
			t.Fatalf("显式映射被改写: %s", source)
		}
	}
	require.Equal(t, "gemini-pro-agent", MapAntigravityModel(provider, "gemini-3.1-pro-high"))
}

// TestProviderGetModelMapping_AntigravityGemini31ProAliasesRespectWildcard
// supplies an identity rule together with a Gemini wildcard. The configured
// wildcard can match Gemini 3.1 aliases without exact entries in the saved map.
func TestProviderGetModelMapping_AntigravityGemini31ProAliasesRespectWildcard(t *testing.T) {
	t.Parallel()

	provider := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-pro-agent": "gemini-pro-agent",
				"gemini-3.1-*":     "custom-wildcard",
			},
		},
	}

	mapping := acct.ResolveModelMapping(provider, ModelDefaults())

	if got := mapping["gemini-3.1-pro"]; got != "" {
		t.Fatalf("expected gemini-3.1-pro exact alias to stay unset when wildcard exists, got %q", got)
	}
	if got := mapping["gemini-3.1-pro-high"]; got != "" {
		t.Fatalf("expected gemini-3.1-pro-high exact alias to stay unset when wildcard exists, got %q", got)
	}
	if got := mapping["gemini-3.1-pro-preview"]; got != "" {
		t.Fatalf("expected gemini-3.1-pro-preview exact alias to stay unset when wildcard exists, got %q", got)
	}
}

// TestProviderResolveMappedModel checks absent rules, identity targets,
// wildcard targets, and Gemini spellings. The target and matched flag must
// distinguish a successful identity rule from an unmatched request.
func TestProviderResolveMappedModel(t *testing.T) {
	tests := []struct {
		name           string         // provider model policy scenario label
		platform       string         // provider platform under test
		credentials    map[string]any // supplied provider credential configuration
		requestedModel string         // request model under evaluation
		expectedModel  string         // expected final mapping target
		expectedMatch  bool           // expected mapping rule match
	}{
		{
			name:           "no mapping reports unmatched",
			credentials:    nil,
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  false,
		},
		{
			name: "exact passthrough mapping still counts as matched",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.4": "gpt-5.4",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  true,
		},
		{
			name: "wildcard passthrough mapping still counts as matched",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-*": "gpt-5.4",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  true,
		},
		{
			name:     "gemini customtools alias reports normalized match",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview": "gemini-3.1-pro-preview",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expectedModel:  "gemini-3.1-pro-preview-customtools",
			expectedMatch:  false,
		},
		{
			name:     "gemini customtools exact mapping reports exact match",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview":             "gemini-3.1-pro-preview",
					"gemini-3.1-pro-preview-customtools": "gemini-3.1-pro-preview-customtools",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expectedModel:  "gemini-3.1-pro-preview-customtools",
			expectedMatch:  true,
		},
		{
			name: "missing mapping reports unmatched",
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gpt-5.2": "gpt-5.2",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			mappedModel, matched := acct.ResolveMappedModel(acct.ResolveModelMapping(provider, ModelDefaults()), tt.requestedModel)
			if mappedModel != tt.expectedModel || matched != tt.expectedMatch {
				t.Fatalf("ResolveMappedModel(%q) = (%q, %v), want (%q, %v)", tt.requestedModel, mappedModel, matched, tt.expectedModel, tt.expectedMatch)
			}
		})
	}
}

// TestProviderGetModelMapping_AntigravityEnsuresGeminiDefaultPassthroughs reads
// one saved Antigravity mapping. Resolution must retain that configured set
// exactly rather than inserting implicit model passthrough entries.
func TestProviderGetModelMapping_AntigravityEnsuresGeminiDefaultPassthroughs(t *testing.T) {
	raw := map[string]any{"gemini-3-pro-high": "gemini-3.1-pro-high"}
	provider := &acct.Record{Platform: capability.PlatformAntigravity, Credentials: map[string]any{"model_mapping": raw}}
	mapping := acct.ResolveModelMapping(provider, ModelDefaults())
	if len(mapping) != len(raw) {
		t.Fatalf("读取映射时不应补项: %v", mapping)
	}
	for source, target := range raw {
		if mapping[source] != target {
			t.Fatalf("显式映射被改写: %s", source)
		}
	}
}

// TestGoogleOneEmptyWhitelistAllowsUnknownModel supplies Google One
// authentication without model configuration. Mapping must remain empty while
// admission accepts an unknown model through the unrestricted scope.
func TestGoogleOneEmptyWhitelistAllowsUnknownModel(t *testing.T) {
	value := &acct.Record{Platform: capability.PlatformGemini, Type: capability.ProviderTypeOAuth, Credentials: map[string]any{"oauth_type": "google_one"}}
	if len(acct.ResolveModelMapping(value, ModelDefaults())) != 0 {
		t.Fatal("unexpected implicit mapping")
	}
	if !value.IsModelSupported("gemini-future-model", ModelDefaults(), ModelRules(value)) {
		t.Fatal("empty whitelist rejected unknown model")
	}
}

// TestProviderGetModelMapping_GoogleOnePreservesExplicitMapping supplies one
// custom Google One alias. Resolution must retain its target and avoid
// introducing a target identity entry from platform defaults.
func TestProviderGetModelMapping_GoogleOnePreservesExplicitMapping(t *testing.T) {
	provider := &acct.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.ProviderTypeOAuth,
		Credentials: map[string]any{
			"oauth_type": "google_one",
			"model_mapping": map[string]any{
				"custom-model": "gemini-2.5-flash",
			},
		},
	}

	mapping := acct.ResolveModelMapping(provider, ModelDefaults())
	if mapping["custom-model"] != "gemini-2.5-flash" {
		t.Fatalf("expected explicit Google One mapping to be preserved, got %v", mapping)
	}
	if _, ok := mapping["gemini-2.5-flash"]; ok {
		t.Fatalf("did not expect defaults to overwrite an explicit mapping: %v", mapping)
	}
}

// TestProviderGetModelMapping_AntigravityRespectsWildcardOverride supplies one
// Gemini wildcard target. The wildcard must handle concrete Gemini names when
// the saved mapping has no exact rule for those names.
func TestProviderGetModelMapping_AntigravityRespectsWildcardOverride(t *testing.T) {
	provider := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-3*": "gemini-3.1-pro-high",
			},
		},
	}

	mapping := acct.ResolveModelMapping(provider, ModelDefaults())
	if _, exists := mapping["gemini-3-flash"]; exists {
		t.Fatalf("did not expect explicit gemini-3-flash passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-high"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-high passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-low"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-low passthrough when wildcard already exists")
	}
	if mapped, _ := acct.ResolveMappedModel(acct.ResolveModelMapping(provider, ModelDefaults()), "gemini-3-flash"); mapped != "gemini-3.1-pro-high" {
		t.Fatalf("expected wildcard mapping to stay effective, got: %q", mapped)
	}
}

// TestProviderGetModelMapping_CacheInvalidatesOnCredentialsReplace reads a
// mapping and then replaces the credential object. The next read must reflect
// the replacement target rather than the earlier result.
func TestProviderGetModelMapping_CacheInvalidatesOnCredentialsReplace(t *testing.T) {
	provider := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-3-5-sonnet": "upstream-a",
			},
		},
	}

	first := acct.ResolveModelMapping(provider, ModelDefaults())
	if first["claude-3-5-sonnet"] != "upstream-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	provider.Credentials = map[string]any{
		"model_mapping": map[string]any{
			"claude-3-5-sonnet": "upstream-b",
		},
	}
	second := acct.ResolveModelMapping(provider, ModelDefaults())
	if second["claude-3-5-sonnet"] != "upstream-b" {
		t.Fatalf("expected cache invalidated after credentials replace, got: %v", second)
	}
}

// TestProviderGetModelMapping_CacheInvalidatesOnMappingLenChange reads a
// single-rule mapping and then inserts another rule. A subsequent resolution
// must include the newly stored target after that in-place edit.
func TestProviderGetModelMapping_CacheInvalidatesOnMappingLenChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	provider := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := acct.ResolveModelMapping(provider, ModelDefaults())
	if len(first) != 1 {
		t.Fatalf("unexpected first mapping length: %d", len(first))
	}

	rawMapping["claude-opus"] = "opus-b"
	second := acct.ResolveModelMapping(provider, ModelDefaults())
	if second["claude-opus"] != "opus-b" {
		t.Fatalf("expected cache invalidated after mapping len change, got: %v", second)
	}
}

// TestProviderGetModelMapping_CacheInvalidatesOnInPlaceValueChange replaces an
// existing target without changing map size. A subsequent read must observe the
// edited value rather than relying on map length alone.
func TestProviderGetModelMapping_CacheInvalidatesOnInPlaceValueChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	provider := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := acct.ResolveModelMapping(provider, ModelDefaults())
	if first["claude-sonnet"] != "sonnet-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	rawMapping["claude-sonnet"] = "sonnet-b"
	second := acct.ResolveModelMapping(provider, ModelDefaults())
	if second["claude-sonnet"] != "sonnet-b" {
		t.Fatalf("expected cache invalidated after in-place value change, got: %v", second)
	}
}

// TestModelMappingResultIsolation mutates a mapping result returned to the
// caller. A later resolution must still return the saved target from the
// provider's independent configuration.
func TestModelMappingResultIsolation(t *testing.T) {
	provider := &acct.Record{Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	first := acct.ResolveModelMapping(provider, ModelDefaults())
	first["alias"] = "changed-by-caller"
	require.Equal(t, "target", acct.ResolveModelMapping(provider, ModelDefaults())["alias"])
}

// TestModelMappingConcurrentReads releases several readers against one stable
// provider mapping. Every resolution must return the configured target while
// the shared credential map remains read-only.
func TestModelMappingConcurrentReads(t *testing.T) {
	provider := &acct.Record{Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_mapping": map[string]any{"alias": "target"}}}
	start := make(chan struct{})
	var wg sync.WaitGroup
	// Release mapping readers together and wait for them all, exercising shared
	// immutable configuration while each resolution returns a request-private
	// mapping result.
	for range 16 {
		wg.Go(func() {
			<-start
			for range 20 {
				if acct.ResolveModelMapping(provider, ModelDefaults())["alias"] != "target" {
					t.Error("模型映射读取结果发生变化")
				}
			}
		})
	}
	close(start)
	wg.Wait()
}

// TestIsModelSupported_OpenAIOAuthEmptyMappingAllowsUnknownModels checks
// recognized and unknown names on an unrestricted OAuth record. Both sets must
// remain eligible when no foreign-family exclusion or explicit whitelist
// applies.
func TestIsModelSupported_OpenAIOAuthEmptyMappingAllowsUnknownModels(t *testing.T) {
	provider := newOpenAIOAuthProviderForModelTest()
	for _, model := range []string{"gpt-5.4", "gpt-5.6-terra", "gpt-5.6-sol"} {
		require.True(t, provider.IsModelSupported(model, ModelDefaults(), ModelRules(provider)), model)
	}
	for _, model := range []string{"my-custom-alias", "model-outside-current-catalog"} {
		require.True(t, provider.IsModelSupported(model, ModelDefaults(), ModelRules(provider)), model)
	}
}

// TestIsModelSupported_OpenAIOAuthEmptyMapping_RejectsForeignModels supplies
// known foreign model families and vendor-qualified forms. OAuth admission must
// reject them even when the provider has no mapping or explicit whitelist.
func TestIsModelSupported_OpenAIOAuthEmptyMapping_RejectsForeignModels(t *testing.T) {
	provider := newOpenAIOAuthProviderForModelTest()

	// The OAuth model policy excludes these foreign model families before an upstream request.
	foreign := []string{
		"deepseek-v4",
		"deepseek-chat",
		"glm-4.7",
		"kimi-k2",
		"k3",
		"k3-256k",
		"moonshot-v1-128k",
		"gemini-3.0-pro",
		"grok-4",
		"qwen3-max",
		"minimax-m2.5",
		"llama-3.3-70b",
		"provider/deepseek-v4", // Check the model segment of a vendor-qualified ID.
		"provider/k3",          // Vendor-qualified Kimi Code ID.
	}
	for _, model := range foreign {
		require.False(t, provider.IsModelSupported(model, ModelDefaults(), ModelRules(provider)), "expected %q to be rejected by empty-mapping OpenAI OAuth provider", model)
	}
}

// TestIsModelSupported_OpenAIOAuthMappingKeepsForkSemantics maps
// foreign-looking aliases to an eligible OpenAI target. Admission must accept
// mapped aliases and continue rejecting an unmapped foreign model, including
// under an explicit target whitelist.
func TestIsModelSupported_OpenAIOAuthMappingKeepsForkSemantics(t *testing.T) {
	provider := newOpenAIOAuthProviderForModelTest()
	provider.Credentials = map[string]any{
		"model_mapping": map[string]any{"deepseek-v4": "gpt-5.4", "k3": "gpt-5.4"},
	}

	// Configured aliases resolve before authentication-specific model eligibility is checked.
	require.True(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
	require.True(t, provider.IsModelSupported("k3", ModelDefaults(), ModelRules(provider)))
	require.False(t, provider.IsModelSupported("glm-4.7", ModelDefaults(), ModelRules(provider)))

	provider.Credentials["model_whitelist"] = []any{"gpt-5.4"}
	require.True(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
	require.False(t, provider.IsModelSupported("glm-4.7", ModelDefaults(), ModelRules(provider)))
}

// TestIsModelSupported_OpenAIOAuthEmptyMappingRespectsWhitelist includes both
// an eligible OpenAI model and a foreign model in scope. Membership must still
// respect OAuth model eligibility and reject names outside the whitelist.
func TestIsModelSupported_OpenAIOAuthEmptyMappingRespectsWhitelist(t *testing.T) {
	provider := newOpenAIOAuthProviderForModelTest()
	provider.Credentials = map[string]any{"model_whitelist": []any{"gpt-5.4", "deepseek-v4"}}

	// A whitelist cannot override the platform's model eligibility rules.
	require.True(t, provider.IsModelSupported("gpt-5.4", ModelDefaults(), ModelRules(provider)))
	require.False(t, provider.IsModelSupported("gpt-5.3-codex", ModelDefaults(), ModelRules(provider)))
	require.False(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
}

// TestIsModelSupported_OpenAIOAuthPassthroughKeepsModelScope enables
// passthrough and then a global wildcard on an OAuth record. A known
// foreign-family model must remain rejected by authentication-specific
// admission.
func TestIsModelSupported_OpenAIOAuthPassthroughKeepsModelScope(t *testing.T) {
	provider := newOpenAIOAuthProviderForModelTest()
	provider.Extra = map[string]any{"openai_passthrough": true}
	require.False(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
	provider.Credentials = map[string]any{"model_whitelist": []string{"*"}}
	require.False(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
}

// TestIsModelSupported_OpenAIAPIKeyAllowsUnknownModels checks OpenAI and
// foreign-looking names on an unrestricted API-key record. Both must be
// admitted initially, and the explicit global wildcard must continue admitting
// the foreign-looking name.
func TestIsModelSupported_OpenAIAPIKeyAllowsUnknownModels(t *testing.T) {
	provider := &acct.Record{ID: 2, Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey}
	require.True(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
	require.True(t, provider.IsModelSupported("gpt-5.4", ModelDefaults(), ModelRules(provider)))
	provider.Credentials = map[string]any{"model_whitelist": []string{"*"}}
	require.True(t, provider.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(provider)))
}

// TestIsModelSupported_AnthropicEmptyWhitelistAllowsForeignModel checks a
// Claude model and a DeepSeek model on an unrestricted Anthropic OAuth record.
// Admission must accept both names when no whitelist is configured.
func TestIsModelSupported_AnthropicEmptyWhitelistAllowsForeignModel(t *testing.T) {
	anthropic := &acct.Record{ID: 3, Platform: capability.PlatformAnthropic, Type: capability.ProviderTypeOAuth}
	require.True(t, anthropic.IsModelSupported("claude-sonnet-4-6", ModelDefaults(), ModelRules(anthropic)))
	require.True(t, anthropic.IsModelSupported("deepseek-v4", ModelDefaults(), ModelRules(anthropic)))
}

// TestProviderIsModelSupported_QoderMappingWhitelistSemantics compares
// mapping-only, empty, explicit, and identity mapping cases. Qoder admission
// must keep mappings separate from final scope restrictions while applying
// configured target membership.
func TestProviderIsModelSupported_QoderMappingWhitelistSemantics(t *testing.T) {
	tests := []struct {
		name           string         // provider model policy scenario label
		credentials    map[string]any // supplied provider credential configuration
		requestedModel string         // request model under evaluation
		expected       bool           // expected model admission result
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
			expected:       false,
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
			provider := &acct.Record{
				Platform:    capability.PlatformQoder,
				Credentials: tt.credentials,
			}

			attachQoderModelPolicyFixture(provider)
			require.Equal(t, tt.expected, provider.IsModelSupported(tt.requestedModel, ModelDefaults(), ModelRules(provider)))
		})
	}
}

// TestProviderGetConfiguredRequestModels_QoderMappingWhitelistSemantics
// enumerates mapping-only, mapping-with-whitelist, and whitelist-only records.
// Candidate names must include permitted aliases and targets while finite
// defaults follow the effective scope.
func TestProviderGetConfiguredRequestModels_QoderMappingWhitelistSemantics(t *testing.T) {
	mappingOnly := &acct.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
		},
	}
	attachQoderModelPolicyFixture(mappingOnly)
	models := mappingOnly.GetConfiguredRequestModels(ModelDefaults())
	require.Contains(t, models, "claude-opus-4-6")
	require.Contains(t, models, "auto")
	require.Contains(t, models, "ultimate")

	withWhitelist := &acct.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-opus-4-6": "ultimate",
			},
			"model_whitelist": []any{"ultimate"},
		},
	}
	require.ElementsMatch(t, []string{"claude-opus-4-6", "ultimate"}, withWhitelist.GetConfiguredRequestModels(ModelDefaults()))

	whitelistOnly := &acct.Record{
		Platform: capability.PlatformQoder,
		Credentials: map[string]any{
			"model_whitelist": []any{"claude-opus-4-6", "glm-5.2"},
		},
	}
	require.Equal(t, []string{"claude-opus-4-6", "glm-5.2"}, whitelistOnly.GetConfiguredRequestModels(ModelDefaults()))
}

// TestProviderIsModelSupported_QoderSiteCompatibility checks account catalogs
// under wildcard configuration. Missing observations and absent automatic routes
// stay unavailable, while explicitly configured custom routing remains possible.
func TestProviderIsModelSupported_QoderSiteCompatibility(t *testing.T) {
	global := &acct.Record{Platform: capability.PlatformQoder, Credentials: map[string]any{"site": "global"}}
	cn := &acct.Record{Platform: capability.PlatformQoder, Credentials: map[string]any{"site": "cn"}}
	attachQoderModelPolicyFixture(global)
	attachQoderModelPolicyFixture(cn)
	require.True(t, global.IsModelSupported("ultimate", ModelDefaults(), ModelRules(global)))
	require.False(t, cn.IsModelSupported("ultimate", ModelDefaults(), ModelRules(cn)))
	require.False(t, global.IsModelSupported("claude-opus-4-6", ModelDefaults(), ModelRules(global)))
	require.True(t, cn.IsModelSupported("qwen3.8-max", ModelDefaults(), ModelRules(cn)))
	require.False(t, global.IsModelSupported("unknown-raw-key", ModelDefaults(), ModelRules(global)))
	cn.Credentials["model_mapping"] = map[string]any{"custom": "future-route"}
	require.True(t, cn.IsModelSupported("custom", ModelDefaults(), ModelRules(cn)))
	cn.Extra = nil
	require.False(t, cn.IsModelSupported("qwen3.8-max", ModelDefaults(), ModelRules(cn)))
}

// TestUnrestrictedModels checks unknown models across all ordinary platforms
// with absent and empty whitelists. Explicit final wildcards must then
// constrain mapped names and change the unrestricted-scope result.
func TestUnrestrictedModels(t *testing.T) {
	for _, platform := range []string{"anthropic", "openai", "gemini", "antigravity", "qoder", "grok", "kimi", "zhipu", "deepseek"} {
		t.Run(platform, func(t *testing.T) {
			value := &acct.Record{Platform: platform, Type: "apikey", Credentials: map[string]any{}}
			attachQoderModelPolicyFixture(value)
			for _, explicit := range []bool{false, true} {
				if explicit {
					value.Credentials["model_whitelist"] = []any{}
				}
				require.Equal(t, platform != "qoder", value.IsModelSupported("unknown-new-model", ModelDefaults(), ModelRules(value)))
				require.True(t, value.HasUnrestrictedModelScope(ModelDefaults()))
			}
			value.Credentials["model_mapping"] = map[string]any{"alias": "unknown-new-model"}
			value.Credentials["model_whitelist"] = []any{"unknown-*"}
			require.True(t, value.IsModelSupported("alias", ModelDefaults(), ModelRules(value)))
			require.False(t, value.IsModelSupported("another-model", ModelDefaults(), ModelRules(value)))
			require.False(t, value.HasUnrestrictedModelScope(ModelDefaults()))
		})
	}
}

// TestSparkModelScope supplies an OpenAI OAuth shadow with independent Spark
// scope. It must reject unknown models and report restricted scope while
// admitting the finite native Spark model.
func TestSparkModelScope(t *testing.T) {
	parent := int64(1)
	value := &acct.Record{Platform: "openai", Type: "oauth", ParentProviderID: &parent}
	require.False(t, value.HasUnrestrictedModelScope(ModelDefaults()))
	require.False(t, value.IsModelSupported("unknown-new-model", ModelDefaults(), ModelRules(value)))
	require.True(t, value.IsModelSupported("gpt-5.3-codex-spark", ModelDefaults(), ModelRules(value)))
}

// requireMappedModel fails the test when one provider mapping differs from the
// expected target.
func requireMappedModel(t *testing.T, provider *acct.Record, requested, expected string) {
	t.Helper()
	if actual, _ := acct.ResolveMappedModel(acct.ResolveModelMapping(provider, ModelDefaults()), requested); actual != expected {
		t.Fatalf("GetMappedModel(%q) = %q, want %q", requested, actual, expected)
	}
}

// newOpenAIOAuthProviderForModelTest creates a fresh OpenAI OAuth record for
// admission assertions.
func newOpenAIOAuthProviderForModelTest() *acct.Record {
	return &acct.Record{
		ID:       1,
		Platform: capability.PlatformOpenAI,
		Type:     capability.ProviderTypeOAuth,
	}
}

// attachQoderModelPolicyFixture supplies a current account observation for policy
// tests. It leaves other platforms untouched and keeps CN-only eligibility
// separate from the international tier used by explicit mapping cases.
func attachQoderModelPolicyFixture(value *acct.Record) {
	if value.Platform != capability.PlatformQoder {
		return
	}
	value.Type = capability.ProviderTypeCosy
	models := []qodermodel.Model{{Key: "auto", Alias: "auto", Enabled: true}, {Key: "qmodel_38max", Alias: "qwen3.8-max", Enabled: true}, {Key: "gmodel", Alias: "glm-5.3", Enabled: true}}
	if value.GetCredential("site") != "cn" {
		models = append(models, qodermodel.Model{Key: "ultimate", Alias: "ultimate", Enabled: true})
	}
	value.Extra = map[string]any{acct.QoderCatalogExtraKey: &acct.QoderCatalogSnapshot{Identity: acct.QoderCatalogIdentity(value), Version: "fixture", FetchedAt: time.Now(), Models: models}}
}
