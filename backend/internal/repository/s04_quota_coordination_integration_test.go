//go:build integration

package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

type quotaUsersForContract struct {
	repository identity.UserRepository
}

func (r quotaUsersForContract) GetByID(ctx context.Context, id int64) (*billing.UserSummary, error) {
	user, err := r.repository.GetByID(ctx, id)
	return service.BillingUserSummary(user), err
}

// blockedQuotaSnapshot 在实际 Redis 读取后暂停，使测试能精确安排管理写入。
type blockedQuotaSnapshot struct {
	billing.BillingCache
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (c *blockedQuotaSnapshot) BatchGetUserPlatformQuotaCache(ctx context.Context, keys []billing.UserPlatformQuotaKey) ([]*billing.UserPlatformQuotaCacheEntry, error) {
	entries, err := c.BillingCache.BatchGetUserPlatformQuotaCache(ctx, keys)
	c.once.Do(func() {
		close(c.read)
		select {
		case <-c.release:
		case <-ctx.Done():
		}
	})
	return entries, err
}

func quotaCoordinationFixture(t *testing.T) (int64, billing.UserPlatformQuotaRepository, billing.BillingCache, billing.BalanceReader) {
	t.Helper()
	ctx := context.Background()
	client := testEntClient(t)
	id := mustCreateUserForQuota(t, client)
	repository := billingpostgres.NewUserPlatformQuotaRepository(client, timezone.NewCalendar(time.Local))
	limit := 1000.0
	require.NoError(t, repository.BulkInsertInitial(ctx, []billing.UserPlatformQuotaRecord{{UserID: id, Platform: "openai", DailyLimitUSD: &limit}}))
	require.NoError(t, repository.IncrementUsageWithReset(ctx, id, "openai", 100, time.Now()))
	record, err := repository.GetByUserPlatform(ctx, id, "openai")
	require.NoError(t, err)
	cache := billingredis.NewBillingCache(integrationRedis)
	require.NoError(t, cache.SetUserPlatformQuotaCache(ctx, id, "openai", &billing.UserPlatformQuotaCacheEntry{
		SchemaVersion: billing.UserPlatformQuotaCacheSchemaV1,
		DailyLimitUSD: record.DailyLimitUSD, WeeklyLimitUSD: record.WeeklyLimitUSD, MonthlyLimitUSD: record.MonthlyLimitUSD,
		DailyUsageUSD: record.DailyUsageUSD, WeeklyUsageUSD: record.WeeklyUsageUSD, MonthlyUsageUSD: record.MonthlyUsageUSD,
		DailyWindowStart: record.DailyWindowStart, WeeklyWindowStart: record.WeeklyWindowStart, MonthlyWindowStart: record.MonthlyWindowStart,
	}, time.Minute))
	require.NoError(t, integrationRedis.SAdd(ctx, "billing:upq:dirty", fmt.Sprintf("%d:openai", id)).Err())
	t.Cleanup(func() {
		_ = cache.DeleteUserPlatformQuotaCache(ctx, id, "openai")
		_ = integrationRedis.SRem(ctx, "billing:upq:dirty", fmt.Sprintf("%d:openai", id)).Err()
	})
	return id, repository, cache, quotaUsersForContract{postgres.NewUserStore(client, integrationDB)}
}

// TestS04QuotaResetWaitsForFlusher 验证单实例中旧快照写回与管理重置不能交错。
func TestS04QuotaResetWaitsForFlusher(t *testing.T) {
	id, repository, cache, users := quotaCoordinationFixture(t)
	coordinator := billing.NewQuotaCoordinator()
	blocked := &blockedQuotaSnapshot{BillingCache: cache, read: make(chan struct{}), release: make(chan struct{})}
	flusher := billing.NewUserPlatformQuotaUsageFlusher(billing.FlusherOptions{}, blocked, repository, nil, coordinator, nil)
	manager := billing.NewPlatformQuotas(repository, cache, users, coordinator, time.Now, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	flushed := make(chan error, 1)
	go func() { flushed <- flusher.Shutdown(ctx) }()
	select {
	case <-blocked.read:
	case <-ctx.Done():
		t.Fatal("flusher 未读取到 Redis 快照")
	}
	reset := make(chan error, 1)
	go func() {
		_, err := manager.Reset(ctx, 1, id, "openai", "daily")
		reset <- err
	}()
	select {
	case err := <-reset:
		t.Fatalf("flusher 持有旧快照时重置提前结束：%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(blocked.release)
	require.NoError(t, <-flushed)
	require.NoError(t, <-reset)
	record, err := repository.GetByUserPlatform(ctx, id, "openai")
	require.NoError(t, err)
	require.Zero(t, record.DailyUsageUSD)
	_, hit, err := cache.GetUserPlatformQuotaCache(ctx, id, "openai")
	require.NoError(t, err)
	require.False(t, hit, "重置后的失效不能被旧 flusher 快照反向恢复")
}

// TestS04QuotaLockCancellationReaddsDirty 验证预算到期后仍尽力回填已 Pop 的键。
func TestS04QuotaLockCancellationReaddsDirty(t *testing.T) {
	id, repository, cache, _ := quotaCoordinationFixture(t)
	coordinator := billing.NewQuotaCoordinator()
	unlock, err := coordinator.Acquire(context.Background(), id)
	require.NoError(t, err)
	defer unlock()
	flusher := billing.NewUserPlatformQuotaUsageFlusher(billing.FlusherOptions{}, cache, repository, nil, coordinator, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.Error(t, flusher.Shutdown(ctx))
	dirty, err := integrationRedis.SIsMember(context.Background(), "billing:upq:dirty", fmt.Sprintf("%d:openai", id)).Result()
	require.NoError(t, err)
	require.True(t, dirty, "等待用户锁超时不能静默丢失数据库镜像任务")
	unlock()
	retry := billing.NewUserPlatformQuotaUsageFlusher(billing.FlusherOptions{}, cache, repository, nil, coordinator, nil)
	require.NoError(t, retry.Shutdown(context.Background()))
}

type blockedQuotaLoad struct {
	billing.UserPlatformQuotaRepository
	read    chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockedQuotaLoad) GetByUserPlatform(ctx context.Context, id int64, platform string) (*billing.UserPlatformQuotaRecord, error) {
	record, err := r.UserPlatformQuotaRepository.GetByUserPlatform(ctx, id, platform)
	r.once.Do(func() {
		close(r.read)
		select {
		case <-r.release:
		case <-ctx.Done():
		}
	})
	return record, err
}

// TestS04QuotaResetWaitsForSingleflightFill 验证回源取得的旧值不会在重置后才写入缓存。
func TestS04QuotaResetWaitsForSingleflightFill(t *testing.T) {
	id, repository, cache, users := quotaCoordinationFixture(t)
	require.NoError(t, cache.DeleteUserPlatformQuotaCache(context.Background(), id, "openai"))
	coordinator := billing.NewQuotaCoordinator()
	blocked := &blockedQuotaLoad{UserPlatformQuotaRepository: repository, read: make(chan struct{}), release: make(chan struct{})}
	eligibility := billing.NewEligibility(cache, users, nil, blocked, func() billing.EligibilityOptions {
		return billing.EligibilityOptions{Billing: billing.BillingOptions{UserPlatformQuotaCacheTTLSeconds: 60}}
	}, nil, coordinator)
	manager := billing.NewPlatformQuotas(repository, cache, users, coordinator, time.Now, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	checked := make(chan error, 1)
	go func() { checked <- eligibility.CheckUserPlatformQuotaEligibility(ctx, id, "openai") }()
	select {
	case <-blocked.read:
	case <-ctx.Done():
		t.Fatal("准入未读取到数据库快照")
	}
	reset := make(chan error, 1)
	go func() { _, err := manager.Reset(ctx, 1, id, "openai", "daily"); reset <- err }()
	select {
	case err := <-reset:
		t.Fatalf("回源尚未回填时重置提前结束：%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(blocked.release)
	require.NoError(t, <-checked)
	require.NoError(t, <-reset)
	_, hit, err := cache.GetUserPlatformQuotaCache(ctx, id, "openai")
	require.NoError(t, err)
	require.False(t, hit)
	record, err := repository.GetByUserPlatform(ctx, id, "openai")
	require.NoError(t, err)
	require.Zero(t, record.DailyUsageUSD)
}

// 已进入异步队列的数据库镜像持有原用户锁，管理员不能在 Redis 与 SQL 之间重置。
func TestS04QuotaAsyncMirrorCannotCrossReset(t *testing.T) {
	for _, resetFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(resetFirst), func(t *testing.T) {
			id, repository, cache, users := quotaCoordinationFixture(t)
			coordinator := billing.NewQuotaCoordinator()
			eligibility := billing.NewEligibility(cache, users, nil, repository, func() billing.EligibilityOptions {
				return billing.EligibilityOptions{Billing: billing.BillingOptions{UserPlatformQuotaCacheTTLSeconds: 60}}
			}, nil, coordinator)
			manager := billing.NewPlatformQuotas(repository, cache, users, coordinator, time.Now, nil)
			ctx := context.Background()
			if resetFirst {
				_, err := manager.Reset(ctx, 1, id, "openai", "daily")
				require.NoError(t, err)
			}
			queue := make(chan func(), 1)
			effects := billing.SettlementEffects{Cache: eligibility, Quotas: repository, Background: func(_ string, fn func()) bool { queue <- fn; return true }}
			effects.Finalize(billing.SettlementEffectInput{UserID: id, HasUser: true, Platform: "openai", Cost: &billing.CostBreakdown{ActualCost: 5}})
			mirror := <-queue
			deadline, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
			_, err := manager.Reset(deadline, 1, id, "openai", "daily")
			cancel()
			require.ErrorIs(t, err, context.DeadlineExceeded)
			mirror()
			record, err := repository.GetByUserPlatform(ctx, id, "openai")
			require.NoError(t, err)
			expected := 105.0
			if resetFirst {
				expected = 5
			}
			require.Equal(t, expected, record.DailyUsageUSD)
			_, err = manager.Reset(ctx, 1, id, "openai", "daily")
			require.NoError(t, err)
			record, err = repository.GetByUserPlatform(ctx, id, "openai")
			require.NoError(t, err)
			require.Zero(t, record.DailyUsageUSD)
		})
	}
}

// 多个 flusher 遇到管理删除时只能读取失效后的缓存，不能重新创建旧配置。
func TestS04QuotaDeleteBeforeMultipleFlushers(t *testing.T) {
	id, repository, cache, users := quotaCoordinationFixture(t)
	coordinator := billing.NewQuotaCoordinator()
	manager := billing.NewPlatformQuotas(repository, cache, users, coordinator, time.Now, nil)
	_, err := manager.Replace(context.Background(), 1, id, nil)
	require.NoError(t, err)
	results := make(chan error, 2)
	for range 2 {
		flusher := billing.NewUserPlatformQuotaUsageFlusher(billing.FlusherOptions{}, cache, repository, nil, coordinator, nil)
		go func() { results <- flusher.Shutdown(context.Background()) }()
	}
	for range 2 {
		require.NoError(t, <-results)
	}
	record, err := repository.GetByUserPlatform(context.Background(), id, "openai")
	require.NoError(t, err)
	require.Nil(t, record)
}

// 生命周期拒绝新的镜像任务时必须释放已取得的用户锁，不能造成永久阻塞。
func TestS04QuotaRejectedMirrorReleasesLock(t *testing.T) {
	id, repository, cache, users := quotaCoordinationFixture(t)
	coordinator := billing.NewQuotaCoordinator()
	eligibility := billing.NewEligibility(cache, users, nil, repository, func() billing.EligibilityOptions {
		return billing.EligibilityOptions{Billing: billing.BillingOptions{UserPlatformQuotaCacheTTLSeconds: 60}}
	}, nil, coordinator)
	effects := billing.SettlementEffects{Cache: eligibility, Quotas: repository, Background: func(string, func()) bool { return false }}
	effects.Finalize(billing.SettlementEffectInput{UserID: id, HasUser: true, Platform: "openai", Cost: &billing.CostBreakdown{ActualCost: 5}})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	unlock, err := coordinator.Acquire(ctx, id)
	require.NoError(t, err)
	unlock()
}
