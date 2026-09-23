package selection

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	scheduler "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	"github.com/stretchr/testify/require"
)

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

func resetGatewayHotpathStatsForTest() {
	billing.SharedWindowCostMetrics().Hit.Store(0)
	billing.SharedWindowCostMetrics().Miss.Store(0)
	billing.SharedWindowCostMetrics().BatchSQL.Store(0)
	billing.SharedWindowCostMetrics().Fallback.Store(0)
	billing.SharedWindowCostMetrics().Errors.Store(0)

	billing.SharedGroupRateMetrics().Hit.Store(0)
	billing.SharedGroupRateMetrics().Miss.Store(0)
	billing.SharedGroupRateMetrics().Load.Store(0)
	billing.SharedGroupRateMetrics().Shared.Store(0)
	billing.SharedGroupRateMetrics().Fallback.Store(0)

	routing.SharedModelListMetrics().Hit.Store(0)
	routing.SharedModelListMetrics().Miss.Store(0)
	routing.SharedModelListMetrics().Store.Store(0)
}

func TestWithWindowCostPrefetch_BatchReadAndContextReuse(t *testing.T) {
	resetGatewayHotpathStatsForTest()

	windowStart := time.Now().Add(-30 * time.Minute).Truncate(time.Hour)
	windowEnd := windowStart.Add(5 * time.Hour)
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 3,
			Platform: capability.PlatformAnthropic,
			Type:     capability.AccountTypeAPIKey,
			Extra:    map[string]any{"window_cost_limit": 100.0}},
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
	svc := newGenericSelectionForTest(GenericDependencies{

		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	// 原无效账号值不产生预取命中；原生状态以缺失账号表达同一回源边界。

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

	hit, miss, batchSQL, fallback, errCount := windowMetricsForTest()
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
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 1,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeOAuth,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd},
		},
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd},
		},
	}

	cache := &sessionLimitCacheHotpathStub{
		batchData: map[int64]float64{
			1: 11.0,
			2: 22.0,
		},
	}
	repo := &usageLogWindowBatchRepoStub{}
	svc := newGenericSelectionForTest(GenericDependencies{

		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	outCtx := svc.withWindowCostPrefetch(context.Background(), accounts)
	cost1, ok1 := billing.PrefetchedWindowCost(outCtx, 1)
	cost2, ok2 := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok1)
	require.True(t, ok2)
	require.Equal(t, 11.0, cost1)
	require.Equal(t, 22.0, cost2)
	require.Equal(t, int64(0), repo.batchCalls.Load())
	require.Equal(t, int64(0), repo.singleCalls.Load())

	hit, miss, batchSQL, fallback, errCount := windowMetricsForTest()
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
	accounts := []gatewayprovider.ExecutionAccount{
		{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 2,
			Platform:           capability.PlatformAnthropic,
			Type:               capability.AccountTypeSetupToken,
			Extra:              map[string]any{"window_cost_limit": 100.0},
			SessionWindowStart: &windowStart,
			SessionWindowEnd:   &windowEnd},
		},
	}

	cache := &sessionLimitCacheHotpathStub{}
	repo := &usageLogWindowBatchRepoStub{
		batchErr: errors.New("batch failed"),
		singleResult: map[int64]*usage.AccountStats{
			2: {StandardCost: 33.0},
		},
	}
	svc := newGenericSelectionForTest(GenericDependencies{

		Reads:                   Reads{},
		Shared:                  Shared{},
		Window:                  selectionWindowForTest(cache, repo),
		WindowPrefetchAvailable: true,
	}, nil)

	outCtx := svc.withWindowCostPrefetch(context.Background(), accounts)
	cost, ok := billing.PrefetchedWindowCost(outCtx, 2)
	require.True(t, ok)
	require.Equal(t, 33.0, cost)
	require.Equal(t, int64(1), repo.batchCalls.Load())
	require.Equal(t, int64(1), repo.singleCalls.Load())

	_, _, _, fallback, errCount := windowMetricsForTest()
	require.Equal(t, int64(1), fallback)
	require.Equal(t, int64(1), errCount)
}

func TestGatewayHotpathHelpers_CacheTTLAndStickyContext(t *testing.T) {

	t.Run("prefetched_sticky_account_id_from_context", func(t *testing.T) {
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.TODO(), nil))
		require.Equal(t, int64(0), prefetchedStickyAccountIDFromContext(context.Background(), nil))

		ctx := requeststate.WithPrefetchedStickySession(context.Background(), 123, 0)
		require.Equal(t, int64(123), prefetchedStickyAccountIDFromContext(ctx, nil))

		groupID := int64(9)
		ctx2 := requeststate.WithPrefetchedStickySession(context.Background(), 456, groupID)
		require.Equal(t, int64(456), prefetchedStickyAccountIDFromContext(ctx2, &groupID))

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
	account := gatewayprovider.ExecutionAccount{Record: accountcore.Record{LoadLocation: time.LoadLocation, ID: 88,
		Platform:    capability.PlatformAnthropic,
		Type:        capability.AccountTypeAPIKey,
		Status:      billing.StatusActive,
		Schedulable: true,
		Concurrency: 4,
		Priority:    1,
		LastUsedAt:  &now},
	}

	repo := selectionAccountFixture{accounts: []gatewayprovider.ExecutionAccount{account}}
	concurrency := scheduler.NewConcurrencyService(selectionConcurrencyFixture{}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

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
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{

			Reads: Reads{Accounts: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		result, err := svc.SelectAccountWithLoadAwareness(baseCtx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.Record.ID, result.Account.Record.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})

	t.Run("with_prefetch_skips_cache_read", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{

			Reads: Reads{Accounts: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, account.Record.ID, 0)
		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.Record.ID, result.Account.Record.ID)
		require.Equal(t, int64(0), cache.getCalls.Load())
	})

	t.Run("with_prefetch_group_mismatch_reads_cache", func(t *testing.T) {
		cache := &stickyGatewayCacheHotpathStub{stickyID: account.Record.ID}
		svc := newGenericSelectionForTest(GenericDependencies{

			Reads: Reads{Accounts: repo},

			Shared: Shared{Cache: cache, Concurrency: concurrency},
		}, cfg)

		ctx := requeststate.WithPrefetchedStickySession(baseCtx, 999, 77)
		result, err := svc.SelectAccountWithLoadAwareness(ctx, nil, "sess-hash", "", nil, "", int64(0))
		require.NoError(t, err)
		require.NotNil(t, result)
		require.NotNil(t, result.Account)
		require.Equal(t, account.Record.ID, result.Account.Record.ID)
		require.Equal(t, int64(1), cache.getCalls.Load())
	})
}

// windowMetricsForTest 直接读取同一资金窗口观测，不保留旧网关统计入口。
func windowMetricsForTest() (int64, int64, int64, int64, int64) {
	m := billing.SharedWindowCostMetrics()
	return m.Hit.Load(), m.Miss.Load(), m.BatchSQL.Load(), m.Fallback.Load(), m.Errors.Load()
}
