//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// 已放行请求在管理员重置后结算，缓存失效不能吞掉这次新的额度累计。
func TestS04QuotaIncrementAfterResetKeepsUsage(t *testing.T) {
	ctx := context.Background()
	client := testEntClient(t)
	id := mustCreateUserForQuota(t, client)
	repo := billingpostgres.NewUserPlatformQuotaRepository(client, timezone.NewCalendar(time.Local))
	limit := 1000.0
	require.NoError(t, repo.BulkInsertInitial(ctx, []billing.UserPlatformQuotaRecord{{UserID: id, Platform: "openai", DailyLimitUSD: &limit}}))
	cache := billingredis.NewBillingCache(integrationRedis)
	require.NoError(t, cache.DeleteUserPlatformQuotaCache(ctx, id, "openai"))
	options := billing.EligibilityOptions{Billing: billing.BillingOptions{UserPlatformQuotaCacheTTLSeconds: 60}, Database: billing.QuotaMirrorOptions{UserPlatformQuotaFlusherEnabled: true}}
	eligibility := billing.NewEligibility(cache, quotaUsersForContract{postgres.NewUserStore(client, integrationDB)}, nil, repo, func() billing.EligibilityOptions { return options }, nil, billing.NewQuotaCoordinator(), func(_ string, fn func()) { go fn() })

	// 与管理重置的最后一步相同，此时存在已完成资金结算的在途请求。
	eligibility.IncrementUserPlatformQuotaUsage(id, "openai", 5)
	entry, hit, err := cache.GetUserPlatformQuotaCache(ctx, id, "openai")
	require.NoError(t, err)
	require.True(t, hit, "缓存失效后的累计必须先回源，不能被 EXISTS=0 静默丢弃")
	require.NotNil(t, entry)
	require.Equal(t, 5.0, entry.DailyUsageUSD)
	t.Cleanup(func() {
		_ = cache.DeleteUserPlatformQuotaCache(context.Background(), id, "openai")
		_ = integrationRedis.Del(context.Background(), "billing:upq:dirty").Err()
	})
}
