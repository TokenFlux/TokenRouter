//go:build integration

package app

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// TestSessionAndWindowCachesKeepIndependentState 验证拆分端口后，应用仍使用原键与独立数据面。
func TestSessionAndWindowCachesKeepIndependentState(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	container, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 15*time.Second)
		defer stop()
		require.NoError(t, container.Terminate(cleanup))
	})
	endpoint, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	options, err := redis.ParseURL(endpoint)
	require.NoError(t, err)
	rdb := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, rdb.Close()) })

	sessions := provideSessionCache(rdb, &config.Config{})
	windows := provideWindowCostCache(rdb)
	const accountID int64 = 3301
	allowed, err := sessions.RegisterSession(ctx, accountID, "first", 1, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NoError(t, windows.SetWindowCost(ctx, accountID, 12.5))
	stored, err := rdb.Get(ctx, "window_cost:account:3301").Float64()
	require.NoError(t, err)
	require.Equal(t, 12.5, stored)
	ttl, err := rdb.PTTL(ctx, "window_cost:account:3301").Result()
	require.NoError(t, err)
	require.Positive(t, ttl)
	require.LessOrEqual(t, ttl, 30*time.Second)
	active, err := sessions.GetActiveSessionCount(ctx, accountID)
	require.NoError(t, err)
	require.Equal(t, 1, active)

	require.NoError(t, sessions.UnregisterSession(ctx, accountID, "first"))
	cost, hit, err := windows.GetWindowCost(ctx, accountID)
	require.NoError(t, err)
	require.True(t, hit)
	require.Equal(t, 12.5, cost)
	batch, err := windows.GetWindowCostBatch(ctx, []int64{accountID, accountID + 1})
	require.NoError(t, err)
	require.Equal(t, map[int64]float64{accountID: 12.5}, batch)
	allowed, err = sessions.RegisterSession(ctx, accountID, "second", 1, time.Minute)
	require.NoError(t, err)
	require.True(t, allowed)
}
