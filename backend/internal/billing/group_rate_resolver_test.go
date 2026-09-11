package billing

import (
	"context"
	"testing"
	"time"

	gocache "github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/require"
)

type userGroupRateResolverRepoStub struct {
	UserGroupRateRepository

	rate  *float64
	err   error
	calls int
}

func (s *userGroupRateResolverRepoStub) GetByUserAndGroup(ctx context.Context, userID, groupID int64) (*float64, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.rate, nil
}

func TestNewUserGroupRateResolver_Defaults(t *testing.T) {
	resolver := NewGroupRateResolver(nil, nil, 0, nil, "", nil)

	require.NotNil(t, resolver)
	require.NotNil(t, resolver.cache)
	require.Equal(t, defaultUserGroupRateCacheTTL, resolver.cacheTTL)
	require.NotNil(t, resolver.sf)
	require.Equal(t, "service.gateway", resolver.logComponent)
}

func TestUserGroupRateResolverResolve_FallbackForNilResolverAndInvalidIDs(t *testing.T) {
	var nilResolver *GroupRateResolver
	require.Equal(t, 1.4, nilResolver.Resolve(context.Background(), 101, 202, 1.4))

	resolver := NewGroupRateResolver(nil, nil, time.Second, nil, "service.test", nil)
	require.Equal(t, 1.4, resolver.Resolve(context.Background(), 0, 202, 1.4))
	require.Equal(t, 1.4, resolver.Resolve(context.Background(), 101, 0, 1.4))
}

func TestUserGroupRateResolverResolve_InvalidCacheEntryLoadsRepoAndCaches(t *testing.T) {
	groupRateMetrics.Hit.Store(0)
	groupRateMetrics.Miss.Store(0)
	groupRateMetrics.Load.Store(0)
	groupRateMetrics.Shared.Store(0)
	groupRateMetrics.Fallback.Store(0)

	rate := 1.7
	repo := &userGroupRateResolverRepoStub{rate: &rate}
	cache := gocache.New(time.Minute, time.Minute)
	cache.Set("101:202", "bad-cache", time.Minute)
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
