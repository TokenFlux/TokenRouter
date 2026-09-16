//go:build integration

package rediscache

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"
)

// 隔离 Redis 验证旧会话 JSON/键/TTL 与两个 Store 的一次性消费兼容。
func TestGrokSessionsRedisWireAndSingleConsumption(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	container, err := tcredis.Run(ctx, "redis:8.4-alpine")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, container.Terminate(context.Background())) })
	uri, err := container.ConnectionString(ctx)
	require.NoError(t, err)
	options, err := redis.ParseURL(uri)
	require.NoError(t, err)
	client := redis.NewClient(options)
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	first := NewGrokSessionStore(client)
	second := NewGrokSessionStore(client)
	t.Cleanup(first.Stop)
	t.Cleanup(second.Stop)
	original := &account.GrokOAuthSession{State: "fixture", CodeVerifier: "verifier", CreatedAt: time.Now().UTC()}
	encoded, err := json.Marshal(original)
	require.NoError(t, err)
	require.NoError(t, client.Set(ctx, "oauth:session:xai:old-wire", encoded, account.GrokSessionTTL).Err())
	value, ok := first.Get("old-wire")
	require.True(t, ok)
	require.Equal(t, original.State, value.State)
	require.Equal(t, original.CodeVerifier, value.CodeVerifier)
	require.WithinDuration(t, original.CreatedAt, value.CreatedAt, time.Nanosecond)
	first.Set("new-wire", original)
	raw, err := client.Get(ctx, "oauth:session:xai:new-wire").Bytes()
	require.NoError(t, err)
	var decoded account.GrokOAuthSession
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Equal(t, original.State, decoded.State)
	ttl, err := client.TTL(ctx, "oauth:session:xai:new-wire").Result()
	require.NoError(t, err)
	require.Greater(t, ttl, account.GrokSessionTTL-time.Minute)
	require.LessOrEqual(t, ttl, account.GrokSessionTTL)
	var claimed atomic.Int64
	var workers sync.WaitGroup
	for i := range 32 {
		workers.Go(func() {
			store := first
			if i%2 == 1 {
				store = second
			}
			if store.TryConsumeSession("new-wire") {
				claimed.Add(1)
			}
		})
	}
	workers.Wait()
	require.EqualValues(t, 1, claimed.Load())
	marker, err := client.Get(ctx, "oauth:session:xai:used:new-wire").Result()
	require.NoError(t, err)
	require.Equal(t, "1", marker)
	require.False(t, second.TryConsumeSession("new-wire"))
	require.NoError(t, client.Del(ctx, "oauth:session:xai:new-wire").Err())
	_, ok = first.Get("new-wire")
	require.False(t, ok, "远端删除不能复活本地副本")
}
