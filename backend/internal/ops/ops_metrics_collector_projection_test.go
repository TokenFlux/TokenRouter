package ops

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

type opsMetricsProjectionRepo struct {
	AccountLoadSource
	accounts        []AccountObservation
	accountLoads    []AccountWithConcurrency
	listCalls       int
	projectionCalls int
}

func (r *opsMetricsProjectionRepo) ListSchedulable(context.Context) ([]AccountObservation, error) {
	r.listCalls++
	return r.accounts, nil
}

func (r *opsMetricsProjectionRepo) ListSchedulableAccountLoads(context.Context) ([]AccountWithConcurrency, error) {
	r.projectionCalls++
	return r.accountLoads, nil
}

type opsMetricsFallbackRepo struct {
	AccountLoadSource
	accounts  []AccountObservation
	listCalls int
}

func (r *opsMetricsFallbackRepo) ListSchedulable(context.Context) ([]AccountObservation, error) {
	r.listCalls++
	return r.accounts, nil
}

type opsMetricsLoadCache struct {
	scheduler.ConcurrencyCache
	loads map[int64]*AccountLoadInfo
	got   []AccountWithConcurrency
}

func (c *opsMetricsLoadCache) GetAccountsLoadBatch(_ context.Context, accounts []AccountWithConcurrency) (map[int64]*AccountLoadInfo, error) {
	c.got = accounts
	return c.loads, nil
}

func TestCollectConcurrencyQueueDepthUsesProjectionAndPreservesFallbackResult(t *testing.T) {
	loadFactor := 7
	accounts := []AccountObservation{
		{ID: 11, Concurrency: 2, LoadFactor: loadFactor},
		{ID: 12, Concurrency: 3, LoadFactor: 3},
		{ID: 13, LoadFactor: 1},
	}
	accountLoads := []AccountWithConcurrency{
		{ID: 11, MaxConcurrency: 7},
		{ID: 12, MaxConcurrency: 3},
		{ID: 13, MaxConcurrency: 1},
	}
	loads := map[int64]*AccountLoadInfo{
		11: {AccountID: 11, WaitingCount: 2},
		12: {AccountID: 12, WaitingCount: 3},
		13: {AccountID: 13, WaitingCount: 0},
	}

	projectionRepo := &opsMetricsProjectionRepo{accounts: accounts, accountLoads: accountLoads}
	projectionCache := &opsMetricsLoadCache{loads: loads}
	projectionConcurrency := scheduler.NewConcurrencyService(projectionCache)
	projectionConcurrency.SetAccountLoadBatchCacheTTL(0)
	projectionCollector := &OpsMetricsCollector{
		accountRepo:        projectionRepo,
		concurrencyService: projectionConcurrency,
	}

	fallbackRepo := &opsMetricsFallbackRepo{accounts: accounts}
	fallbackCache := &opsMetricsLoadCache{loads: loads}
	fallbackConcurrency := scheduler.NewConcurrencyService(fallbackCache)
	fallbackConcurrency.SetAccountLoadBatchCacheTTL(0)
	fallbackCollector := &OpsMetricsCollector{
		accountRepo:        fallbackRepo,
		concurrencyService: fallbackConcurrency,
	}

	projectionDepth := projectionCollector.collectConcurrencyQueueDepth(context.Background())
	fallbackDepth := fallbackCollector.collectConcurrencyQueueDepth(context.Background())

	require.NotNil(t, projectionDepth)
	require.NotNil(t, fallbackDepth)
	require.Equal(t, 5, *projectionDepth)
	require.Equal(t, *fallbackDepth, *projectionDepth)
	require.Equal(t, 1, projectionRepo.projectionCalls)
	require.Zero(t, projectionRepo.listCalls)
	require.Equal(t, 1, fallbackRepo.listCalls)
	require.Equal(t, accountLoads, projectionCache.got)
	require.Equal(t, fallbackCache.got, projectionCache.got)
}

func BenchmarkOpsMetricsCollectorCollectConcurrencyQueueDepth(b *testing.B) {
	const accountCount = 1000
	loadFactor := 8
	accounts := make([]AccountObservation, accountCount)
	accountLoads := make([]AccountWithConcurrency, accountCount)
	for i := range accountCount {
		id := int64(i + 1)
		accounts[i] = AccountObservation{
			ID:          id,
			Concurrency: 4,
			LoadFactor:  loadFactor,
		}
		accountLoads[i] = AccountWithConcurrency{ID: id, MaxConcurrency: loadFactor}
	}

	repo := &opsMetricsProjectionRepo{accounts: accounts, accountLoads: accountLoads}
	cache := &opsMetricsLoadCache{loads: map[int64]*AccountLoadInfo{}}
	concurrency := scheduler.NewConcurrencyService(cache)
	concurrency.SetAccountLoadBatchCacheTTL(0)
	collector := &OpsMetricsCollector{accountRepo: repo, concurrencyService: concurrency}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if depth := collector.collectConcurrencyQueueDepth(context.Background()); depth == nil || *depth != 0 {
			b.Fatalf("unexpected queue depth: %v", depth)
		}
	}
}
