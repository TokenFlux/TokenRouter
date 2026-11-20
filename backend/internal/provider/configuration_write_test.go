package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A configuration edit can carry older usage totals or redacted credentials
// than the locked current provider record. These tests supply both snapshots
// and inspect the merge result, covering captured-time window reconciliation,
// sensitive-key intent, temporary-secret removal, and supported output
// isolation.

// TestConfigurationUsesCurrentWindowAndIsolatedValues merges quota
// configuration against current usage in active and expired daily windows. It
// checks retained total consumption, appropriate daily rollover, and selected
// output mutations leaving both inputs intact.
func TestConfigurationUsesCurrentWindowAndIsolatedValues(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	for _, currentDay := range []bool{false, true} {
		t.Run(map[bool]string{false: "previous_window", true: "current_window"}[currentDay], func(t *testing.T) {
			start := now.Add(-24 * time.Hour)
			if currentDay {
				start = now.Add(-time.Hour)
			}
			current := &Record{
				ID: 1, Platform: PlatformOpenAI, Type: ProviderTypeAPIKey, LoadLocation: time.LoadLocation,
				Credentials: map[string]any{"api_key": "latest-secret"},
				Extra:       map[string]any{"quota_used": 7.0, "quota_daily_used": 5.0, "quota_daily_start": start.Format(time.RFC3339)},
			}
			note := "new-note"
			desired := CloneRecord(current)
			desired.Notes = &note
			desired.Extra = map[string]any{"quota_daily_limit": 10.0, "quota_daily_reset_mode": "fixed", "quota_daily_reset_hour": 0.0}
			out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigNotes | ConfigExtra, ComputeResetAt: &now, NormalizeWindowAt: &now})
			require.NoError(t, err)
			require.Equal(t, 7.0, out.Extra["quota_used"])
			if currentDay {
				require.Equal(t, 5.0, out.Extra["quota_daily_used"])
			} else {
				require.Equal(t, 0.0, out.Extra["quota_daily_used"])
			}
			*out.Notes = "changed-copy"
			out.Credentials["api_key"] = "changed-copy"
			require.Equal(t, "new-note", note)
			require.Equal(t, "latest-secret", current.Credentials["api_key"])
			require.Equal(t, 5.0, current.Extra["quota_daily_used"])
		})
	}
}

// TestConfigurationSensitiveCredentialIntent submits an omitted, cleared, or
// replaced API Key against a newer stored secret. The merge follows submitted
// key presence and leaves the current record's secret unchanged.
func TestConfigurationSensitiveCredentialIntent(t *testing.T) {
	for _, mode := range []string{"omitted", "clear", "rotate"} {
		t.Run(mode, func(t *testing.T) {
			current := &Record{Credentials: map[string]any{"api_key": "latest-secret"}}
			desired := &Record{Credentials: map[string]any{"api_key": "stale-secret", "model_mapping": map[string]any{"alias": "model"}}}
			input := map[string]any{}
			expected := "latest-secret"
			switch mode {
			case "clear":
				expected = ""
				input["api_key"] = expected
				desired.Credentials["api_key"] = expected
			case "rotate":
				expected = "explicit-secret"
				input["api_key"] = expected
				desired.Credentials["api_key"] = expected
			}
			out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigCredentials, PreserveSensitive: true, CredentialInput: input})
			require.NoError(t, err)
			require.Equal(t, expected, out.Credentials["api_key"])
			require.Equal(t, "latest-secret", current.Credentials["api_key"])
		})
	}
}

// TestConfigurationDoesNotRestoreTemporaryCredentials starts with current Grok
// login material and an edit omitting secrets. The renewed object retains
// access_token while removing password, SSO, and cookie keys, leaving current
// input intact.
func TestConfigurationDoesNotRestoreTemporaryCredentials(t *testing.T) {
	current := &Record{Platform: PlatformGrok, Credentials: map[string]any{
		"access_token": "current-token", "password": "old-password", "sso_token": "old-sso", "cookie": "old-cookie",
	}}
	desired := &Record{Credentials: map[string]any{"model_mapping": map[string]any{"alias": "model"}}}
	out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigCredentials, PreserveSensitive: true})
	require.NoError(t, err)
	require.Equal(t, "current-token", out.Credentials["access_token"])
	for _, key := range []string{"password", "sso_token", "cookie"} {
		require.NotContains(t, out.Credentials, key)
		require.Contains(t, current.Credentials, key)
	}
}

// TestConfigurationRetainsCurrentMonthlyUsage submits stale monthly usage while
// editing its ceiling. The locked current counter and start survive replacement,
// so an administrative limit edit cannot restore a prior consumption value.
func TestConfigurationRetainsCurrentMonthlyUsage(t *testing.T) {
	now := time.Date(2026, 9, 13, 12, 0, 0, 0, time.UTC)
	current := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_monthly_used": 70.0, "quota_monthly_start": now.Format(time.RFC3339),
	}}
	desired := &Record{Extra: map[string]any{
		"quota_monthly_limit": 200.0, "quota_monthly_used": 5.0,
		"quota_monthly_start": now.Add(-time.Hour).Format(time.RFC3339),
	}}
	out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{Fields: ConfigExtra})
	require.NoError(t, err)
	require.Equal(t, 70.0, out.Extra["quota_monthly_used"])
	require.Equal(t, now.Format(time.RFC3339), out.Extra["quota_monthly_start"])
	require.Equal(t, 200.0, out.Extra["quota_monthly_limit"])
}

// TestConfigurationNormalizesPatchedEffortModelMapping submits a raw
// CredentialPatch containing uncanonical effort keys and whitespace. Merging
// must normalize effort_model_mapping before persistence and reject invalid
// effort values.
func TestConfigurationNormalizesPatchedEffortModelMapping(t *testing.T) {
	current := &Record{
		Platform:    PlatformAnthropic,
		Type:        ProviderTypeAPIKey,
		Credentials: map[string]any{"api_key": "secret"},
	}
	desired := CloneRecord(current)
	out, err := ApplyConfigurationChange(current, desired, ConfigurationChange{
		Fields: ConfigCredentials,
		CredentialPatch: map[string]any{
			EffortModelMappingCredentialKey: map[string]any{
				" claude-opus-5-5 ": map[string]any{
					"HIGH":       " claude-opus-5-5-high ",
					"extra_high": "claude-opus-5-5-xhigh",
				},
			},
			EffortModelMappingStripEffortCredentialKey: true,
		},
	})
	require.NoError(t, err)
	require.Equal(t, map[string]any{
		"claude-opus-5-5": map[string]any{
			"high":  "claude-opus-5-5-high",
			"xhigh": "claude-opus-5-5-xhigh",
		},
	}, out.Credentials[EffortModelMappingCredentialKey])
	require.Equal(t, true, out.Credentials[EffortModelMappingStripEffortCredentialKey])

	_, err = ApplyConfigurationChange(current, desired, ConfigurationChange{
		Fields: ConfigCredentials,
		CredentialPatch: map[string]any{
			EffortModelMappingCredentialKey: map[string]any{
				"claude-opus-5-5": map[string]any{"ultra": "claude-opus-5-5-ultra"},
			},
		},
	})
	require.Error(t, err)
}
