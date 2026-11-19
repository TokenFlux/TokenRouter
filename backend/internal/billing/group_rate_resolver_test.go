package billing

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/querycache"
)

// userGroupRateRepoHotpathStub holds one delayed lookup result for concurrent
// resolver tests.
type userGroupRateRepoHotpathStub struct {
	// UserGroupRateRepository fills unused methods for this narrow lookup
	// fixture. Tests invoke only the overridden method; calling an embedded
	// method on the nil interface would panic.
	UserGroupRateRepository // unused repository fixture methods

	// rate, err and wait are configured before concurrent resolution begins and
	// remain stable during the blocked load. calls records attempts with atomic
	// updates for concurrent assertions.
	rate *float64 // configured multiplier response pointer
	// err overrides the rate response when non-nil. Configure it before resolving
	// so concurrent callers observe stable fixture inputs.
	err error // injected multiplier read error
	// wait blocks the shared repository load until released by the test. A nil
	// channel skips waiting, and ctx cancellation does not interrupt this fixture
	// wait.
	wait  <-chan struct{} // shared load release signal
	calls atomic.Int64    // concurrent lookup invocation count
}

// userGroupRateResolverRepoStub counts synchronous multiplier loads and
// supplies one lookup result.
type userGroupRateResolverRepoStub struct {
	// UserGroupRateRepository fills unused methods for this narrow lookup
	// fixture. Tests invoke only the overridden method; calling an embedded
	// method on the nil interface would panic.
	UserGroupRateRepository // unused repository fixture methods

	rate *float64 // configured multiplier response pointer
	// err overrides the rate response when non-nil. Configure it before resolving
	// so concurrent callers observe stable fixture inputs.
	err error // injected multiplier read error
	// calls records synchronous lookups with an ordinary integer. This fixture is
	// used by a single resolver caller, so concurrent mutation requires a
	// different counter.
	calls int // synchronous lookup invocation count
}

// TestGetUserGroupRateMultiplier_UsesCacheAndSingleflight blocks a repository
// read while twelve callers miss the same cache key. It checks one shared
// load, identical results and a subsequent cache hit with the expected shared
// counters.
func TestGetUserGroupRateMultiplier_UsesCacheAndSingleflight(t *testing.T) {
	resetGatewayRateStatsForTest()

	rate := 1.7
	unblock := make(chan struct{})
	repo := &userGroupRateRepoHotpathStub{
		rate: &rate,
		wait: unblock,
	}
	svc := NewGroupRateResolver(repo, querycache.NewTTLMap[float64](time.Minute), 30*time.Second, nil, "service.gateway", nil)

	const concurrent = 12
	results := make([]float64, concurrent)
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(concurrent)
	// Each caller waits for the common start signal and writes its own result
	// index, allowing concurrent misses without sharing result slots.
	for i := range concurrent {
		go func(idx int) {
			defer wg.Done()
			<-start
			results[idx] = svc.Resolve(context.Background(), 101, 202, 1.2)
		}(i)
	}

	// Release every caller before observing misses; keeping the repository
	// blocked until that count completes makes the singleflight overlap
	// reproducible.
	close(start)
	// Keep the repository read blocked until all callers have recorded misses, so
	// their lookup attempts overlap and exercise the shared-load path.
	require.Eventually(t, func() bool {
		_, miss, _, _, _ := GroupRateCacheStats()
		return miss == int64(concurrent)
	}, 5*time.Second, time.Millisecond, "所有调用方必须在释放 loader 前完成 cache miss")
	// All callers have reached the miss path; release the shared read and wait
	// for every result before inspecting the shared counters.
	close(unblock)
	wg.Wait()

	for _, got := range results {
		require.Equal(t, rate, got)
	}
	require.Equal(t, int64(1), repo.calls.Load())

	// A subsequent lookup uses the populated cache.
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

// TestGetUserGroupRateMultiplier_FallbackOnRepoError returns a repository
// failure during multiplier resolution. It checks the caller's group default,
// one load attempt and an incremented fallback counter.
func TestGetUserGroupRateMultiplier_FallbackOnRepoError(t *testing.T) {
	resetGatewayRateStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("db down"),
	}
	svc := NewGroupRateResolver(repo, querycache.NewTTLMap[float64](time.Minute), 30*time.Second, nil, "service.gateway", nil)

	got := svc.Resolve(context.Background(), 101, 202, 1.25)
	require.Equal(t, 1.25, got)
	require.Equal(t, int64(1), repo.calls.Load())

	_, _, _, _, fallback := GroupRateCacheStats()
	require.Equal(t, int64(1), fallback)
}

// TestGetUserGroupRateMultiplier_CacheHitAndNilRepo preloads multipliers with
// a failing or absent repository. It checks usable cache hits, default
// selection for invalid IDs and default selection after the cached entry is
// removed.
func TestGetUserGroupRateMultiplier_CacheHitAndNilRepo(t *testing.T) {
	resetGatewayRateStatsForTest()

	repo := &userGroupRateRepoHotpathStub{
		err: errors.New("should not be called"),
	}
	svc := NewGroupRateResolver(repo, querycache.NewTTLMap[float64](time.Minute), DefaultGroupRateCacheTTL, nil, "service.gateway", nil)
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

	// A cached value remains usable without a repository; invalid IDs return the group default.
	svc2 := NewGroupRateResolver(nil, querycache.NewTTLMap[float64](time.Minute), DefaultGroupRateCacheTTL, nil, "service.gateway", nil)
	svc2.cache.Set(key, 1.9, time.Minute)
	require.Equal(t, 1.9, svc2.Resolve(context.Background(), 101, 202, 1.4))
	require.Equal(t, 1.4, svc2.Resolve(context.Background(), 0, 202, 1.4))
	svc2.cache.Delete(key)
	require.Equal(t, 1.4, svc2.Resolve(context.Background(), 101, 202, 1.4))
}

// TestNewUserGroupRateResolver_Defaults constructs a resolver with nil
// dependencies and a nonpositive TTL. It checks allocated cache/singleflight
// instances, the default entry lifetime and the gateway log component.
func TestNewUserGroupRateResolver_Defaults(t *testing.T) {
	resolver := NewGroupRateResolver(nil, nil, 0, nil, "", nil)

	require.NotNil(t, resolver)
	require.NotNil(t, resolver.cache)
	require.Equal(t, defaultUserGroupRateCacheTTL, resolver.cacheTTL)
	require.NotNil(t, resolver.sf)
	require.Equal(t, "service.gateway", resolver.logComponent)
}

// TestUserGroupRateResolverResolve_FallbackForNilResolverAndInvalidIDs calls
// resolution on a nil receiver and on nonpositive identities. It checks that
// each path returns the supplied group default.
func TestUserGroupRateResolverResolve_FallbackForNilResolverAndInvalidIDs(t *testing.T) {
	var nilResolver *GroupRateResolver
	require.Equal(t, 1.4, nilResolver.Resolve(context.Background(), 101, 202, 1.4))

	resolver := NewGroupRateResolver(nil, nil, time.Second, nil, "service.test", nil)
	require.Equal(t, 1.4, resolver.Resolve(context.Background(), 0, 202, 1.4))
	require.Equal(t, 1.4, resolver.Resolve(context.Background(), 101, 0, 1.4))
}

// TestUserGroupRateResolverResolve_ExpiredCacheEntryLoadsRepoAndCaches stores
// a short-lived multiplier entry and waits past its expiration deadline.
// Resolving the expired key must reload the repository once, cache the fresh override, and update miss/load counters.
func TestUserGroupRateResolverResolve_ExpiredCacheEntryLoadsRepoAndCaches(t *testing.T) {
	// Reset the global counters before this synchronous case begins, so previous
	// resolver activity cannot supply the expected hit/miss totals.
	groupRateMetrics.Hit.Store(0)
	groupRateMetrics.Miss.Store(0)
	groupRateMetrics.Load.Store(0)
	groupRateMetrics.Shared.Store(0)
	groupRateMetrics.Fallback.Store(0)

	rate := 1.7
	repo := &userGroupRateResolverRepoStub{rate: &rate}
	cache := querycache.NewTTLMap[float64](time.Minute)
	cache.Set("101:202", 0.5, 10*time.Millisecond)
	time.Sleep(25 * time.Millisecond)
	resolver := NewGroupRateResolver(repo, cache, time.Minute, nil, "service.test", nil)

	got := resolver.Resolve(context.Background(), 101, 202, 1.2)
	require.Equal(t, rate, got)
	require.Equal(t, 1, repo.calls)

	cached, ok := cache.Get("101:202")
	require.True(t, ok)
	require.Equal(t, rate, cached)

	hit, miss, load, _, fallback := GroupRateCacheStats()
	require.Equal(t, int64(0), hit)
	require.Equal(t, int64(1), miss)
	require.Equal(t, int64(1), load)
	require.Equal(t, int64(0), fallback)
}

// GetByUserAndGroup increments the atomic call count and waits for the fixture
// channel when configured. The wait ignores ctx, then returns the configured
// error or rate to hold concurrent resolver callers at one shared load.
func (s *userGroupRateRepoHotpathStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls.Add(1)
	// Hold the repository outcome until the test has observed every miss, so
	// singleflight sharing is exercised before any cache entry can be populated.
	if s.wait != nil {
		<-s.wait
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

// resetGatewayRateStatsForTest zeros the shared resolver counters used by
// these assertions. Call it while no resolver load is active, since each
// atomic reset occurs separately and does not create an isolated test
// snapshot.
func resetGatewayRateStatsForTest() {
	m := SharedGroupRateMetrics()
	// Reset all shared counters before the next case starts; the test's quiet
	// resolver state is required because these stores are separate operations.
	m.Hit.Store(0)
	m.Miss.Store(0)
	m.Load.Store(0)
	m.Shared.Store(0)
	m.Fallback.Store(0)
}

// GroupRateCacheStats reads cumulative hit, miss, load, shared and fallback
// counters. Each load is atomic, but concurrent resolver activity can advance
// other counters between the individual loads in this tuple.
func GroupRateCacheStats() (int64, int64, int64, int64, int64) {
	return groupRateMetrics.Hit.Load(), groupRateMetrics.Miss.Load(), groupRateMetrics.Load.Load(), groupRateMetrics.Shared.Load(), groupRateMetrics.Fallback.Load()
}

// GetByUserAndGroup counts a synchronous lookup and returns the configured
// error or rate. This stub uses an ordinary counter and belongs to the
// single-caller invalid-cache test, which avoids concurrent access.
func (s *userGroupRateResolverRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}
