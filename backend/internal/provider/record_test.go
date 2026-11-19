package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// record_test.go verifies core provider identity, load-factor precedence,
// credential integer parsing, shadow linkage, and quota-gated schedulability.

// TestHasGeminiThirdPartyBaseURL compares a custom host, a missing URL, and
// Google's official host under the third-party marker. Only the parsed custom
// endpoint qualifies for the accessor.
func TestHasGeminiThirdPartyBaseURL(t *testing.T) {
	tests := []struct {
		name     string // test case display name
		provider Record // provider configuration under test
		expected bool   // expected accessor result
	}{
		{
			name: "custom Gemini-compatible endpoint",
			provider: Record{
				Platform: capability.PlatformGemini,
				Type:     capability.ProviderTypeAPIKey,
				Credentials: map[string]any{
					GeminiProviderTypeCredentialKey: GeminiProviderTypeThirdParty,
					"base_url":                      "https://provider.example.test/v1beta",
				},
			},
			expected: true,
		},
		{
			name: "missing base URL",
			provider: Record{
				Platform: capability.PlatformGemini,
				Type:     capability.ProviderTypeAPIKey,
				Credentials: map[string]any{
					GeminiProviderTypeCredentialKey: GeminiProviderTypeThirdParty,
				},
			},
			expected: false,
		},
		{
			name: "official Gemini endpoint",
			provider: Record{
				Platform: capability.PlatformGemini,
				Type:     capability.ProviderTypeAPIKey,
				Credentials: map[string]any{
					GeminiProviderTypeCredentialKey: GeminiProviderTypeThirdParty,
					"base_url":                      "https://generativelanguage.googleapis.com/v1beta",
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.provider.HasGeminiThirdPartyBaseURL())
		})
	}
}

// TestGetOpenAIProtocolAPIKey_KimiZhipuDeepSeek reads keys from eligible Kimi,
// Zhipu, and DeepSeek and OpenAI API Key records. A Kimi, Zhipu, and DeepSeek
// OAuth record returns empty, and Kimi, Zhipu, and DeepSeek classification
// remains separate from the OpenAI-specific type check.
func TestGetOpenAIProtocolAPIKey_KimiZhipuDeepSeek(t *testing.T) {
	t.Parallel()

	kimi := &Record{
		Platform:    capability.PlatformKimi,
		Type:        capability.ProviderTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-kimi"},
	}
	require.Equal(t, "sk-kimi", kimi.GetOpenAIProtocolAPIKey())
	require.False(t, kimi.IsOpenAIApiKey(), "IsOpenAIApiKey stays openai-only for scheduling gates")

	// A Chinese provider with a non-API-Key type must not expose its stored key.
	notAPIKey := &Record{
		Platform:    capability.PlatformDeepseek,
		Type:        capability.ProviderTypeOAuth,
		Credentials: map[string]any{"api_key": "sk-leak"},
	}
	require.Equal(t, "", notAPIKey.GetOpenAIProtocolAPIKey())

	// OpenAI uses its platform-specific API key accessor.
	openai := &Record{
		Platform:    capability.PlatformOpenAI,
		Type:        capability.ProviderTypeAPIKey,
		Credentials: map[string]any{"api_key": "sk-openai"},
	}
	require.Equal(t, "sk-openai", openai.GetOpenAIProtocolAPIKey())
}

// intPtrHelper allocates an integer pointer for load-factor configuration
// fixtures.
func intPtrHelper(v int) *int { return &v }

// TestEffectiveLoadFactor_NilProvider calls the accessor on an absent record.
// The result is one, giving consumers a positive divisor when no provider
// configuration is available.
func TestEffectiveLoadFactor_NilProvider(t *testing.T) {
	var a *Record
	require.Equal(t, 1, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_NilLoadFactor_PositiveConcurrency omits a separate
// load factor on a record with five concurrency slots. The accessor uses that
// positive concurrency as its divisor.
func TestEffectiveLoadFactor_NilLoadFactor_PositiveConcurrency(t *testing.T) {
	a := &Record{Concurrency: 5}
	require.Equal(t, 5, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_NilLoadFactor_ZeroConcurrency omits a separate load
// factor and sets unlimited concurrency. The accessor returns one instead of
// passing a zero divisor to load scoring.
func TestEffectiveLoadFactor_NilLoadFactor_ZeroConcurrency(t *testing.T) {
	a := &Record{Concurrency: 0}
	require.Equal(t, 1, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_PositiveLoadFactor sets a positive load factor larger
// than the record's concurrency. The accessor returns the configured factor,
// protecting its priority over inherited concurrency.
func TestEffectiveLoadFactor_PositiveLoadFactor(t *testing.T) {
	a := &Record{Concurrency: 5, LoadFactor: intPtrHelper(20)}
	require.Equal(t, 20, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_ZeroLoadFactor_FallbackToConcurrency supplies a zero
// factor and positive concurrency. The result uses concurrency because the
// factor needs to be positive before it can take priority.
func TestEffectiveLoadFactor_ZeroLoadFactor_FallbackToConcurrency(t *testing.T) {
	a := &Record{Concurrency: 5, LoadFactor: intPtrHelper(0)}
	require.Equal(t, 5, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_NegativeLoadFactor_FallbackToConcurrency supplies a
// negative factor with positive concurrency. The invalid divisor is ignored and
// the positive concurrency supplies the effective load factor.
func TestEffectiveLoadFactor_NegativeLoadFactor_FallbackToConcurrency(t *testing.T) {
	a := &Record{Concurrency: 3, LoadFactor: intPtrHelper(-1)}
	require.Equal(t, 3, a.EffectiveLoadFactor())
}

// TestEffectiveLoadFactor_ZeroLoadFactor_ZeroConcurrency supplies zero for both
// factor and concurrency. The accessor returns one, protecting the final
// positive divisor when neither configured input applies.
func TestEffectiveLoadFactor_ZeroLoadFactor_ZeroConcurrency(t *testing.T) {
	a := &Record{Concurrency: 0, LoadFactor: intPtrHelper(0)}
	require.Equal(t, 1, a.EffectiveLoadFactor())
}

// TestProviderIsSchedulable_QuotaExceeded supplies total and current-period
// quota exhaustion alongside an expired daily period. API Key and Bedrock
// records apply these limits, while OAuth records keep this generic check
// outside admission.
func TestProviderIsSchedulable_QuotaExceeded(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		want     bool    // expected accessor result
	}{
		{
			name: "apikey daily quota exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey weekly quota exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeAPIKey,
				Extra: map[string]any{
					"quota_weekly_limit": 50.0,
					"quota_weekly_used":  50.0,
					"quota_weekly_start": now.Add(-2 * 24 * time.Hour).Format(time.RFC3339),
				},
			},
			want: false,
		},
		{
			name: "apikey total quota exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeAPIKey,
				Extra: map[string]any{
					"quota_limit": 100.0,
					"quota_used":  100.0,
				},
			},
			want: false,
		},
		{
			name: "apikey quota not exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  5.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "apikey expired daily period restores schedulable",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeAPIKey,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-25 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "oauth ignores quota exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeOAuth,
				Extra: map[string]any{
					"quota_daily_limit": 10.0,
					"quota_daily_used":  10.0,
					"quota_daily_start": now.Add(-1 * time.Hour).Format(time.RFC3339),
				},
			},
			want: true,
		},
		{
			name: "bedrock quota exceeded",
			provider: &Record{
				LoadLocation: time.LoadLocation,
				Status:       StatusActive,
				Schedulable:  true,
				Type:         capability.ProviderTypeBedrock,
				Extra: map[string]any{
					"quota_limit": 200.0,
					"quota_used":  200.0,
				},
			},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.provider.IsSchedulable())
		})
	}
}

// TestProviderSparkShadowHelpers compares an ordinary provider with a
// parent-linked Spark record. Shadow and credential-shadow flags follow the
// parent link, while quota dimension defaults to global or retains Spark.
func TestProviderSparkShadowHelpers(t *testing.T) {
	pid := int64(100)
	normal := &Record{ID: 100}
	require.False(t, normal.IsShadow())
	require.False(t, normal.IsCredentialShadow())
	require.Equal(t, QuotaDimensionGlobal, normal.QuotaDimensionOrDefault())
	shadow := &Record{ID: 200, ParentProviderID: &pid, QuotaDimension: QuotaDimensionSpark}
	require.True(t, shadow.IsShadow())
	require.True(t, shadow.IsCredentialShadow())
	require.Equal(t, QuotaDimensionSpark, shadow.QuotaDimensionOrDefault())
}

// TestProvider_GetCredentialAsInt64 supplies supported numeric forms, trimmed
// string values, and missing or unusable credential entries. Parsing retains
// the selected integer metadata and returns zero for the unavailable cases.
func TestProvider_GetCredentialAsInt64(t *testing.T) {
	tests := []struct {
		name        string         // test case display name
		credentials map[string]any // input provider credential object
		key         string         // credential field under test
		expected    int64          // expected accessor result
	}{
		{
			name:        "int64_value",
			credentials: map[string]any{"_token_version": int64(1737654321000)},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "float64_value",
			credentials: map[string]any{"_token_version": float64(1737654321000)},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "int_value",
			credentials: map[string]any{"_token_version": 12345},
			key:         "_token_version",
			expected:    12345,
		},
		{
			name:        "string_value",
			credentials: map[string]any{"_token_version": "1737654321000"},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "string_with_spaces",
			credentials: map[string]any{"_token_version": "  1737654321000  "},
			key:         "_token_version",
			expected:    1737654321000,
		},
		{
			name:        "nil_credentials",
			credentials: nil,
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "missing_key",
			credentials: map[string]any{"other_key": 123},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "nil_value",
			credentials: map[string]any{"_token_version": nil},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "invalid_string",
			credentials: map[string]any{"_token_version": "not_a_number"},
			key:         "_token_version",
			expected:    0,
		},
		{
			name:        "empty_string",
			credentials: map[string]any{"_token_version": ""},
			key:         "_token_version",
			expected:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &Record{Credentials: tt.credentials}
			result := provider.GetCredentialAsInt64(tt.key)
			require.Equal(t, tt.expected, result)
		})
	}
}

// TestProvider_GetCredentialAsInt64_NilProvider calls the token-version
// accessor on an absent record. The returned zero lets callers treat that
// unavailable version as missing metadata.
func TestProvider_GetCredentialAsInt64_NilProvider(t *testing.T) {
	var provider *Record
	result := provider.GetCredentialAsInt64("_token_version")
	require.Equal(t, int64(0), result)
}
