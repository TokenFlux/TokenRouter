package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type userGroupRateRepoHotpathStub struct {
	billing.UserGroupRateRepository

	rate  *float64
	err   error
	wait  <-chan struct{}
	calls atomic.Int64
}

func (s *userGroupRateRepoHotpathStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls.Add(1)
	if s.wait != nil {
		<-s.wait
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

type usageLogWindowBatchRepoStub struct {
	usage.UsageLogRepository

	batchResult map[int64]*usage.AccountStats
	batchErr    error
	batchCalls  atomic.Int64

	singleResult map[int64]*usage.AccountStats
	singleErr    error
	singleCalls  atomic.Int64
}

func (s *usageLogWindowBatchRepoStub) GetAccountWindowStatsBatch(ctx context.Context, accountIDs []int64, startTime time.Time) (map[int64]*usage.AccountStats, error) {
	s.batchCalls.Add(1)
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]*usage.AccountStats, len(accountIDs))
	for _, id := range accountIDs {
		if stats, ok := s.batchResult[id]; ok {
			out[id] = stats
		}
	}
	return out, nil
}

func (s *usageLogWindowBatchRepoStub) GetAccountWindowStats(ctx context.Context, accountID int64, startTime time.Time) (*usage.AccountStats, error) {
	s.singleCalls.Add(1)
	if s.singleErr != nil {
		return nil, s.singleErr
	}
	if stats, ok := s.singleResult[accountID]; ok {
		return stats, nil
	}
	return &usage.AccountStats{}, nil
}

type sessionLimitCacheHotpathStub struct {
	billing.WindowCostCache

	batchData map[int64]float64
	batchErr  error

	setData map[int64]float64
	setErr  error
}

func (s *sessionLimitCacheHotpathStub) GetWindowCostBatch(ctx context.Context, accountIDs []int64) (map[int64]float64, error) {
	if s.batchErr != nil {
		return nil, s.batchErr
	}
	out := make(map[int64]float64, len(accountIDs))
	for _, id := range accountIDs {
		if v, ok := s.batchData[id]; ok {
			out[id] = v
		}
	}
	return out, nil
}

func (s *sessionLimitCacheHotpathStub) SetWindowCost(ctx context.Context, accountID int64, cost float64) error {
	if s.setErr != nil {
		return s.setErr
	}
	if s.setData == nil {
		s.setData = make(map[int64]float64)
	}
	s.setData[accountID] = cost
	return nil
}

type modelsListAccountRepoStub struct {
	AccountRepository

	byGroup map[int64][]Account
	all     []Account
	err     error

	listByGroupCalls atomic.Int64
	listAllCalls     atomic.Int64
}

type stickyGatewayCacheHotpathStub struct {
	session.GatewayCache

	stickyID int64
	getCalls atomic.Int64
}

func (s *stickyGatewayCacheHotpathStub) GetSessionAccountID(ctx context.Context, groupID int64, sessionHash string) (int64, error) {
	s.getCalls.Add(1)
	if s.stickyID > 0 {
		return s.stickyID, nil
	}
	return 0, errors.New("not found")
}

func (s *stickyGatewayCacheHotpathStub) SetSessionAccountID(ctx context.Context, groupID int64, sessionHash string, accountID int64, ttl time.Duration) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) RefreshSessionTTL(ctx context.Context, groupID int64, sessionHash string, ttl time.Duration) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) DeleteSessionAccountID(ctx context.Context, groupID int64, sessionHash string) error {
	return nil
}

func (s *stickyGatewayCacheHotpathStub) SetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string, groupID int64, ttl time.Duration) (bool, error) {
	return true, nil
}

func (s *stickyGatewayCacheHotpathStub) GetSessionOwnerGroupID(ctx context.Context, userID int64, source, sessionHash string) (int64, error) {
	return 0, errors.New("not found")
}

func (s *stickyGatewayCacheHotpathStub) RefreshSessionOwnerTTL(ctx context.Context, userID int64, source, sessionHash string, ttl time.Duration) error {
	return nil
}

func (s *modelsListAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]Account, error) {
	s.listByGroupCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	accounts, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]Account, len(accounts))
	copy(out, accounts)
	return out, nil
}

func (s *modelsListAccountRepoStub) ListSchedulable(ctx context.Context) ([]Account, error) {
	s.listAllCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]Account, len(s.all))
	copy(out, s.all)
	return out, nil
}

func resetGatewayHotpathStatsForTest() {
	windowCostPrefetchCacheHitTotal.Store(0)
	windowCostPrefetchCacheMissTotal.Store(0)
	windowCostPrefetchBatchSQLTotal.Store(0)
	windowCostPrefetchFallbackTotal.Store(0)
	windowCostPrefetchErrorTotal.Store(0)

	userGroupRateCacheHitTotal.Store(0)
	userGroupRateCacheMissTotal.Store(0)
	userGroupRateCacheLoadTotal.Store(0)
	userGroupRateCacheSFSharedTotal.Store(0)
	userGroupRateCacheFallbackTotal.Store(0)

	modelsListCacheHitTotal.Store(0)
	modelsListCacheMissTotal.Store(0)
	modelsListCacheStoreTotal.Store(0)
}

func TestGetUserGroupRateMultiplier_UsesCacheAndSingleflight(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	rate := 1.7
	unblock := make(chan struct{})
	repo := &userGroupRateRepoHotpathStub{
		rate: &rate,
		wait: unblock,
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 30,
			},
		},
	}

	const concurrent = 12
	results := make([]float64, concurrent)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.2)
		}(i)
	}

	close(start)
	// 等所有调用方都记录 cache miss 后再释放 loader，避免固定 sleep 与调度器竞争。
	// miss 计数正是下方断言的可观察条件，也能保证调用方已经进入 singleflight。
	require.Eventually(t, func() bool {
		_, miss, _, _, _ := GatewayUserGroupRateCacheStats()
		return miss == int64(concurrent)
	}, 5*time.Second, time.Millisecond, "所有调用方必须在释放 loader 前完成 cache miss")
	close(unblock)
	wg.Wait()

	for _, got := range results {
		require.Equal(t, rate, got)
	}
	require.Equal(t, int64(1), repo.calls.Load())

	// 再次读取应命中缓存，不再回源。
	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.2)
	require.Equal(t, rate, got)
	require.Equal(t, int64(1), repo.calls.Load())

	hit, miss, load, sfShared, fallback := GatewayUserGroupRateCacheStats()
	require.GreaterOrEqual(t, hit, int64(1))
	require.Equal(t, int64(12), miss)
	require.Equal(t, int64(1), load)
	require.GreaterOrEqual(t, sfShared, int64(1))
	require.Equal(t, int64(0), fallback)
}

func TestGetUserGroupRateMultiplier_FallbackOnRepoError(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("db down"),
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
		cfg: &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 30,
			},
		},
	}

	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.25)
	require.Equal(t, 1.25, got)
	require.Equal(t, int64(1), repo.calls.Load())

	_, _, _, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), fallback)
}

func TestGetUserGroupRateMultiplier_CacheHitAndNilRepo(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("should not be called"),
	}
	svc := &GatewayService{
		userGroupRateRepo:  repo,
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
	}
	key := "101:202"
	svc.userGroupRateCache.Set(key, 2.3, time.Minute)

	got := svc.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.1)
	require.Equal(t, 2.3, got)

	hit, miss, load, _, fallback := GatewayUserGroupRateCacheStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(0), miss)
	require.Equal(t, int64(0), load)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), repo.calls.Load())

	// 无 repo 时直接返回分组默认倍率
	svc2 := &GatewayService{
		userGroupRateCache: gocache.New(time.Minute, time.Minute),
	}
	svc2.userGroupRateCache.Set(key, 1.9, time.Minute)
	require.Equal(t, 1.9, svc2.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.4))
	require.Equal(t, 1.4, svc2.getUserGroupRateMultiplier(context.Background(), 0, 202, 1.4))
	svc2.userGroupRateCache.Delete(key)
	require.Equal(t, 1.4, svc2.getUserGroupRateMultiplier(context.Background(), 101, 202, 1.4))
}

func TestWithWindowCostPrefetch_BatchReadAndContextReuse(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []Account{
		{
			ID:                 1,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
		{
			ID:                 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
		{
			ID:       3,
			Platform: capability.PlatformAnthropic,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"window_cost_limit": 100.0},
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{
		batchResult: map[int64]*usage.AccountStats{
			2: {StandardCost: 22.0},
		},
	}
	svc := &GatewayService{
		windowCostCache: cache,
		usageLogRepo:    repo,
	}

	outCtx := svc.withWindowCostPrefetch(context.Background(), accounts)
	require.NotNil(t, outCtx)

	cost1, ok1 := billing.PrefetchedWindowCost(outCtx, 1)
	require.True(t, ok1)
	require.Equal(t, 11.0, cost1)

	cost2, ok2 := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok2)
	require.Equal(t, 22.0, cost2)

	_, ok3 := billing.PrefetchedWindowCost(outCtx, 3)
	require.False(t, ok3)

	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, 22.0, cache.setData[2])

	hit, miss, batchSQL, fallback, errCount := GatewayWindowCostPrefetchStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(1), batchSQL)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), errCount)
}

func TestWithWindowCostPrefetch_AllHitNoSQL(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []Account{
		{
			ID:                 1,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
		{
			ID:                 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
			2: 22.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{}
	svc := &GatewayService{
		windowCostCache: cache,
		usageLogRepo:    repo,
	}

	outCtx := svc.withWindowCostPrefetch(context.Background(), accounts)
	cost1, ok1 := billing.PrefetchedWindowCost(outCtx, 1)
	cost2, ok2 := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok1)
	require.True(t, ok2)
	require.Equal(t, 11.0, cost1)
	require.Equal(t, 22.0, cost2)
	require.Equal(t, int64(0), repo.batchCalls.Load())
	require.Equal(t, int64(0), repo.singleCalls.Load())

	hit, miss, batchSQL, fallback, errCount := GatewayWindowCostPrefetchStats()
	require.Equal(t, int64(2), hit)
	require.Equal(t, int64(0), miss)
	require.Equal(t, int64(0), batchSQL)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), errCount)
}

func TestWithWindowCostPrefetch_BatchErrorFallbackSingleQuery(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []Account{
		{
			ID:                 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd,
		},
	}

	cache := &sessionLimitCacheHotpathStub{}
	repo := &usageLogWindowBatchRepoStub{
		batchErr: errors.New("batch failed"),
		singleResult: map[int64]*usage.AccountStats{
			2: {StandardCost: 33.0},
		},
	}
	svc := &GatewayService{
		windowCostCache: cache,
		usageLogRepo:    repo,
	}

	outCtx := svc.withWindowCostPrefetch(context.Background(), accounts)
	cost, ok := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok)
	require.Equal(t, 33.0, cost)
	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, int64(1), repo.singleCalls.Load())

	_, _, _, fallback, errCount := GatewayWindowCostPrefetchStats()
	require.Equal(t, int64(1), fallback)
	require.Equal(t, int64(1), errCount)
}

func TestGatewayHotpathHelpers_CacheTTLAndStickyContext(t *testing.T) {
	t.Run("resolve_user_group_rate_cache_ttl", func(t *testing.T) {
		require.Equal(t, billing.DefaultGroupRateCacheTTL, resolveUserGroupRateCacheTTL(nil))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{
				UserGroupRateCacheTTLSeconds: 45,
			},
		}
		require.Equal(t, 45*time.Second, resolveUserGroupRateCacheTTL(cfg))
	})

	t.Run("resolve_models_list_cache_ttl", func(t *testing.T) {
		require.Equal(t, defaultModelsListCacheTTL, resolveModelsListCacheTTL(nil))

		cfg := &config.Config{
			Gateway: config.GatewayConfig{
				ModelsListCacheTTLSeconds: 20,
			},
		}
		require.Equal(t, 20*time.Second, resolveModelsListCacheTTL(cfg))
	})

	t.Run("prefetched_sticky_account_id_from_context", func(t *testing.T) {
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.TODO(), nil))
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.Background(), nil))

		ctx := requeststate.WithPrefetchedStickySession(context.Background(), 123, 0)
		require.Equal(t, int64(123), prefetchedStickyAccountIDFromContext(ctx, nil))

		groupID := int64(9)
		ctx2 := requeststate.WithPrefetchedStickySession(context.Background(), 456, groupID)
		require.Equal(t, int64(456), prefetchedStickyAccountIDFromContext(ctx2, &groupID))

		// 原无效账号值不产生预取命中；原生状态以缺失账号表达同一回源边界。
		ctx3 := requeststate.WithExecutionHints(context.Background(), requeststate.ExecutionHints{
			PrefetchedStickyGroupID: requeststate.Hint[int64]{Value: groupID, Set: true},
		})
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(ctx3, &groupID))

		ctx4 := requeststate.WithPrefetchedStickySession(context.Background(), 789, 10)
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(ctx4, &groupID))
	})

	t.Run("window_cost_from_prefetch_context", func(t *testing.T) {
		require.Equal(t, false, func() bool {
			_, ok := billing.PrefetchedWindowCost(context.TODO(), 0)
			return ok
		}())
		require.Equal(t, false, func() bool {
			_, ok := billing.PrefetchedWindowCost(context.Background(), 1)
			return ok
		}())

		ctx := billing.WithPrefetchedWindowCosts(context.Background(), map[int64]float64{
			9: 12.34,
		})
		cost, ok := billing.PrefetchedWindowCost(ctx, 9)
		require.True(t, ok)
		require.Equal(t, 12.34, cost)
	})
}

func TestSelectAccountWithLoadAwareness_StickyReadReuse(t *testing.T) {
	now := time.Now().Add(-time.Minute)
	account := Account{
		ID:          88,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 4,
		Priority:    1,
		LastUsedAt:  &now,
	}

	repo := stubOpenAIAccountRepo{accounts: []Account{account}}
	concurrency := scheduler.NewConcurrencyService(stubConcurrencyCache{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event},
	)

	cfg := &config.Config{
		RunMode: config.RunModeStandard,
		Gateway: config.GatewayConfig{
			Scheduling: config.GatewaySchedulingConfig{
				LoadBatchEnabled:         true,
				StickySessionMaxWaiting:  3,
				StickySessionWaitTimeout: time.Second,
				FallbackWaitTimeout:      time.Second,
				FallbackMaxWaiting:       10,
			},
		},
	}

	baseCtx := apikey.WithForcePlatform(context.Background(), capability.PlatformAnthropic)

	t.Run("without_prefetch_reads_cache_once", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.ID}
		svc := &GatewayService{
			accountRepo:        repo,
			cache:              cache,
			cfg:                cfg,
			concurrencyService: concurrency,
			userGroupRateCache: gocache.New(time.Minute, time.Minute),
		}

		result, err := svc.SelectAccountWithLoadAwareness(baseCtx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.ID, result.Account.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})

	t.Run("with_prefetch_skips_cache_read", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.ID}
		svc := &GatewayService{
			accountRepo:        repo,
			cache:              cache,
			cfg:                cfg,
			concurrencyService: concurrency,
			userGroupRateCache: gocache.New(time.Minute, time.Minute),
		}

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, account.ID, 0)
		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.ID, result.Account.ID)
		require.Equal(t, int64(0), cache.getCalls.Load())
	})

	t.Run("with_prefetch_group_mismatch_reads_cache", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.ID}
		svc := &GatewayService{
			accountRepo:        repo,
			cache:              cache,
			cfg:                cfg,
			concurrencyService: concurrency,
			userGroupRateCache: gocache.New(time.Minute, time.Minute),
		}

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, 999, 77)
		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.ID, result.Account.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})
}
