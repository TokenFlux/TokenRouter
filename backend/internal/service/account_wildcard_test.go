//go:build unit

package service

import (
	"reflect"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/domain"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
)

func TestGrokAccountModelMappingRemainsIndependentFromRuntimeSettings(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	account := &Account{Platform: PlatformGrok, Credentials: map[string]any{}}

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})
	requireMappedModel(t, account, "claude-sonnet-4-5", "claude-sonnet-4-5")

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
		DefaultText:          "grok-build-0.1",
		EnableCrossClientMap: true,
	})
	requireMappedModel(t, account, "claude-sonnet-4-5", "claude-sonnet-4-5")
}

func requireMappedModel(t *testing.T, account *Account, requested, expected string) {
	t.Helper()
	if actual := account.GetMappedModel(requested); actual != expected {
		t.Fatalf("GetMappedModel(%q) = %q, want %q", requested, actual, expected)
	}
}

func TestMatchWildcard(t *testing.T) {
	tests := []struct {
		name     string
		pattern  string
		str      string
		expected bool
	}{
		// 精确匹配
		{"exact match", "claude-sonnet-4-5", "claude-sonnet-4-5", true},
		{"exact mismatch", "claude-sonnet-4-5", "claude-opus-4-5", false},

		// 通配符匹配
		{"wildcard prefix match", "claude-*", "claude-sonnet-4-5", true},
		{"wildcard prefix match 2", "claude-*", "claude-opus-4-5-thinking", true},
		{"wildcard prefix mismatch", "claude-*", "gemini-3-flash", false},
		{"wildcard partial match", "gemini-3*", "gemini-3-flash", true},
		{"wildcard partial match 2", "gemini-3*", "gemini-3-pro-image", true},
		{"wildcard partial mismatch", "gemini-3*", "gemini-2.5-flash", false},

		// 边界情况
		{"empty pattern exact", "", "", true},
		{"empty pattern mismatch", "", "claude", false},
		{"single star", "*", "anything", true},
		{"star at end only", "abc*", "abcdef", true},
		{"star at end empty suffix", "abc*", "abc", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchWildcard(tt.pattern, tt.str)
			if result != tt.expected {
				t.Errorf("matchWildcard(%q, %q) = %v, want %v", tt.pattern, tt.str, result, tt.expected)
			}
		})
	}
}

func TestMatchWildcardMappingResult(t *testing.T) {
	tests := []struct {
		name           string
		mapping        map[string]string
		requestedModel string
		expected       string
		matched        bool
	}{
		// 精确匹配优先于通配符
		{
			name: "exact match takes precedence",
			mapping: map[string]string{
				"claude-sonnet-4-5": "claude-sonnet-4-5-exact",
				"claude-*":          "claude-default",
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-4-5-exact",
			matched:        true,
		},

		// 最长通配符优先
		{
			name: "longer wildcard takes precedence",
			mapping: map[string]string{
				"claude-*":         "claude-default",
				"claude-sonnet-*":  "claude-sonnet-default",
				"claude-sonnet-4*": "claude-sonnet-4-series",
			},
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-4-series",
			matched:        true,
		},

		// 单个通配符
		{
			name: "single wildcard",
			mapping: map[string]string{
				"claude-*": "claude-mapped",
			},
			requestedModel: "claude-opus-4-5",
			expected:       "claude-mapped",
			matched:        true,
		},

		// 无匹配返回原始模型
		{
			name: "no match returns original",
			mapping: map[string]string{
				"claude-*": "claude-mapped",
			},
			requestedModel: "gemini-3-flash",
			expected:       "gemini-3-flash",
			matched:        false,
		},

		// 空映射返回原始模型
		{
			name:           "empty mapping returns original",
			mapping:        map[string]string{},
			requestedModel: "claude-sonnet-4-5",
			expected:       "claude-sonnet-4-5",
			matched:        false,
		},

		// Gemini 模型映射
		{
			name: "gemini wildcard mapping",
			mapping: map[string]string{
				"gemini-3*":   "gemini-3-pro-high",
				"gemini-2.5*": "gemini-2.5-flash",
			},
			requestedModel: "gemini-3-flash-preview",
			expected:       "gemini-3-pro-high",
			matched:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, matched := matchWildcardMappingResult(tt.mapping, tt.requestedModel)
			if result != tt.expected || matched != tt.matched {
				t.Errorf("matchWildcardMappingResult(%v, %q) = (%q, %v), want (%q, %v)", tt.mapping, tt.requestedModel, result, matched, tt.expected, tt.matched)
			}
		})
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
			platform: PlatformGemini,
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
			platform:       PlatformQoder,
			credentials:    nil,
			requestedModel: "claude-opus-4-6",
			expected:       true,
		},
		{
			name:           "qoder mapping absent does not reject raw upstream key",
			platform:       PlatformQoder,
			credentials:    nil,
			requestedModel: "ultimate",
			expected:       true,
		},
		{
			name:           "qoder global accepts qwen 38 public alias",
			platform:       PlatformQoder,
			credentials:    map[string]any{"site": "global"},
			requestedModel: "qwen3.8-max",
			expected:       true,
		},
		{
			name:           "qoder global accepts qwen 38 raw route",
			platform:       PlatformQoder,
			credentials:    map[string]any{"site": "global"},
			requestedModel: "qmodel_38max",
			expected:       true,
		},
		{
			name:           "qoder cn accepts qwen 38 public alias",
			platform:       PlatformQoder,
			credentials:    map[string]any{"site": "cn"},
			requestedModel: "qwen3.8-max",
			expected:       true,
		},
		{
			name:           "qoder cn accepts qwen 38 raw route",
			platform:       PlatformQoder,
			credentials:    map[string]any{"site": "cn"},
			requestedModel: "qmodel_38max",
			expected:       true,
		},
		{
			name:     "qoder preview requires explicit mapping when route whitelist is used",
			platform: PlatformQoder,
			credentials: map[string]any{
				"site":            "cn",
				"model_whitelist": []any{"qmodel_38max"},
			},
			requestedModel: "qwen3.8-max-preview",
			expected:       false,
		},
		{
			name:     "qoder explicit preview mapping remains compatible",
			platform: PlatformQoder,
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
			platform: PlatformQoder,
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
			platform: PlatformQoder,
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
			platform: PlatformQoder,
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
			platform: PlatformQoder,
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
			account := &Account{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result := account.IsModelSupported(tt.requestedModel)
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
			platform: PlatformQoder,
			credentials: map[string]any{
				"model_mapping": map[string]any{"claude-opus-4-6": "ultimate"},
			},
			expected: []string{"claude-opus-4-6"},
		},
		{
			name:     "qoder explicit mapping returns mapping keys for model list display",
			platform: PlatformQoder,
			credentials: map[string]any{
				"model_mapping":   map[string]any{"claude-opus-4-6": "ultimate"},
				"model_whitelist": []any{"ultimate"},
			},
			expected: []string{"claude-opus-4-6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &Account{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result := account.GetConfiguredRequestModels()
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
			platform:       PlatformGemini,
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
			platform: PlatformGemini,
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
			platform: PlatformGemini,
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
			account := &Account{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			result := account.GetMappedModel(tt.requestedModel)
			if result != tt.expected {
				t.Errorf("GetMappedModel(%q) = %q, want %q", tt.requestedModel, result, tt.expected)
			}
		})
	}
}

func TestAccountGetModelMapping_AntigravityNormalizesGemini31ProAliases(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				domain.AntigravityGemini31ProAgentModel: domain.AntigravityGemini31ProAgentModel,
				"gemini-3.1-pro-high":                   "gemini-3.1-pro-high",
				"gemini-3.1-pro-preview":                "gemini-3.1-pro-high",
			},
		},
	}

	mapping := account.GetModelMapping()

	if got := mapping["gemini-3.1-pro"]; got != domain.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro to map to %q, got %q", domain.AntigravityGemini31ProAgentModel, got)
	}
	if got := mapping["gemini-3.1-pro-high"]; got != domain.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro-high to map to %q, got %q", domain.AntigravityGemini31ProAgentModel, got)
	}
	if got := mapping["gemini-3.1-pro-preview"]; got != domain.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro-preview to map to %q, got %q", domain.AntigravityGemini31ProAgentModel, got)
	}
}

func TestAccountGetModelMapping_AntigravityPreservesGemini31ProOverrides(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				domain.AntigravityGemini31ProAgentModel: domain.AntigravityGemini31ProAgentModel,
				"gemini-3.1-pro-high":                   "custom-high",
				"gemini-3.1-pro-preview":                "custom-preview",
			},
		},
	}

	mapping := account.GetModelMapping()

	if got := mapping["gemini-3.1-pro-high"]; got != "custom-high" {
		t.Fatalf("expected gemini-3.1-pro-high override to be preserved, got %q", got)
	}
	if got := mapping["gemini-3.1-pro-preview"]; got != "custom-preview" {
		t.Fatalf("expected gemini-3.1-pro-preview override to be preserved, got %q", got)
	}
	if got := mapping["gemini-3.1-pro"]; got != domain.AntigravityGemini31ProAgentModel {
		t.Fatalf("expected gemini-3.1-pro alias to default to %q, got %q", domain.AntigravityGemini31ProAgentModel, got)
	}
}

func TestAccountGetModelMapping_AntigravityGemini31ProAliasesRespectWildcard(t *testing.T) {
	t.Parallel()

	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				domain.AntigravityGemini31ProAgentModel: domain.AntigravityGemini31ProAgentModel,
				"gemini-3.1-*":                          "custom-wildcard",
			},
		},
	}

	mapping := account.GetModelMapping()

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
			platform: PlatformGemini,
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
			platform: PlatformGemini,
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
			account := &Account{
				Platform:    tt.platform,
				Credentials: tt.credentials,
			}
			mappedModel, matched := account.ResolveMappedModel(tt.requestedModel)
			if mappedModel != tt.expectedModel || matched != tt.expectedMatch {
				t.Fatalf("ResolveMappedModel(%q) = (%q, %v), want (%q, %v)", tt.requestedModel, mappedModel, matched, tt.expectedModel, tt.expectedMatch)
			}
		})
	}
}

func TestAccountGetModelMapping_AntigravityEnsuresGeminiDefaultPassthroughs(t *testing.T) {
	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-3-pro-high": "gemini-3.1-pro-high",
			},
		},
	}

	mapping := account.GetModelMapping()
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

func TestAccountGetModelMapping_AntigravityRespectsWildcardOverride(t *testing.T) {
	account := &Account{
		Platform: PlatformAntigravity,
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"gemini-3*": "gemini-3.1-pro-high",
			},
		},
	}

	mapping := account.GetModelMapping()
	if _, exists := mapping["gemini-3-flash"]; exists {
		t.Fatalf("did not expect explicit gemini-3-flash passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-high"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-high passthrough when wildcard already exists")
	}
	if _, exists := mapping["gemini-3.1-pro-low"]; exists {
		t.Fatalf("did not expect explicit gemini-3.1-pro-low passthrough when wildcard already exists")
	}
	if mapped := account.GetMappedModel("gemini-3-flash"); mapped != "gemini-3.1-pro-high" {
		t.Fatalf("expected wildcard mapping to stay effective, got: %q", mapped)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnCredentialsReplace(t *testing.T) {
	account := &Account{
		Credentials: map[string]any{
			"model_mapping": map[string]any{
				"claude-3-5-sonnet": "upstream-a",
			},
		},
	}

	first := account.GetModelMapping()
	if first["claude-3-5-sonnet"] != "upstream-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	account.Credentials = map[string]any{
		"model_mapping": map[string]any{
			"claude-3-5-sonnet": "upstream-b",
		},
	}
	second := account.GetModelMapping()
	if second["claude-3-5-sonnet"] != "upstream-b" {
		t.Fatalf("expected cache invalidated after credentials replace, got: %v", second)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnMappingLenChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	account := &Account{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := account.GetModelMapping()
	if len(first) != 1 {
		t.Fatalf("unexpected first mapping length: %d", len(first))
	}

	rawMapping["claude-opus"] = "opus-b"
	second := account.GetModelMapping()
	if second["claude-opus"] != "opus-b" {
		t.Fatalf("expected cache invalidated after mapping len change, got: %v", second)
	}
}

func TestAccountGetModelMapping_CacheInvalidatesOnInPlaceValueChange(t *testing.T) {
	rawMapping := map[string]any{
		"claude-sonnet": "sonnet-a",
	}
	account := &Account{
		Credentials: map[string]any{
			"model_mapping": rawMapping,
		},
	}

	first := account.GetModelMapping()
	if first["claude-sonnet"] != "sonnet-a" {
		t.Fatalf("unexpected first mapping: %v", first)
	}

	rawMapping["claude-sonnet"] = "sonnet-b"
	second := account.GetModelMapping()
	if second["claude-sonnet"] != "sonnet-b" {
		t.Fatalf("expected cache invalidated after in-place value change, got: %v", second)
	}
}
