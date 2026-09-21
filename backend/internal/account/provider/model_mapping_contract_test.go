//go:build unit

package provider

import (
	acct "github.com/TokenFlux/TokenRouter/internal/account"

	"reflect"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func TestGrokAccountModelMappingRemainsIndependentFromRuntimeSettings(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	account := &acct.Record{Platform: capability.PlatformGrok, Credentials: map[string]any{}}

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})
	requireMappedModel(t, account, "claude-sonnet-4-5", "claude-sonnet-4-5")

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
		DefaultText:          "grok-build-0.1",
		EnableCrossClientMap: true,
	})
	requireMappedModel(t, account, "claude-sonnet-4-5", "claude-sonnet-4-5")
}

func requireMappedModel(t *testing.T, account *acct.Record, requested, expected string) {
	t.Helper()
	if actual, _ := acct.ResolveMappedModel(account.Platform, acct.ResolveModelMapping(account, ModelDefaults()), requested); actual != expected {
		t.Fatalf("GetMappedModel(%q) = %q, want %q", requested, actual, expected)
	}
}

func TestAccountIsModelSupported(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		credentials    map[string]any
		requestedModel string
		expected       bool
	}{
		// 无映射 = 允许所有
		{
			name:           "no mapping allows all",
			credentials:    nil,
			requestedModel: "any-model",
			expected:       true,
		},
		{
			name:           "empty mapping allows all",
			credentials:    map[string]any{},
			requestedModel: "any-model",
			expected:       true,
		},

		// 精确匹配
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

		// 通配符匹配
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
			name:           "qoder mapping absent does not restrict public alias",
			platform:       capability.PlatformQoder,
			credentials:    nil,
			requestedModel: "claude-opus-4-6",
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
			requestedModel: "glm-5",
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
			expected:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result := account.IsModelSupported(tt.requestedModel, ModelDefaults(), ModelRules(account))
			if result != tt.expected {
				t.Errorf("IsModelSupported(%q) = %v, want %v", tt.requestedModel, result, tt.expected)
			}
		})
	}
}

func TestAccountGetConfiguredRequestModels(t *testing.T) {
	tests := []struct {
		name        string
		platform    string
		credentials map[string]any
		expected    []string
	}{
		{
			name: "mapping only returns nil because request space is unrestricted",
			credentials: map[string]any{
				"model_mapping": map[string]any{"model-a": "model-b"},
			},
			expected: nil,
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
			name: "explicit empty whitelist returns nil even with mapping",
			credentials: map[string]any{
				"model_mapping":   map[string]any{"model-a": "model-b"},
				"model_whitelist": []any{},
			},
			expected: nil,
		},
		{
			name:     "qoder mapping only returns mapping keys for model list display",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"},
			},
			expected: []string{"claude-opus-4-6"},
		},
		{
			name:     "qoder explicit mapping returns mapping keys for model list display",
			platform: capability.PlatformQoder,
			credentials: map[string]any{
				"model_mapping":   map[string]any{"claude-opus-4-6": "ultimate"},
				"model_whitelist": []any{"ultimate"},
			},
			expected: []string{"claude-opus-4-6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result := account.GetConfiguredRequestModels(ModelDefaults())
			if !reflect.DeepEqual(result, tt.expected) {
				t.Fatalf("GetConfiguredRequestModels() = %#v, want %#v", result, tt.expected)
			}
		})
	}
}

func TestAccountGetMappedModel(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		credentials    map[string]any
		requestedModel string
		expected       string
	}{
		// 无映射 = 返回原始模型
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

		// 精确匹配
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

		// 通配符匹配（最长优先）
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

		// 无匹配返回原始模型
		{
			name:     "gemini customtools alias resolves through normalized mapping",
			platform: capability.PlatformGemini,
			credentials: map[string]any{
				"model_mapping": map[string]any{
					"gemini-3.1-pro-preview": "gemini-3.1-pro-preview",
				},
			},
			requestedModel: "gemini-3.1-pro-preview-customtools",
			expected:       "gemini-3.1-pro-preview",
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
			account := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result, _ := acct.ResolveMappedModel(account.Platform, acct.ResolveModelMapping(account, ModelDefaults()), tt.requestedModel)
			if result != tt.expected {
				t.Errorf("GetMappedModel(%q) = %q, want %q", tt.requestedModel, result, tt.expected)
			}
		})
	}
}

func TestAccountGetModelMapping_AntigravityNormalizesGemini31ProAliases(t *testing.T) {
	t.Parallel()

	account := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				antigravity.AntigravityGemini31ProAgentModel: antigravity.AntigravityGemini31ProAgentModel,
				"gemini-3.1-pro-high":                        "gemini-3.1-pro-high",
				"gemini-3.1-pro-preview":                     "gemini-3.1-pro-high",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())

	if got := mapping["gemini-3.1-pro"]; got != antigravity.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro to map to %q, got %q", antigravity.AntigravityGemini31ProAgentModel, got)
	}
	if got := mapping["gemini-3.1-pro-high"]; got != antigravity.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro-high to map to %q, got %q", antigravity.AntigravityGemini31ProAgentModel, got)
	}
	if got := mapping["gemini-3.1-pro-preview"]; got != antigravity.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro-preview to map to %q, got %q", antigravity.AntigravityGemini31ProAgentModel, got)
	}
}

func TestAccountGetModelMapping_AntigravityPreservesGemini31ProOverrides(t *testing.T) {
	t.Parallel()

	account := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				antigravity.AntigravityGemini31ProAgentModel: antigravity.AntigravityGemini31ProAgentModel,
				"gemini-3.1-pro-high":                        "custom-high",
				"gemini-3.1-pro-preview":                     "custom-preview",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())

	if got := mapping["gemini-3.1-pro-high"]; got != "custom-high" {
		t.Fatalf("expected gemini-3.1-pro-high override to be preserved, got %q", got)
	}
	if got := mapping["gemini-3.1-pro-preview"]; got != "custom-preview" {
		t.Fatalf("expected gemini-3.1-pro-preview override to be preserved, got %q", got)
	}
	if got := mapping["gemini-3.1-pro"]; got != antigravity.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro alias to default to %q, got %q", antigravity.AntigravityGemini31ProAgentModel, got)
	}
}

func TestAccountGetModelMapping_AntigravityGemini31ProAliasesRespectWildcard(t *testing.T) {
	t.Parallel()

	account := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				antigravity.AntigravityGemini31ProAgentModel: antigravity.AntigravityGemini31ProAgentModel,
				"gemini-3.1-*": "custom-wildcard",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())

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

func TestAccountResolveMappedModel(t *testing.T) {
	tests := []struct {
		name           string
		platform       string
		credentials    map[string]any
		requestedModel string
		expectedModel  string
		expectedMatch  bool
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
			expectedModel:  "gemini-3.1-pro-preview",
			expectedMatch:  true,
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
			account := &acct.Record{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			mappedModel, matched := acct.ResolveMappedModel(account.Platform, acct.ResolveModelMapping(account, ModelDefaults()), tt.requestedModel)
			if mappedModel != tt.expectedModel || matched != tt.expectedMatch {
				t.Fatalf("ResolveMappedModel(%q) = (%q, %v), want (%q, %v)", tt.requestedModel, mappedModel, matched, tt.expectedModel, tt.expectedMatch)
			}
		})
	}
}

func TestAccountGetModelMapping_AntigravityEnsuresGeminiDefaultPassthroughs(t *testing.T) {
	account := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-3-pro-high": "gemini-3.1-pro-high",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())
	if mapping["gemini-3-flash"] != "gemini-3-flash" {
		t.Fatalf("expected gemini-3-flash passthrough to be auto-filled, got: %q", mapping["gemini-3-flash"])
	}
	if mapping["gemini-3.1-pro-high"] != "gemini-3.1-pro-high" {
		t.Fatalf("expected gemini-3.1-pro-high passthrough to be auto-filled, got: %q", mapping["gemini-3.1-pro-high"])
	}
	if mapping["gemini-3.1-pro-low"] != "gemini-3.1-pro-low" {
		t.Fatalf("expected gemini-3.1-pro-low passthrough to be auto-filled, got: %q", mapping["gemini-3.1-pro-low"])
	}
	// 自定义映射不能屏蔽新发布的 Gemini 3.6 Flash 直通模型。
	for _, model := range []string{"gemini-3.6-flash", "gemini-3.6-flash-high", "gemini-3.6-flash-low", "gemini-3.6-flash-medium", "gemini-3.6-flash-tiered"} {
		if mapping[model] != model {
			t.Fatalf("expected %s passthrough to be auto-filled, got: %q", model, mapping[model])
		}
	}
}

func TestAccountGetModelMapping_GoogleOneUsesConservativeDefaults(t *testing.T) {
	account := &acct.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"oauth_type": "google_one",
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())
	for _, model := range []string{"gemini-2.0-flash", "gemini-2.5-flash", "gemini-2.5-pro"} {
		if mapping[model] != model {
			t.Fatalf("expected Google One model %q to map to itself, got %q", model, mapping[model])
		}
	}
	for _, model := range []string{"gemini-2.5-flash-image", "gemini-3.1-flash-image", "gemini-3.5-flash"} {
		if _, ok := mapping[model]; ok {
			t.Fatalf("did not expect unsupported Google One model %q", model)
		}
	}
	if account.IsModelSupported("gemini-3.5-flash", ModelDefaults(), ModelRules(account)) {
		t.Fatal("Google One defaults must not treat unsupported models as eligible")
	}
}

func TestAccountGetModelMapping_GoogleOnePreservesExplicitMapping(t *testing.T) {
	account := &acct.Record{
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeOAuth,
		Credentials: map[string]any{
			"oauth_type": "google_one",
			"model_mapping": map[string]any{
				"custom-model": "gemini-2.5-flash",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())
	if mapping["custom-model"] != "gemini-2.5-flash" {
		t.Fatalf("expected explicit Google One mapping to be preserved, got %v", mapping)
	}
	if _, ok := mapping["gemini-2.5-flash"]; ok {
		t.Fatalf("did not expect defaults to overwrite an explicit mapping: %v", mapping)
	}
}

func TestAccountGetModelMapping_AntigravityRespectsWildcardOverride(t *testing.T) {
	account := &acct.Record{
		Platform: capability.PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-3*": "gemini-3.1-pro-high",
			},
		},
	}

	mapping := acct.ResolveModelMapping(account, ModelDefaults())
	if _, exists := mapping["gemini-3-flash"]; exists {
		t.Fatalf("did not expect explicit gemini-3-flash passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-high"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-high passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-low"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-low passthrough when wildcard already exists")
	}
	if mapped, _ := acct.ResolveMappedModel(account.Platform, acct.ResolveModelMapping(account, ModelDefaults()), "gemini-3-flash"); mapped != "gemini-3.1-pro-high" {
		t.Fatalf("expected wildcard mapping to stay effective, got: %q", mapped)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnCredentialsReplace(t *testing.T) {
	account := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-3-5-sonnet": "upstream-a",
			},
		},
	}

	first := acct.ResolveModelMapping(account, ModelDefaults())
	if first["claude-3-5-sonnet"] != "upstream-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	account.Credentials = map[string]any{
		"model_mapping": map[string]any{
			"claude-3-5-sonnet": "upstream-b",
		},
	}
	second := acct.ResolveModelMapping(account, ModelDefaults())
	if second["claude-3-5-sonnet"] != "upstream-b" {
		t.Fatalf("expected cache invalidated after credentials replace, got: %v", second)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnMappingLenChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	account := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := acct.ResolveModelMapping(account, ModelDefaults())
	if len(first) != 1 {
		t.Fatalf("unexpected first mapping length: %d", len(first))
	}

	rawMapping["claude-opus"] = "opus-b"
	second := acct.ResolveModelMapping(account, ModelDefaults())
	if second["claude-opus"] != "opus-b" {
		t.Fatalf("expected cache invalidated after mapping len change, got: %v", second)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnInPlaceValueChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	account := &acct.Record{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := acct.ResolveModelMapping(account, ModelDefaults())
	if first["claude-sonnet"] != "sonnet-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	rawMapping["claude-sonnet"] = "sonnet-b"
	second := acct.ResolveModelMapping(account, ModelDefaults())
	if second["claude-sonnet"] != "sonnet-b" {
		t.Fatalf("expected cache invalidated after in-place value change, got: %v", second)
	}
}
