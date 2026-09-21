package usage_test

import (
	"context"
	"testing"

	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

type usageRankingSettingsRepoStub struct {
	settingscore.Repository
	values map[string]string
}

func (s *usageRankingSettingsRepoStub) GetMultiple(_ context.Context, keys []string) (map[string]string, error) {
	result := make(map[string]string, len(keys))
	for _, key := range keys {
		if value, ok := s.values[key]; ok {
			result[key] = value
		}
	}
	return result, nil
}

func TestGetUsageRankingSettingsUsesCompatibleDefaults(t *testing.T) {
	svc := usage.NewRuntimeSettings(&usageRankingSettingsRepoStub{values: map[string]string{}})

	settings, err := svc.GetUsageRankingSettings(context.Background())

	require.NoError(t, err)
	require.True(t, settings.Enabled)
	require.Equal(t, usage.UsageRankingSortByTotalTokens, settings.SortBy)
	require.True(t, settings.ShowTotalTokens)
	require.True(t, settings.ShowRequests)
	require.True(t, settings.ShowActualCost)
	require.Equal(t, usage.DefaultUsageRankingLimit, settings.Limit)
}

func TestGetUsageRankingSettingsForcesSortMetricVisible(t *testing.T) {
	svc := usage.NewRuntimeSettings(&usageRankingSettingsRepoStub{values: map[string]string{
		usage.SettingKeyUsageRankingSortBy:          string(usage.UsageRankingSortByActualCost),
		usage.SettingKeyUsageRankingShowTotalTokens: "false",
		usage.SettingKeyUsageRankingShowRequests:    "false",
		usage.SettingKeyUsageRankingShowActualCost:  "false",
		usage.SettingKeyUsageRankingLimit:           "999",
	}})

	settings, err := svc.GetUsageRankingSettings(context.Background())

	require.NoError(t, err)
	require.Equal(t, usage.UsageRankingSortByActualCost, settings.SortBy)
	require.False(t, settings.ShowTotalTokens)
	require.False(t, settings.ShowRequests)
	require.True(t, settings.ShowActualCost)
	require.Equal(t, usage.MaxUsageRankingLimit, settings.Limit)
}
