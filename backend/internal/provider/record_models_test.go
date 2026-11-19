package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// record_models_test.go verifies administrator compaction switches and
// compact-specific model mapping resolution.

// TestProviderCompactionControlledByAdministrator pairs each administrator mode
// with old probe observations and independently disables V2. The switches
// follow administrator selection, stay independent, and reject nil or
// non-OpenAI records.
func TestProviderCompactionControlledByAdministrator(t *testing.T) {
	for _, mode := range []string{"", "force_on", "force_off"} {
		for _, probed := range []bool{false, true} {
			a := &Record{Platform: capability.PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": mode, "openai_compact_supported": probed, OpenAINativeCompactionV2ModeExtraKey: mode, "openai_native_compaction_v2_supported": probed}}
			want := mode != "force_off"
			if a.AllowsOpenAICompact() != want || a.AllowsOpenAINativeCompactionV2() != want {
				t.Fatalf("mode=%s probe=%v ignored administrator", mode, probed)
			}
			a.Extra[OpenAINativeCompactionV2ModeExtraKey] = "force_off"
			if a.AllowsOpenAINativeCompactionV2() || a.AllowsOpenAICompact() != want {
				t.Fatal("independent switches required")
			}
		}
	}
	for _, a := range []*Record{nil, {Platform: capability.PlatformAnthropic}} {
		if a.AllowsOpenAICompact() || a.AllowsOpenAINativeCompactionV2() {
			t.Fatal("non OpenAI must be excluded")
		}
	}
}

// TestProviderGetCompactModelMapping reads missing, mixed-value, and typed
// compact mapping objects. Nil inputs remain absent, mixed entries retain
// string targets, and typed maps return the expected string entries.
func TestProviderGetCompactModelMapping(t *testing.T) {
	tests := []struct {
		name     string            // test case display name
		provider *Record           // provider configuration under test
		want     map[string]string // expected accessor result
	}{
		{
			name: "nil provider returns nil",
			want: nil,
		},
		{
			name: "missing credentials returns nil",
			provider: &Record{
				Platform: capability.PlatformOpenAI,
			},
			want: nil,
		},
		{
			name: "map any is converted",
			provider: &Record{
				Credentials: map[string]any{
					"compact_model_mapping": map[string]any{
						"gpt-5.4": "gpt-5.4-openai-compact",
						"invalid": 1,
					},
				},
			},
			want: map[string]string{
				"gpt-5.4": "gpt-5.4-openai-compact",
			},
		},
		{
			name: "map string string is copied",
			provider: &Record{
				Credentials: map[string]any{
					"compact_model_mapping": map[string]string{
						"gpt-*": "compact-*",
					},
				},
			},
			want: map[string]string{
				"gpt-*": "compact-*",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.provider.GetCompactModelMapping()
			if !equalStringMap(got, tt.want) {
				t.Fatalf("GetCompactModelMapping() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

// TestProviderResolveCompactMappedModel exercises absent rules, exact remaps,
// identity rules, and overlapping wildcard prefixes. Results retain unmatched
// input, count an identity rule as matched, and select the longest wildcard.
func TestProviderResolveCompactMappedModel(t *testing.T) {
	tests := []struct {
		name           string         // test case display name
		credentials    map[string]any // input provider credential object
		requestedModel string         // input requested model name
		expectedModel  string         // expected resolved model name
		expectedMatch  bool           // expected mapping presence flag
	}{
		{
			name:           "no compact mapping reports unmatched",
			credentials:    nil,
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  false,
		},
		{
			name: "exact compact mapping matches",
			credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.4": "gpt-5.4-openai-compact",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4-openai-compact",
			expectedMatch:  true,
		},
		{
			name: "exact passthrough counts as match",
			credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.4": "gpt-5.4",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  true,
		},
		{
			name: "longest wildcard wins",
			credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-*":         "fallback-compact",
					"gpt-5.4*":      "gpt-5.4-openai-compact",
					"gpt-5.4-mini*": "gpt-5.4-mini-openai-compact",
				},
			},
			requestedModel: "gpt-5.4-mini",
			expectedModel:  "gpt-5.4-mini-openai-compact",
			expectedMatch:  true,
		},
		{
			name: "missing compact mapping reports unmatched",
			credentials: map[string]any{
				"compact_model_mapping": map[string]any{
					"gpt-5.3": "gpt-5.3-openai-compact",
				},
			},
			requestedModel: "gpt-5.4",
			expectedModel:  "gpt-5.4",
			expectedMatch:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &Record{
				Platform:    capability.PlatformOpenAI,
				Credentials: tt.credentials,
			}
			gotModel, gotMatch := provider.ResolveCompactMappedModel(tt.requestedModel)
			if gotModel != tt.expectedModel || gotMatch != tt.expectedMatch {
				t.Fatalf("ResolveCompactMappedModel(%q) = (%q, %v), want (%q, %v)", tt.requestedModel, gotModel, gotMatch, tt.expectedModel, tt.expectedMatch)
			}
		})
	}
}

// equalStringMap compares mapping lengths, key presence, and every expected
// target value.
func equalStringMap(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for key, want := range right {
		if got, ok := left[key]; !ok || got != want {
			return false
		}
	}
	return true
}
