//go:build integration

// 真实 Redis 验证 B03 取消后不回读，也不释放其他持有者的刷新锁。
package repository

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	accountmodule "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/stretchr/testify/require"
)

type vertexCancelAfterLockCache struct {
	accountmodule.AccessTokenCache
	cancel context.CancelFunc
	reads  atomic.Int32
}

func (c *vertexCancelAfterLockCache) GetAccessToken(ctx context.Context, key string) (string, error) {
	c.reads.Add(1)
	return c.AccessTokenCache.GetAccessToken(ctx, key)
}
func (c *vertexCancelAfterLockCache) AcquireRefreshLock(ctx context.Context, key string, ttl time.Duration) (bool, error) {
	locked, err := c.AccessTokenCache.AcquireRefreshLock(ctx, key, ttl)
	c.cancel()
	return locked, err
}

func TestS09VertexLockCancellationWithRedis(t *testing.T) {
	cache := rediscache.NewOAuthTokenCache(testRedis(t))
	key := accountmodule.VertexServiceAccountCacheKey(99, "s09-vertex-fixture", t.Name(), true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	locked, err := cache.AcquireRefreshLock(ctx, key, 30*time.Second)
	require.NoError(t, err)
	require.True(t, locked)
	defer func() { require.NoError(t, cache.ReleaseRefreshLock(context.Background(), key)) }()
	canceled, stop := context.WithCancel(ctx)
	defer stop()
	tracked := &vertexCancelAfterLockCache{AccessTokenCache: cache, cancel: stop}
	var exchanges atomic.Int32
	token, err := accountmodule.GetVertexServiceAccountAccessToken(canceled, accountmodule.VertexTokenOptions{AccountID: 99, CacheKey: key, Cache: tracked, Exchange: func(context.Context) (string, time.Duration, error) {
		exchanges.Add(1)
		return "unexpected", time.Hour, nil
	}})
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, token)
	require.EqualValues(t, 1, tracked.reads.Load())
	require.Zero(t, exchanges.Load())
	locked, err = cache.AcquireRefreshLock(ctx, key, 30*time.Second)
	require.NoError(t, err)
	require.False(t, locked, "取消者不能释放其他持有者的锁")
	require.NoError(t, cache.SetAccessToken(ctx, key, "peer-token", 55*time.Minute))
	defer func() { require.NoError(t, cache.DeleteAccessToken(context.Background(), key)) }()
	token, err = accountmodule.GetVertexServiceAccountAccessToken(ctx, accountmodule.VertexTokenOptions{CacheKey: key, Cache: cache, Exchange: func(context.Context) (string, time.Duration, error) {
		exchanges.Add(1)
		return "unexpected", time.Hour, nil
	}})
	require.NoError(t, err)
	require.Equal(t, "peer-token", token)
	require.Zero(t, exchanges.Load())
}
