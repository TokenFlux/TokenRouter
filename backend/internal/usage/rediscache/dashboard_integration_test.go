//go:build integration

package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/usage"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// 原键可直接读入新实现，新写入也可由原始 Redis 客户端按旧键读取。
func TestDashboardCacheLegacyKeyAndTTL(t *testing.T) {
	ctx := context.Background()
	container, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { _ = container.Terminate(ctx) })
	url, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	options, err := redis.ParseURL(url)
	require.NoError(t, err)
	raw := redis.NewClient(options)
	t.Cleanup(func() { _ = raw.Close() })
	cache := NewDashboardCache(raw, "prod")
	const key = "prod:dashboard:stats:v1"
	const legacy = `{"total_requests":7,"total_actual_cost":0.125}`
	require.NoError(t, raw.Set(ctx, key, legacy, time.Minute).Err())
	value, err := cache.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.Equal(t, legacy, value)
	const next = `{"total_requests":8}`
	require.NoError(t, cache.SetDashboardStats(ctx, next, 2*time.Minute))
	value, err = raw.Get(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, next, value)
	ttl, err := raw.PTTL(ctx, key).Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Minute)
	require.LessOrEqual(t, ttl, 2*time.Minute)
	require.NoError(t, cache.DeleteDashboardStats(ctx))
	_, err = cache.GetDashboardStats(ctx)
	require.ErrorIs(t, err, usage.ErrDashboardStatsCacheMiss)
	unprefixed := NewDashboardCache(raw, "")
	require.NoError(t, unprefixed.SetDashboardStats(ctx, next, time.Minute))
	value, err = raw.Get(ctx, "dashboard:stats:v1").Result()
	require.NoError(t, err)
	require.Equal(t, next, value)
}
