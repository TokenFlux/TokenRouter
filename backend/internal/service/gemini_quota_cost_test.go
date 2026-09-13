//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/stretchr/testify/require"
)

func TestGeminiAggregateUsageUsesAccountCost(t *testing.T) {
	// 用户扣费倍率与账号成本倍率不同时，Gemini 本地用量必须保持账号成本口径。
	stats := []usagestats.ModelStat{
		{
			Model:       "gemini-2.5-pro",
			Requests:    2,
			TotalTokens: 300,
			ActualCost:  500,
			AccountCost: 10,
		},
		{
			Model:       "gemini-2.5-flash",
			Requests:    3,
			TotalTokens: 400,
			ActualCost:  100,
			AccountCost: 2,
		},
	}

	totals := geminiAggregateUsage(stats)

	require.Equal(t, int64(2), totals.ProRequests)
	require.Equal(t, int64(3), totals.FlashRequests)
	require.Equal(t, int64(300), totals.ProTokens)
	require.Equal(t, int64(400), totals.FlashTokens)
	require.InDelta(t, 10, totals.ProCost, 0.000001)
	require.InDelta(t, 2, totals.FlashCost, 0.000001)
}

func TestGeminiThirdPartyAPIKeySkipsLocalQuota(t *testing.T) {
	ctx := context.Background()
	quotaService := NewGeminiQuotaService(&config.Config{}, nil)
	official := &Account{
		ID:       101,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"tier_id":       GeminiTierAIStudioFree,
			"provider_type": "official",
		},
	}
	thirdParty := &Account{
		ID:       102,
		Platform: PlatformGemini,
		Type:     AccountTypeAPIKey,
		Credentials: map[string]any{
			"provider_type": GeminiProviderTypeThirdParty,
		},
	}

	_, officialHasQuota := quotaService.QuotaForAccount(ctx, official)
	_, thirdPartyHasQuota := quotaService.QuotaForAccount(ctx, thirdParty)
	require.True(t, officialHasQuota)
	require.False(t, thirdPartyHasQuota)
	require.True(t, thirdParty.IsGeminiThirdPartyProvider())
	require.Equal(t, 5*time.Minute, quotaService.CooldownForAccount(ctx, thirdParty))

	usageSvc := &AccountUsageService{
		geminiQuotaService: quotaService,
		usageLogRepo:       &usageBatchLogRepoStub{},
	}
	usage, err := usageSvc.getGeminiUsage(ctx, thirdParty)
	require.NoError(t, err)
	require.Nil(t, usage.GeminiSharedDaily)
	require.Nil(t, usage.GeminiProDaily)
	require.Nil(t, usage.GeminiFlashDaily)

	// 官方免费档位的 Pro 日配额为 50；由原统计接口回源后必须被预检拦截。
	rateLimitSvc := NewRateLimitService(nil, &geminiFullLocalUsage{}, &config.Config{}, quotaService, nil)
	officialAllowed, err := rateLimitSvc.PreCheckUsage(ctx, official, "gemini-2.5-pro")
	require.NoError(t, err)
	require.False(t, officialAllowed)

	thirdPartyAllowed, err := rateLimitSvc.PreCheckUsage(ctx, thirdParty, "gemini-2.5-pro")
	require.NoError(t, err)
	require.True(t, thirdPartyAllowed)
}

// 满额夹具只提供原 SQL 查询投影，不直接修改实现的缓存。
type geminiFullLocalUsage struct{ usageBatchLogRepoStub }

func (r *geminiFullLocalUsage) GetModelStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, *int16, *bool, *int8) ([]usagestats.ModelStat, error) {
	return []usagestats.ModelStat{{Model: "gemini-2.5-pro", Requests: 50}}, nil
}
