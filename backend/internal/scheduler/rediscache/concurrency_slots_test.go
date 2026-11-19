package rediscache

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// Batch concurrency and load tests exercise ordinary and Live sorted-set
// pruning cutoffs, command ordering, duplicate identities, and per-command
// error propagation when an earlier wait key returns redis.Nil. An optional
// Redis benchmark compares sorted-set counting with SCAN traversal.

// benchSlotTTLMinutes selects the ordinary activity window used to prune
// seeded members during timed counts; extended runs can outlive those
// fixtures.
const benchSlotTTLMinutes = 15

// benchSlotTTL supplies expiry for benchmark fixtures so seeded sorted sets
// and individual SCAN keys share the same configured retention interval.
var benchSlotTTL = time.Duration(benchSlotTTLMinutes) * time.Minute

// concurrencyBatchEntry identifies a resource's batch-count entry point and keys.
type concurrencyBatchEntry struct {
	name string // resource subtest display name
	// ordinaryPrefix, livePrefix and count select one resource's storage and
	// batch entry point together, so both resource cases run the same observations
	// against their own keys.
	ordinaryPrefix string // ordinary resource set prefix
	livePrefix     string // Live resource set prefix
	// count invokes this resource's batch count method.
	count func(*concurrencyCache, context.Context, []int64) (map[int64]int, error)
}

// concurrencyBatchCommandHook records adapter commands and supplies selected failures.
type concurrencyBatchCommandHook struct {
	// commands snapshots arguments when hooks observe them, so subsequent
	// command mutation cannot change the recorded sequence used by assertions.
	commands      [][]any // copied observed command arguments
	pipelineCalls int     // observed pipeline execution count
	// timeErr replaces the TIME operation when set, stopping the batch before
	// its sorted-set commands can execute.
	timeErr error
	// pipelineErr replaces pipeline execution's aggregate error when set;
	// countValues can still supply results for the redis.Nil case.
	pipelineErr error
	// countValues supplies cardinality replies in command order when populated.
	// Each ordinary and Live ZCARD consumes one value, allowing repeated IDs to
	// return different observations within a single batch.
	countValues []int64
}

// DialHook leaves fixture connections under the Redis client's usual policy.
func (h *concurrencyBatchCommandHook) DialHook(next redis.DialHook) redis.DialHook {
	return next
}

// ProcessHook records TIME arguments and injects a configured clock failure.
// Other commands continue through the fixture client so setup and membership
// assertions use the stored Redis data.
func (h *concurrencyBatchCommandHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		if cmd.Name() == "time" {
			h.commands = append(h.commands, append([]any(nil), cmd.Args()...))
			if h.timeErr != nil {
				cmd.SetErr(h.timeErr)
				return h.timeErr
			}
		}
		return next(ctx, cmd)
	}
}

// ProcessPipelineHook records command order before supplying configured replies.
// Populated countValues bypass storage execution, and pipelineErr controls the
// aggregate error independently of the supplied cardinality values.
func (h *concurrencyBatchCommandHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		h.pipelineCalls++
		for _, cmd := range cmds {
			h.commands = append(h.commands, append([]any(nil), cmd.Args()...))
		}
		if h.countValues != nil || h.pipelineErr != nil {
			countIndex := 0
			for _, cmd := range cmds {
				countCmd, ok := cmd.(*redis.IntCmd)
				if ok && cmd.Name() == "zcard" && h.countValues != nil {
					countCmd.SetVal(h.countValues[countIndex])
					countIndex++
				}
			}
			return h.pipelineErr
		}
		return next(ctx, cmds)
	}
}

// concurrencyBatchEntries returns both resource entry points with their storage prefixes.
func concurrencyBatchEntries() []concurrencyBatchEntry {
	return []concurrencyBatchEntry{
		{
			name:           "provider",
			ordinaryPrefix: providerSlotKeyPrefix,
			livePrefix:     liveProviderSlotKeyPrefix,
			count:          (*concurrencyCache).GetProviderConcurrencyBatch,
		},
		{
			name:           "api_key",
			ordinaryPrefix: apiKeySlotKeyPrefix,
			livePrefix:     liveAPIKeySlotKeyPrefix,
			count:          (*concurrencyCache).GetAPIKeyConcurrencyBatch,
		},
	}
}

// newConcurrencyBatchTestCache returns a cache with a two-minute ordinary window.
// The fixture owns its client and Redis server; both close after the test,
// while the shorter Live window remains the production sixty-second value.
func newConcurrencyBatchTestCache(t *testing.T) (*concurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	// The fixture owns this client, so close it before miniredis teardown and
	// report cleanup failures alongside the test's command assertions.
	t.Cleanup(func() {
		require.NoError(t, client.Close())
	})
	// Client initialization sends metadata in its own pipeline; complete it
	// before hooks record the adapter's TIME and batch commands.
	require.NoError(t, client.Ping(context.Background()).Err())
	cache, ok := NewConcurrencyCache(client, 2, 30).(*concurrencyCache)
	require.True(t, ok)
	return cache, server
}

// TestConcurrencyBatchOrdinaryAndLiveCounts seeds members around both expiration
// cutoffs for each resource. It checks inclusive pruning, signed and duplicate
// identities, one Redis clock sample, command order and unchanged key lifetimes.
func TestConcurrencyBatchOrdinaryAndLiveCounts(t *testing.T) {
	for _, entry := range concurrencyBatchEntries() {
		t.Run(entry.name, func(t *testing.T) {
			cache, server := newConcurrencyBatchTestCache(t)
			ctx := context.Background()
			now := time.Unix(1_700_000_000, 750_000_000)
			server.SetTime(now)
			ordinaryKey := entry.ordinaryPrefix + "10"
			liveKey := entry.livePrefix + "10"
			signedLiveKey := entry.livePrefix + "-20"
			// Fixture cutoffs use the two-minute ordinary and sixty-second Live windows.
			ordinaryCutoff := now.Unix() - 120
			liveCutoff := now.Unix() - 60
			require.NoError(t, cache.rdb.ZAdd(ctx, ordinaryKey,
				redis.Z{Score: float64(ordinaryCutoff - 1), Member: "ordinary-expired"},
				redis.Z{Score: float64(ordinaryCutoff), Member: "ordinary-cutoff"},
				redis.Z{Score: float64(ordinaryCutoff + 1), Member: "ordinary-retained"},
				redis.Z{Score: float64(now.Unix()), Member: "ordinary-current"},
			).Err())
			require.NoError(t, cache.rdb.ZAdd(ctx, liveKey,
				redis.Z{Score: float64(liveCutoff - 1), Member: "live-expired"},
				redis.Z{Score: float64(liveCutoff), Member: "live-cutoff"},
				redis.Z{Score: float64(liveCutoff + 1), Member: "live-retained"},
			).Err())
			require.NoError(t, cache.rdb.ZAdd(ctx, signedLiveKey,
				redis.Z{Score: float64(now.Unix()), Member: "signed-live-first"},
				redis.Z{Score: float64(now.Unix()), Member: "signed-live-second"},
			).Err())
			server.SetTTL(ordinaryKey, 40*time.Second)
			server.SetTTL(liveKey, 45*time.Second)
			server.SetTTL(signedLiveKey, 50*time.Second)
			hook := &concurrencyBatchCommandHook{}
			cache.rdb.AddHook(hook)

			ids := []int64{10, -20, 0, 10}
			counts, err := entry.count(cache, ctx, ids)
			require.NoError(t, err)
			require.Equal(t, map[int64]int{10: 3, -20: 2, 0: 0}, counts)
			require.Equal(t, 1, hook.pipelineCalls)
			wantCommands := [][]any{{"time"}}
			for _, id := range ids {
				wantCommands = append(wantCommands,
					[]any{"zremrangebyscore", fmt.Sprintf("%s%d", entry.ordinaryPrefix, id), "-inf", "1699999880"},
					[]any{"zremrangebyscore", fmt.Sprintf("%s%d", entry.livePrefix, id), "-inf", "1699999940"},
					[]any{"zcard", fmt.Sprintf("%s%d", entry.ordinaryPrefix, id)},
					[]any{"zcard", fmt.Sprintf("%s%d", entry.livePrefix, id)},
				)
			}
			require.Equal(t, wantCommands, hook.commands)
			require.Equal(t, 40*time.Second, server.TTL(ordinaryKey))
			require.Equal(t, 45*time.Second, server.TTL(liveKey))
			require.Equal(t, 50*time.Second, server.TTL(signedLiveKey))
			require.Equal(t, []int64{10, -20, 0, 10}, ids)
			ordinaryMembers, err := cache.rdb.ZRange(ctx, ordinaryKey, 0, -1).Result()
			require.NoError(t, err)
			require.Equal(t, []string{"ordinary-retained", "ordinary-current"}, ordinaryMembers)
			liveMembers, err := cache.rdb.ZRange(ctx, liveKey, 0, -1).Result()
			require.NoError(t, err)
			require.Equal(t, []string{"live-retained"}, liveMembers)
		})
	}
}

// TestConcurrencyBatchEmptyIDs supplies nil and allocated empty input slices.
// Both entry points must return allocated empty maps before issuing any Redis
// commands, even when clock operations are configured to fail.
func TestConcurrencyBatchEmptyIDs(t *testing.T) {
	for _, entry := range concurrencyBatchEntries() {
		t.Run(entry.name, func(t *testing.T) {
			cache, _ := newConcurrencyBatchTestCache(t)
			hook := &concurrencyBatchCommandHook{timeErr: errors.New("unexpected TIME")}
			cache.rdb.AddHook(hook)
			for _, ids := range [][]int64{nil, {}} {
				counts, err := entry.count(cache, context.Background(), ids)
				require.NoError(t, err)
				require.NotNil(t, counts)
				require.Empty(t, counts)
			}
			require.Empty(t, hook.commands)
			require.Zero(t, hook.pipelineCalls)
		})
	}
}

// TestConcurrencyBatchDuplicateIDs supplies different replies for repeated
// identities within one pipeline. It checks that the last pair wins in the
// result map and redis.Nil still permits assembling the supplied counts.
func TestConcurrencyBatchDuplicateIDs(t *testing.T) {
	for _, entry := range concurrencyBatchEntries() {
		t.Run(entry.name, func(t *testing.T) {
			for _, pipelineErr := range []error{nil, redis.Nil, fmt.Errorf("batch: %w", redis.Nil)} {
				cache, _ := newConcurrencyBatchTestCache(t)
				hook := &concurrencyBatchCommandHook{
					countValues: []int64{1, 2, 3, 4, 5, 6},
					pipelineErr: pipelineErr,
				}
				cache.rdb.AddHook(hook)
				counts, err := entry.count(cache, context.Background(), []int64{41, 52, 41})
				require.NoError(t, err)
				require.Equal(t, map[int64]int{41: 11, 52: 7}, counts)
				require.Equal(t, 1, hook.pipelineCalls)
				require.Len(t, hook.commands, 13)
			}
		})
	}
}

// TestConcurrencyBatchErrors injects TIME and pipeline execution failures for
// each resource. It checks the existing error wrapping, nil result map and
// whether execution stops before a pipeline can run.
func TestConcurrencyBatchErrors(t *testing.T) {
	for _, entry := range concurrencyBatchEntries() {
		t.Run(entry.name, func(t *testing.T) {
			for _, phase := range []string{"time", "pipeline"} {
				t.Run(phase, func(t *testing.T) {
					cache, _ := newConcurrencyBatchTestCache(t)
					injected := errors.New("injected batch failure")
					hook := &concurrencyBatchCommandHook{}
					wantError := "redis TIME: injected batch failure"
					wantPipelines := 0
					if phase == "time" {
						hook.timeErr = injected
					} else {
						hook.pipelineErr = injected
						wantError = "pipeline exec: injected batch failure"
						wantPipelines = 1
					}
					cache.rdb.AddHook(hook)
					counts, err := entry.count(cache, context.Background(), []int64{10})
					require.ErrorIs(t, err, injected)
					require.EqualError(t, err, wantError)
					require.Nil(t, counts)
					require.Equal(t, wantPipelines, hook.pipelineCalls)
					require.Equal(t, []any{"time"}, hook.commands[0])
				})
			}
		})
	}
}

// BenchmarkProviderConcurrency measures sorted-set counting and SCAN counting
// at three seeded slot sizes on TEST_REDIS_URL. Member seeding and deletion
// run with timers stopped; results describe that configured Redis workload.
func BenchmarkProviderConcurrency(b *testing.B) {
	rdb := newBenchmarkRedisClient(b)
	// Close the benchmark-owned client when its workload finishes; teardown
	// discards close errors so measured command failures remain the benchmark's
	// reported failure.
	defer func() {
		_ = rdb.Close()
	}()

	cache, _ := NewConcurrencyCache(rdb, benchSlotTTLMinutes, int(benchSlotTTL.Seconds())).(*concurrencyCache)
	ctx := context.Background()

	for _, size := range []int{10, 100, 1000} {
		b.Run(fmt.Sprintf("zset/slots=%d", size), func(b *testing.B) {
			providerID := time.Now().UnixNano()
			key := providerSlotKey(providerID)

			// Seed the sorted-set members outside timing so the counted workload
			// starts with its requested number of existing ordinary request members.
			b.StopTimer()
			members := make([]redis.Z, 0, size)
			now := float64(time.Now().Unix())
			for i := range size {
				members = append(members, redis.Z{
					Score:  now,
					Member: fmt.Sprintf("req_%d", i),
				})
			}
			if err := rdb.ZAdd(ctx, key, members...).Err(); err != nil {
				b.Fatalf("初始化有序集合失败: %v", err)
			}
			if err := rdb.Expire(ctx, key, benchSlotTTL).Err(); err != nil {
				b.Fatalf("设置有序集合 TTL 失败: %v", err)
			}
			b.StartTimer()

			b.ReportAllocs()
			for range b.N {
				if _, err := cache.GetProviderConcurrency(ctx, providerID); err != nil {
					b.Fatalf("获取并发数量失败: %v", err)
				}
			}

			// Delete the seeded sorted set after stopping timing so cleanup of this
			// benchmark's member fixture does not contribute to the count
			// measurements.
			b.StopTimer()
			if err := rdb.Del(ctx, key).Err(); err != nil {
				b.Fatalf("清理有序集合失败: %v", err)
			}
		})

		b.Run(fmt.Sprintf("scan/slots=%d", size), func(b *testing.B) {
			providerID := time.Now().UnixNano()
			pattern := fmt.Sprintf("%s%d:*", providerSlotKeyPrefix, providerID)
			keys := make([]string, 0, size)

			// Populate one Redis key per request outside timing so the measured SCAN
			// traversal operates on the already-created fixture for this slot count.
			b.StopTimer()
			pipe := rdb.Pipeline()
			for i := range size {
				key := fmt.Sprintf("%s%d:req_%d", providerSlotKeyPrefix, providerID, i)
				keys = append(keys, key)
				pipe.Set(ctx, key, "1", benchSlotTTL)
			}
			if _, err := pipe.Exec(ctx); err != nil {
				b.Fatalf("初始化扫描键失败: %v", err)
			}
			b.StartTimer()

			b.ReportAllocs()
			for range b.N {
				if _, err := scanSlotCount(ctx, rdb, pattern); err != nil {
					b.Fatalf("SCAN 计数失败: %v", err)
				}
			}

			// Delete the exact seeded request keys after stopping timing so teardown
			// is separated from repeated traversal of this fixture's key pattern.
			b.StopTimer()
			if err := rdb.Del(ctx, keys...).Err(); err != nil {
				b.Fatalf("清理扫描键失败: %v", err)
			}
		})
	}
}

// scanSlotCount accumulates matching key occurrences returned by SCAN until
// its cursor completes. It propagates command errors; callers keep the
// benchmark key set stable because this traversal is not an atomic count.
func scanSlotCount(ctx context.Context, rdb *redis.Client, pattern string) (int, error) {
	var cursor uint64
	count := 0
	for {
		// SCAN's count argument is a cursor-work hint, not a returned-key limit.
		keys, nextCursor, err := rdb.Scan(ctx, cursor, pattern, 100).Result()
		if err != nil {
			return 0, err
		}
		count += len(keys)
		if nextCursor == 0 {
			break
		}
		cursor = nextCursor
	}
	return count, nil
}

// newBenchmarkRedisClient creates and pings the client configured by
// TEST_REDIS_URL, skipping when that value is empty. Invalid URLs and failed
// pings fail the benchmark; callers close a successfully returned client.
func newBenchmarkRedisClient(b *testing.B) *redis.Client {
	b.Helper()

	redisURL := os.Getenv("TEST_REDIS_URL")
	// An absent endpoint leaves this fixture's Redis workload unconfigured. Skip
	// the benchmark so results identify the server selected by TEST_REDIS_URL.
	if redisURL == "" {
		b.Skip("未设置 TEST_REDIS_URL，跳过 Redis 基准测试")
	}

	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		b.Fatalf("解析 TEST_REDIS_URL 失败: %v", err)
	}

	client := redis.NewClient(opt)
	// Supply a short context deadline for the endpoint probe; socket operations
	// still use the parsed client's configured read and write timeouts.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		b.Fatalf("Redis 连接失败: %v", err)
	}

	return client
}

// TestLoadBatchCommandErrorsPropagate verifies that GetProvidersLoadBatch and
// GetUsersLoadBatch surface a later slot command error (such as WRONGTYPE) even
// when an earlier idle wait-counter lookup in the same pipeline returns
// redis.Nil, while valid mixed load entries compute expected rates.
func TestLoadBatchCommandErrorsPropagate(t *testing.T) {
	ctx := context.Background()

	t.Run("provider_and_user_load_rates", func(t *testing.T) {
		cache, server := newConcurrencyBatchTestCache(t)
		now := time.Unix(1_700_000_000, 0)
		server.SetTime(now)

		require.NoError(t, cache.rdb.ZAdd(ctx, providerSlotKey(1), redis.Z{Score: float64(now.Unix()), Member: "req-1"}).Err())
		require.NoError(t, cache.rdb.ZAdd(ctx, liveProviderSlotKey(1), redis.Z{Score: float64(now.Unix()), Member: "live-1"}).Err())
		require.NoError(t, cache.rdb.Set(ctx, providerWaitKey(1), "1", time.Minute).Err())

		providerLoads, err := cache.GetProvidersLoadBatch(ctx, []scheduler.ProviderWithConcurrency{
			{ID: 1, MaxConcurrency: 4},
			{ID: 2, MaxConcurrency: 2},
		})
		require.NoError(t, err)
		require.Equal(t, &scheduler.ProviderLoadInfo{
			ProviderID:         1,
			CurrentConcurrency: 2,
			WaitingCount:       1,
			LoadRate:           75,
		}, providerLoads[1])
		require.Equal(t, &scheduler.ProviderLoadInfo{
			ProviderID:         2,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}, providerLoads[2])

		require.NoError(t, cache.rdb.ZAdd(ctx, userSlotKey(10), redis.Z{Score: float64(now.Unix()), Member: "u-req-1"}).Err())
		require.NoError(t, cache.rdb.Set(ctx, waitQueueKey(10), "2", time.Minute).Err())

		userLoads, err := cache.GetUsersLoadBatch(ctx, []scheduler.UserWithConcurrency{
			{ID: 10, MaxConcurrency: 2},
			{ID: 20, MaxConcurrency: 5},
		})
		require.NoError(t, err)
		require.Equal(t, &scheduler.UserLoadInfo{
			UserID:             10,
			CurrentConcurrency: 1,
			WaitingCount:       2,
			LoadRate:           150,
		}, userLoads[10])
		require.Equal(t, &scheduler.UserLoadInfo{
			UserID:             20,
			CurrentConcurrency: 0,
			WaitingCount:       0,
			LoadRate:           0,
		}, userLoads[20])
	})

	t.Run("earlier_redis_nil_does_not_mask_later_slot_error", func(t *testing.T) {
		cache, server := newConcurrencyBatchTestCache(t)
		server.SetTime(time.Unix(1_700_000_000, 0))

		// Entity 1 has an absent wait key (producing redis.Nil first in the
		// pipeline), while entity 2 stores a string at its ordinary slot sorted-set
		// key so ZREMRANGEBYSCORE/ZCARD fail with WRONGTYPE.
		require.NoError(t, cache.rdb.Set(ctx, providerSlotKey(2), "corrupt-string", time.Minute).Err())
		providerLoads, err := cache.GetProvidersLoadBatch(ctx, []scheduler.ProviderWithConcurrency{
			{ID: 1, MaxConcurrency: 5},
			{ID: 2, MaxConcurrency: 5},
		})
		require.Error(t, err)
		require.Nil(t, providerLoads)
		require.Contains(t, err.Error(), "WRONGTYPE")

		require.NoError(t, cache.rdb.Set(ctx, userSlotKey(22), "corrupt-string", time.Minute).Err())
		userLoads, err := cache.GetUsersLoadBatch(ctx, []scheduler.UserWithConcurrency{
			{ID: 11, MaxConcurrency: 5},
			{ID: 22, MaxConcurrency: 5},
		})
		require.Error(t, err)
		require.Nil(t, userLoads)
		require.Contains(t, err.Error(), "WRONGTYPE")
	})
}
