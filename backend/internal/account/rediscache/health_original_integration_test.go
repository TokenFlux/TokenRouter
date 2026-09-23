//go:build integration

package rediscache_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/testutil/rediscontainer"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	"github.com/stretchr/testify/require"
)

// 独立缓存实例共享原 Redis 命名空间和 Lua 语义，不创建第二套计数或限流状态。
func TestS06AccountHealthRedisCompatibility(t *testing.T) {
	client := rediscontainer.New(t)
	ctx := context.Background()
	t.Run("internal500_count_and_fixed_ttl", func(t *testing.T) {
		first := rediscache.NewInternal500CounterCache(client)
		second := rediscache.NewInternal500CounterCache(client)
		key := "internal500_count:account:905"
		count, err := first.IncrementInternal500Count(ctx, 905)
		require.NoError(t, err)
		require.EqualValues(t, 1, count)
		ttl, err := client.TTL(ctx, key).Result()
		require.NoError(t, err)
		require.Greater(t, ttl, 23*time.Hour)
		require.LessOrEqual(t, ttl, 24*time.Hour)
		require.NoError(t, client.PExpire(ctx, key, 20*time.Second).Err())
		count, err = second.IncrementInternal500Count(ctx, 905)
		require.NoError(t, err)
		require.EqualValues(t, 2, count)
		ttl, err = client.PTTL(ctx, key).Result()
		require.NoError(t, err)
		require.Greater(t, ttl, 15*time.Second)
		require.LessOrEqual(t, ttl, 20*time.Second)
		require.NoError(t, first.ResetInternal500Count(ctx, 905))
		require.Zero(t, client.Exists(ctx, key).Val())
	})
	t.Run("403_count_and_fixed_ttl", func(t *testing.T) {
		old := rediscache.NewOpenAI403CounterCache(client)
		current := rediscache.NewOpenAI403CounterCache(client)
		key := "openai_403_count:account:901"
		n, err := old.IncrementOpenAI403Count(ctx, 901, 0)
		require.NoError(t, err)
		require.EqualValues(t, 1, n)
		ttl, err := client.TTL(ctx, key).Result()
		require.NoError(t, err)
		require.LessOrEqual(t, ttl, time.Minute)
		require.Greater(t, ttl, 50*time.Second)
		require.NoError(t, client.PExpire(ctx, key, 20*time.Second).Err())
		n, err = current.IncrementOpenAI403Count(ctx, 901, 5)
		require.NoError(t, err)
		require.EqualValues(t, 2, n)
		ttl, err = client.PTTL(ctx, key).Result()
		require.NoError(t, err)
		require.LessOrEqual(t, ttl, 20*time.Second)
		require.Greater(t, ttl, 15*time.Second)
		require.NoError(t, current.ResetOpenAI403Count(ctx, 901))
		require.Zero(t, client.Exists(ctx, key).Val())
	})
	t.Run("timeout_concurrent_shared_counter", func(t *testing.T) {
		stores := []account.TimeoutCounterCache{rediscache.NewTimeoutCounterCache(client), rediscache.NewTimeoutCounterCache(client)}
		type result struct {
			count int64
			err   error
		}
		results := make(chan result, 40)
		var wg sync.WaitGroup
		for i := range 40 {
			wg.Go(func() { n, err := stores[i%2].IncrementTimeoutCount(ctx, 902, 1); results <- result{n, err} })
		}
		wg.Wait()
		close(results)
		values := make([]int, 0, 40)
		for result := range results {
			require.NoError(t, result.err)
			values = append(values, int(result.count))
		}
		sort.Ints(values)
		for i, v := range values {
			require.Equal(t, i+1, v)
		}
		require.NoError(t, client.PExpire(ctx, "timeout_count:account:902", 20*time.Second).Err())
		n, err := stores[0].IncrementTimeoutCount(ctx, 902, 8)
		require.NoError(t, err)
		require.EqualValues(t, 41, n)
		ttl, err := stores[1].GetTimeoutCountTTL(ctx, 902)
		require.NoError(t, err)
		require.LessOrEqual(t, ttl, 20*time.Second)
		require.NoError(t, stores[1].ResetTimeoutCount(ctx, 902))
		n, err = stores[0].GetTimeoutCount(ctx, 902)
		require.NoError(t, err)
		require.Zero(t, n)
	})
	t.Run("temporary_state_extends_only", func(t *testing.T) {
		old := rediscache.NewTempUnschedCache(client)
		current := rediscache.NewTempUnschedCache(client)
		now := time.Now().Unix()
		longer := &account.TempUnschedState{UntilUnix: now + 300, StatusCode: 429, ErrorMessage: "long"}
		require.NoError(t, old.SetTempUnsched(ctx, 903, longer))
		require.NoError(t, current.SetTempUnsched(ctx, 903, &account.TempUnschedState{UntilUnix: now + 100, StatusCode: 403}))
		observed, err := current.GetTempUnsched(ctx, 903)
		require.NoError(t, err)
		require.Equal(t, longer, observed)
		require.NoError(t, current.DeleteTempUnsched(ctx, 903))
		observed, err = old.GetTempUnsched(ctx, 903)
		require.NoError(t, err)
		require.Nil(t, observed)
		require.NoError(t, client.Set(ctx, "temp_unsched:account:903", "broken", time.Minute).Err())
		_, err = current.GetTempUnsched(ctx, 903)
		require.Error(t, err)
	})
	t.Run("rolling_threshold_keeps_legacy_keys", func(t *testing.T) {
		old, ok := rediscache.NewTempUnschedCache(client).(account.OpenAIAPIKeyHealthCache)
		require.True(t, ok)
		current, ok := rediscache.NewTempUnschedCache(client).(account.OpenAIAPIKeyHealthCache)
		require.True(t, ok)
		for i := 1; i <= 3; i++ {
			store := old
			if i == 2 {
				store = current
			}
			n, tripped, err := store.RecordOpenAIAPIKeyHealthFailure(ctx, 904, 1, 3)
			require.NoError(t, err)
			require.EqualValues(t, i, n)
			require.Equal(t, i == 3, tripped)
		}
		require.Zero(t, client.Exists(ctx, fmt.Sprintf("openai_apikey_health:{%d}:failures", 904), fmt.Sprintf("openai_apikey_health:{%d}:failures:sequence", 904)).Val())
	})
}
