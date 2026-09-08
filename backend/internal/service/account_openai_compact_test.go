package service

import "testing"

// 管理员配置覆盖历史探测结论，两种压缩能力相互独立。
func TestAccountCompactionControlledByAdministrator(t *testing.T) {
	for _, mode := range []string{"", "auto", "force_on", "force_off"} {
		for _, probed := range []bool{false, true} {
			a := &Account{Platform: PlatformOpenAI, Extra: map[string]any{"openai_compact_mode": mode, "openai_compact_supported": probed, openAINativeCompactionV2ModeExtraKey: mode, openAINativeCompactionV2SupportedExtraKey: probed}}
			want := mode != "force_off"
			if a.AllowsOpenAICompact() != want || a.AllowsOpenAINativeCompactionV2() != want {
				t.Fatalf("mode=%s probe=%v ignored administrator", mode, probed)
			}
			a.Extra[openAINativeCompactionV2ModeExtraKey] = "force_off"
			if a.AllowsOpenAINativeCompactionV2() || a.AllowsOpenAICompact() != want {
				t.Fatal("independent switches required")
			}
		}
	}
	for _, a := range []*Account{nil, {Platform: PlatformAnthropic}} {
		if a.AllowsOpenAICompact() || a.AllowsOpenAINativeCompactionV2() {
			t.Fatal("non OpenAI must be excluded")
		}
	}
}

func TestAccountGetCompactModelMapping(t *testing.T) {
	tests := []struct {
		name    string
		account *Account
		want    map[string]string
	}{
		{
			name: "nil account returns nil",
			want: nil,
		},
		{
			name: "missing credentials returns nil",
			account: &Account{
				Platform: PlatformOpenAI,
			},
			want: nil,
		},
		{
			name: "map any is converted",
			account: &Account{
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
			account: &Account{
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
			got := tt.account.GetCompactModelMapping()
			if !equalStringMap(got, tt.want) {
				t.Fatalf("GetCompactModelMapping() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAccountResolveCompactMappedModel(t *testing.T) {
	tests := []struct {
		name           string
		credentials    map[string]any
		requestedModel string
		expectedModel  string
		expectedMatch  bool
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
			account := &Account{
				Platform:    PlatformOpenAI,
				Credentials: tt.credentials,
			}
			gotModel, gotMatch := account.ResolveCompactMappedModel(tt.requestedModel)
			if gotModel != tt.expectedModel || gotMatch != tt.expectedMatch {
				t.Fatalf("ResolveCompactMappedModel(%q) = (%q, %v), want (%q, %v)", tt.requestedModel, gotModel, gotMatch, tt.expectedModel, tt.expectedMatch)
			}
		})
	}
}

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
