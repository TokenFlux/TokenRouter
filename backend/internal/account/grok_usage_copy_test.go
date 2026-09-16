package account

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/account/usageview"
	"github.com/stretchr/testify/require"
)

// 实际 Grok 编排不得改写共享供应商结果，返回值的统计与嵌套账单也必须独立。
func TestGrokUsageOwnsProviderAndProbeResults(t *testing.T) {
	billing := &usageview.BillingSummary{Plan: "SuperGrok"}
	shared := &UsageInfo{SevenDay: &UsageProgress{}, GrokBilling: billing}
	probe := &GrokUsageProbe{Billing: billing, LocalUsage7d: &WindowStats{Requests: 12}}
	core := NewOAuthUsageService(nil, nil, nil, OAuthUsageOptions{Grok: GrokUsageOptions{
		Available:      func() bool { return true },
		StatsAvailable: func() bool { return false },
		Probe:          func(context.Context, int64) (*GrokUsageProbe, error) { return probe, nil },
		Build:          func(*Record) *UsageInfo { return shared },
		Enrich:         func(*UsageInfo, *Record) {},
	}})
	record := &Record{
		ID:          5,
		Platform:    PlatformGrok,
		Type:        AccountTypeOAuth,
		Credentials: map[string]any{"refresh_token": "fixture", "access_token": "fixture", "auth_mode": "oauth"},
	}
	require.True(t, record.IsGrokOAuth())
	first, err := core.GetGrokUsage(context.Background(), record, true)
	require.NoError(t, err)
	require.NotNil(t, first.GrokLocalUsage7d)
	first.GrokLocalUsage7d.Requests = 999
	first.GrokBilling.Plan = "caller"
	require.Nil(t, shared.SevenDay.WindowStats)
	require.Empty(t, shared.GrokQuotaSnapshotState)
	require.Equal(t, int64(12), probe.LocalUsage7d.Requests)
	require.Equal(t, "SuperGrok", billing.Plan)
	second, err := core.GetGrokUsage(context.Background(), record, true)
	require.NoError(t, err)
	require.Equal(t, int64(12), second.GrokLocalUsage7d.Requests)
	require.Equal(t, "SuperGrok", second.GrokBilling.Plan)
}
