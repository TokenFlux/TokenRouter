package provider

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// record_pool_retry_test.go verifies pool-mode retry count clamping and
// retryable HTTP status override semantics.

// TestGetPoolModeRetryCount exercises missing settings, supported numeric and
// string values, and values outside the allowed range. The accessor applies its
// default and clamps negative or oversized configured counts.
func TestGetPoolModeRetryCount(t *testing.T) {
	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		expected int     // expected accessor result
	}{
		{
			name: "default_when_not_pool_mode",
			provider: &Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformOpenAI,
				Credentials: map[string]any{},
			},
			expected: DefaultPoolModeRetryCount,
		},
		{
			name: "default_when_missing_retry_count",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode": true,
				},
			},
			expected: DefaultPoolModeRetryCount,
		},
		{
			name: "supports_float64_from_json_credentials",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": float64(5),
				},
			},
			expected: 5,
		},
		{
			name: "supports_json_number",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": json.Number("4"),
				},
			},
			expected: 4,
		},
		{
			name: "supports_string_value",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": "2",
				},
			},
			expected: 2,
		},
		{
			name: "negative_value_is_clamped_to_zero",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": -1,
				},
			},
			expected: 0,
		},
		{
			name: "oversized_value_is_clamped_to_max",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": 99,
				},
			},
			expected: MaxPoolModeRetryCount,
		},
		{
			name: "invalid_value_falls_back_to_default",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"pool_mode":             true,
					"pool_mode_retry_count": "oops",
				},
			},
			expected: DefaultPoolModeRetryCount,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.provider.GetPoolModeRetryCount())
		})
	}
}

// TestGetPoolModeRetryStatusCodes supplies missing, empty, mixed numeric/text,
// repeated, and out-of-range overrides. It checks nil inheritance, explicit
// empty selection, sorted deduplication, and filtering of unusable elements.
func TestGetPoolModeRetryStatusCodes(t *testing.T) {
	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		expected []int   // expected accessor result
	}{
		{name: "nil_provider_returns_nil", provider: nil, expected: nil},
		{
			name: "nil_credentials_returns_nil",
			provider: &Record{
				Type:     capability.ProviderTypeAPIKey,
				Platform: capability.PlatformOpenAI,
			},
			expected: nil,
		},
		{
			name: "missing_key_returns_nil",
			provider: &Record{
				Type:        capability.ProviderTypeAPIKey,
				Platform:    capability.PlatformOpenAI,
				Credentials: map[string]any{"pool_mode": true},
			},
			expected: nil,
		},
		{
			name: "empty_slice_is_preserved",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{}},
			},
			expected: []int{},
		},
		{
			name: "float64_values_from_json_are_normalized",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{float64(429), float64(401), float64(403)}},
			},
			expected: []int{401, 403, 429},
		},
		{
			name: "json_number_values_supported",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{json.Number("502"), json.Number("503")}},
			},
			expected: []int{502, 503},
		},
		{
			name: "string_values_supported",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{"520", "529"}},
			},
			expected: []int{520, 529},
		},
		{
			name: "duplicates_are_deduped",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{float64(429), float64(429), float64(401)}},
			},
			expected: []int{401, 429},
		},
		{
			name: "out_of_range_values_dropped",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{float64(99), float64(600), float64(429)}},
			},
			expected: []int{429},
		},
		{
			name: "invalid_string_dropped",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{"oops", float64(429)}},
			},
			expected: []int{429},
		},
		{
			name: "non_array_value_returns_nil",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": "not-an-array"},
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.provider.GetPoolModeRetryStatusCodes())
		})
	}
}

// TestIsPoolModeRetryableStatus_Provider compares default inheritance,
// configured replacement, and an explicit empty override. Unconfigured
// providers match 502 and 503, while configured lists control membership even
// when they omit a default status or contain no entries.
func TestIsPoolModeRetryableStatus_Provider(t *testing.T) {
	tests := []struct {
		name       string  // test case display name
		provider   *Record // provider configuration under test
		statusCode int     // upstream response status code
		expected   bool    // expected accessor result
	}{
		{name: "nil_provider_falls_back_to_default_401", provider: nil, statusCode: 401, expected: true},
		{name: "nil_provider_falls_back_to_default_500", provider: nil, statusCode: 500, expected: false},
		{
			name:       "unconfigured_uses_default_403",
			provider:   &Record{Credentials: map[string]any{"pool_mode": true}},
			statusCode: 403,
			expected:   true,
		},
		{
			name:       "unconfigured_uses_default_502",
			provider:   &Record{Credentials: map[string]any{"pool_mode": true}},
			statusCode: 502,
			expected:   true,
		},
		{
			name:       "unconfigured_uses_default_503",
			provider:   &Record{Credentials: map[string]any{"pool_mode": true}},
			statusCode: 503,
			expected:   true,
		},
		{
			name: "configured_list_overrides_default_401_dropped",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{float64(502), float64(503)}},
			},
			statusCode: 401,
			expected:   false,
		},
		{
			name: "configured_list_adds_520",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{float64(520)}},
			},
			statusCode: 520,
			expected:   true,
		},
		{
			name: "empty_list_disables_all_default_codes",
			provider: &Record{
				Credentials: map[string]any{"pool_mode_retry_status_codes": []any{}},
			},
			statusCode: 503,
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, tt.provider.IsPoolModeRetryableStatus(tt.statusCode))
		})
	}
}
