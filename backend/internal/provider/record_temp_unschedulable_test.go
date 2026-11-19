package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// record_temp_unschedulable_test.go verifies temporary suspension deadline
// admission and rule parsing from provider Credentials.

// TestProviderIsSchedulable_TempUnschedulable varies temporary suspension
// deadlines around real time. A future deadline blocks admission even after a
// separate rate limit expires, while elapsed and missing deadlines permit the
// remaining checks.
func TestProviderIsSchedulable_TempUnschedulable(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	past := time.Now().Add(-10 * time.Minute)

	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		want     bool    // expected accessor result
	}{
		{
			name: "temp_unschedulable_active",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: &future,
			},
			want: false,
		},
		{
			name: "temp_unschedulable_expired",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: &past,
			},
			want: true,
		},
		{
			name: "no_temp_unschedulable",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: nil,
			},
			want: true,
		},
		{
			name: "temp_unschedulable_with_rate_limit",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: &future,
				RateLimitResetAt:       &past,
			},
			want: false, // The temporary block still applies after the rate limit expires.
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.provider.IsSchedulable())
		})
	}
}

// TestProvider_IsTempUnschedulableEnabled checks true, false, missing, and
// absent-map suspension switches. Only the stored true boolean enables the rule
// feature, keeping incomplete configuration on its disabled path.
func TestProvider_IsTempUnschedulableEnabled(t *testing.T) {
	tests := []struct {
		name     string  // test case display name
		provider *Record // provider configuration under test
		want     bool    // expected accessor result
	}{
		{
			name: "enabled",
			provider: &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"temp_unschedulable_enabled": true,
			}},
			want: true,
		},
		{
			name: "disabled",
			provider: &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"temp_unschedulable_enabled": false,
			}},
			want: false,
		},
		{
			name:     "not_set",
			provider: &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			want:     false,
		},
		{
			name:     "nil_credentials",
			provider: &Record{LoadLocation: time.LoadLocation},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, tt.provider.IsTempUnschedulableEnabled())
		})
	}
}

// TestProvider_GetTempUnschedulableRules reads a valid JSON-shaped rule list
// and three absent or empty forms. The accepted rule count matches the
// populated input while every absent or empty case returns no rules.
func TestProvider_GetTempUnschedulableRules(t *testing.T) {
	tests := []struct {
		name      string  // test case display name
		provider  *Record // provider configuration under test
		wantCount int     // expected accepted rule count
	}{
		{
			name: "has_rules",
			provider: &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"temp_unschedulable_rules": []any{
					map[string]any{
						"error_code":       float64(503),
						"keywords":         []any{"overloaded"},
						"duration_minutes": float64(5),
					},
					map[string]any{
						"error_code":       float64(500),
						"keywords":         []any{"internal"},
						"duration_minutes": float64(10),
					},
				},
			}},
			wantCount: 2,
		},
		{
			name: "empty_rules",
			provider: &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
				"temp_unschedulable_rules": []any{},
			}},
			wantCount: 0,
		},
		{
			name:      "no_rules",
			provider:  &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{}},
			wantCount: 0,
		},
		{
			name:      "nil_credentials",
			provider:  &Record{LoadLocation: time.LoadLocation},
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rules := tt.provider.GetTempUnschedulableRules()
			require.Len(t, rules, tt.wantCount)
		})
	}
}

// TestTempUnschedulableRule_Parse reads one JSON-shaped rule containing a
// numeric status and duration plus two keywords. The parsed result retains the
// status, both keyword strings, and the minute duration.
func TestTempUnschedulableRule_Parse(t *testing.T) {
	provider := &Record{LoadLocation: time.LoadLocation, Credentials: map[string]any{
		"temp_unschedulable_rules": []any{
			map[string]any{
				"error_code":       float64(503),
				"keywords":         []any{"overloaded", "capacity"},
				"duration_minutes": float64(5),
			},
		},
	}}

	rules := provider.GetTempUnschedulableRules()
	require.Len(t, rules, 1)

	rule := rules[0]
	require.Equal(t, 503, rule.ErrorCode)
	require.Equal(t, []string{"overloaded", "capacity"}, rule.Keywords)
	require.Equal(t, 5, rule.DurationMinutes)
}

// TestProvider_TempUnschedulableUntil compares future, elapsed, and absent
// temporary block timestamps on active records. A future block rejects
// scheduling, while the elapsed or absent block leaves admission available.
func TestProvider_TempUnschedulableUntil(t *testing.T) {
	future := time.Now().Add(10 * time.Minute)
	past := time.Now().Add(-10 * time.Minute)

	tests := []struct {
		name        string  // test case display name
		provider    *Record // provider configuration under test
		schedulable bool    // expected provider admission result
	}{
		{
			name: "active_temp_unsched_not_schedulable",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: &future,
			},
			schedulable: false,
		},
		{
			name: "expired_temp_unsched_is_schedulable",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: &past,
			},
			schedulable: true,
		},
		{
			name: "nil_temp_unsched_is_schedulable",
			provider: &Record{
				LoadLocation:           time.LoadLocation,
				Status:                 StatusActive,
				Schedulable:            true,
				TempUnschedulableUntil: nil,
			},
			schedulable: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.schedulable, tt.provider.IsSchedulable())
		})
	}
}
