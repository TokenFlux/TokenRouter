package billing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type userGroupRateRepoHotpathStub struct {
	UserGroupRateRepository

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

func TestGetUserGroupRateMultiplier_UsesCacheAndSingleflight(t *testing.T) {
	resetGatewayRateStatsForTest()

	rate := 1.7
	unblock := make(chan struct{})
	repo := &userGroupRateRepoHotpathStub{
		rate: &rate,
		wait: unblock,
	}
	svc := NewGroupRateResolver(repo, gocache.New(time.Minute, time.Minute), 30*time.Second, nil, "service.gateway", nil)

	const concurrent = 12
	results := make([]float64, concurrent)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrent)
	for i := 0; i < concurrent; i++ {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = svc.Resolve(context.Background(), 101, 202, 1.2)
		}(i)
	}

	close(start)
	// 等所有调用方都记录 cache miss 后再释放 loader，避免固定 sleep 与调度器竞争。
	// miss 计数正是下方断言的可观察条件，也能保证调用方已经进入 singleflight。
	require.Eventually(t, func() bool {
		_, miss, _, _, _ := GroupRateCacheStats()
		return miss == int64(concurrent)
	}, 5*time.Second, time.Millisecond, "所有调用方必须在释放 loader 前完成 cache miss")
	close(unblock)
	wg.Wait()

	for _, got := range results {
		require.Equal(t, rate, got)
	}
	require.Equal(t, int64(1), repo.calls.Load())

	// 再次读取应命中缓存，不再回源。
	got := svc.Resolve(context.Background(), 101, 202, 1.2)
	require.Equal(t, rate, got)
	require.Equal(t, int64(1), repo.calls.Load())

	hit, miss, load, sfShared, fallback := GroupRateCacheStats()
	require.GreaterOrEqual(t, hit, int64(1))
	require.Equal(t, int64(12), miss)
	require.Equal(t, int64(1), load)
	require.GreaterOrEqual(t, sfShared, int64(1))
	require.Equal(t, int64(0), fallback)
}

func TestGetUserGroupRateMultiplier_FallbackOnRepoError(t *testing.T) {
	resetGatewayRateStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("db down"),
	}
	svc := NewGroupRateResolver(repo, gocache.New(time.Minute, time.Minute), 30*time.Second, nil, "service.gateway", nil)

	got := svc.Resolve(context.Background(), 101, 202, 1.25)
	require.Equal(t, 1.25, got)
	require.Equal(t, int64(1), repo.calls.Load())

	_, _, _, _, fallback := GroupRateCacheStats()
	require.Equal(t, int64(1), fallback)
}

func TestGetUserGroupRateMultiplier_CacheHitAndNilRepo(t *testing.T) {
	resetGatewayRateStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("should not be called"),
	}
	svc := NewGroupRateResolver(repo, gocache.New(time.Minute, time.Minute), DefaultGroupRateCacheTTL, nil, "service.gateway", nil)
	key := "101:202"
	svc.cache.Set(key, 2.3, time.Minute)

	got := svc.Resolve(context.Background(), 101, 202, 1.1)
	require.Equal(t, 2.3, got)

	hit, miss, load, _, fallback := GroupRateCacheStats()
	require.Equal(t, int64(1), hit)
	require.Equal(t, int64(0), miss)
	require.Equal(t, int64(0), load)
	require.Equal(t, int64(0), fallback)
	require.Equal(t, int64(0), repo.calls.Load())

	// 无 repo 时直接返回分组默认倍率
	svc2 := NewGroupRateResolver(nil, gocache.New(time.Minute, time.Minute), DefaultGroupRateCacheTTL, nil, "service.gateway", nil)
	svc2.cache.Set(key, 1.9, time.Minute)
	require.Equal(t, 1.9, svc2.Resolve(context.Background(), 101, 202, 1.4))
	require.Equal(t, 1.4, svc2.Resolve(context.Background(), 0, 202, 1.4))
	svc2.cache.Delete(key)
	require.Equal(t, 1.4, svc2.Resolve(context.Background(), 101, 202, 1.4))
}

// 仅重置本组断言实际观察的原生倍率指标。
func resetGatewayRateStatsForTest() {
	m := SharedGroupRateMetrics()
	m.Hit.Store(0)
	m.Miss.Store(0)
	m.Load.Store(0)
	m.Shared.Store(0)
	m.Fallback.Store(0)
}
