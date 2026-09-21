//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/stretchr/testify/require"
)

func TestGeminiThirdPartyAPIKeySkipsLocalQuota(t *testing.T) {
	ctx := context.Background()
	quotaService := account.NewGeminiQuotaService(account.GeminiQuotaOptions{})
	official := &Account{
		ID:       101,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"tier_id":       account.GeminiTierAIStudioFree,
			"provider_type": "official",
		},
	}
	thirdParty := &Account{
		ID:       102,
		Platform: capability.PlatformGemini,
		Type:     capability.AccountTypeAPIKey,
		Credentials: map[string]any{
			"provider_type": account.GeminiProviderTypeThirdParty,
		},
	}

	_, officialHasQuota := quotaService.QuotaForAccount(ctx, AccountRecordView(official))
	_, thirdPartyHasQuota := quotaService.QuotaForAccount(ctx, AccountRecordView(thirdParty))
	require.True(t, officialHasQuota)
	require.False(t, thirdPartyHasQuota)
	require.True(t, thirdParty.IsGeminiThirdPartyProvider())
	require.Equal(t, 5*time.Minute, quotaService.CooldownForAccount(ctx, AccountRecordView(thirdParty)))

	usageSvc := account.NewOAuthUsageService(nil, nil, nil, account.OAuthUsageOptions{Gemini: account.GeminiUsageOptions{
		Location: geminiQuotaLocation, Quota: quotaService.QuotaForAccount,
		Totals: func(context.Context, int64, time.Time, time.Time) (account.GeminiUsageTotals, error) {
			return account.GeminiUsageTotals{}, nil
		},
	}})
	usage, err := usageSvc.GetGeminiUsage(ctx, AccountRecordView(thirdParty))
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

func (r *geminiFullLocalUsage) GetModelStatsWithFilters(context.Context, time.Time, time.Time, int64, int64, int64, int64, *int16, *bool, *int8) ([]usagecore.ModelStat, error) {
	return []usagecore.ModelStat{{Model: "gemini-2.5-pro", Requests: 50}}, nil
}
