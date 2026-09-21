//go:build unit

package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestBuildQuotaDims_AllDimensionsReturned(t *testing.T) {
	// Use an account with quota notify config across all 3 dimensions.
	a := &account.Record{
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeAPIKey,
		Extra: map[string]any{
			"quota_notify_daily_enabled":         true,
			"quota_notify_daily_threshold":       100.0,
			"quota_notify_daily_threshold_type":  "fixed",
			"quota_notify_weekly_enabled":        true,
			"quota_notify_weekly_threshold":      20.0,
			"quota_notify_weekly_threshold_type": "percentage",
			"quota_notify_total_enabled":         false,
			"quota_daily_limit":                  500.0,
			"quota_weekly_limit":                 2000.0,
			"quota_limit":                        10000.0,
			"quota_daily_used":                   50.0,
			"quota_weekly_used":                  300.0,
			"quota_used":                         1000.0,
		},
	}

	dims := QuotaNotification(a).Dimensions
	require.Len(t, dims, 3)

	// Daily
	require.Equal(t, "daily", dims[0].Name)
	require.True(t, dims[0].Enabled)
	require.Equal(t, 100.0, dims[0].Threshold)
	require.Equal(t, "fixed", dims[0].ThresholdType)
	require.Equal(t, 500.0, dims[0].Limit)
	require.Equal(t, 50.0, dims[0].CurrentUsed)

	// Weekly
	require.Equal(t, "weekly", dims[1].Name)
	require.True(t, dims[1].Enabled)
	require.Equal(t, 20.0, dims[1].Threshold)
	require.Equal(t, "percentage", dims[1].ThresholdType)
	require.Equal(t, 2000.0, dims[1].Limit)

	// Total
	require.Equal(t, "total", dims[2].Name)
	require.False(t, dims[2].Enabled)
	require.Equal(t, 10000.0, dims[2].Limit)
	require.Equal(t, 1000.0, dims[2].CurrentUsed)
}

func TestBuildQuotaDims_EmptyExtra(t *testing.T) {
	// Missing fields default to zero/disabled.
	a := &account.Record{
		Platform: capability.PlatformAnthropic,
		Type:     capability.AccountTypeAPIKey,
		Extra:    map[string]any{},
	}
	dims := QuotaNotification(a).Dimensions
	require.Len(t, dims, 3)
	for _, d := range dims {
		require.False(t, d.Enabled)
		require.Equal(t, 0.0, d.Threshold)
		require.Equal(t, 0.0, d.Limit)
	}
}
