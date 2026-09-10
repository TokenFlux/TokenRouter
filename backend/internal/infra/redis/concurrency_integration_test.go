//go:build integration

package redis

import (
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/redis/session"

	"github.com/stretchr/testify/require"
)

// TestFixedWindowConcurrentCounts 验证真实 Redis 中每次计数唯一，首批配额与 TTL 不因并发改变。
func TestFixedWindowConcurrentCounts(t *testing.T) {
	client := startRedis(t, t.Context())
	limiter := NewFixedWindowLimiter(client, "rate_limit:")
	type result struct {
		allowed bool
		count   int64
		err     error
	}
	results := make(chan result, 32)
	var group sync.WaitGroup
	for range 32 {
		group.Add(1)
		go func() {
			defer group.Done()
			allowed, count, _, err := limiter.Allow(t.Context(), "concurrent", 16, 2*time.Second)
			results <- result{allowed, count, err}
		}()
	}
	group.Wait()
	close(results)
	counts := map[int64]bool{}
	allowed := 0
	for value := range results {
		require.NoError(t, value.err)
		counts[value.count] = true
		if value.allowed {
			allowed++
		}
	}
	require.Len(t, counts, 32)
	require.True(t, counts[1])
	require.True(t, counts[32])
	require.Equal(t, 16, allowed)
	ttl, err := client.PTTL(t.Context(), "rate_limit:concurrent").Result()
	require.NoError(t, err)
	require.Greater(t, ttl, time.Duration(0))
	require.LessOrEqual(t, ttl, 2*time.Second)
}

// TestRedisSessionConcurrentConsumption 验证新旧实例共享 JSON/键格式且只能有一个领取者。
func TestRedisSessionConcurrentConsumption(t *testing.T) {
	client := startRedis(t, t.Context())
	first := session.New(client, "s01:session", time.Minute)
	second := session.New(client, "s01:session:", time.Minute)
	expected := map[string]string{"state": "opaque", "verifier": "test-value"}
	require.NoError(t, first.Set(t.Context(), " id ", expected))
	var decoded map[string]string
	found, err := second.Get(t.Context(), "id", &decoded)
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, expected, decoded)
	type result struct {
		claimed bool
		err     error
	}
	results := make(chan result, 16)
	var group sync.WaitGroup
	for range 16 {
		group.Add(1)
		go func() {
			defer group.Done()
			claimed, err := second.TryConsume(t.Context(), "id")
			results <- result{claimed, err}
		}()
	}
	group.Wait()
	close(results)
	winners := 0
	for value := range results {
		require.NoError(t, value.err)
		if value.claimed {
			winners++
		}
	}
	require.Equal(t, 1, winners)
	require.NoError(t, first.Delete(t.Context(), "id"))
	found, err = second.Get(t.Context(), "id", &decoded)
	require.NoError(t, err)
	require.False(t, found)
	exists, err := client.Exists(t.Context(), "s01:session:id", "s01:session:used:id").Result()
	require.NoError(t, err)
	require.Zero(t, exists)
}
