package taskqueue

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// TestEnqueueAndRecoverStaleActiveResetInflightPreservesDeduplication enqueues
// a job, simulates an active lease, and recovers it as stale. It checks that
// the old lease token is overwritten with the job ID while duplicate Enqueue
// calls remain blocked.
func TestEnqueueAndRecoverStaleActiveResetInflightPreservesDeduplication(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	ok, err := Enqueue(ctx, rdb, "q:inflight:job_1", "q:ready", "job_1", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)

	dup, err := Enqueue(ctx, rdb, "q:inflight:job_1", "q:ready", "job_1", time.Minute)
	require.NoError(t, err)
	require.False(t, dup)

	_, err = rdb.RPop(ctx, "q:ready").Result()
	require.NoError(t, err)
	require.NoError(t, rdb.ZAdd(ctx, "q:active", redis.Z{Score: 1000, Member: "job_1"}).Err())
	require.NoError(t, rdb.Set(ctx, "q:inflight:job_1", "old_lease_token", time.Minute).Err())

	moved, err := RecoverStaleActiveResetInflight(ctx, rdb, "q:active", "q:ready", "q:inflight:", 2000, 10, time.Minute)
	require.NoError(t, err)
	require.Equal(t, 1, moved)

	inflightVal, err := rdb.Get(ctx, "q:inflight:job_1").Result()
	require.NoError(t, err)
	require.Equal(t, "job_1", inflightVal)

	dupAfterRecover, err := Enqueue(ctx, rdb, "q:inflight:job_1", "q:ready", "job_1", time.Minute)
	require.NoError(t, err)
	require.False(t, dupAfterRecover)
}

// TestAcquireRefreshAndReleaseLockFencesStaleToken acquires a token-owned lock,
// refreshes its TTL, and verifies mismatched tokens cannot refresh or delete a
// replacement lock. It checks boolean ownership results and final key cleanup.
func TestAcquireRefreshAndReleaseLockFencesStaleToken(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	ctx := context.Background()
	token, ok, err := AcquireLock(ctx, rdb, "q:lock:job_1", time.Minute)
	require.NoError(t, err)
	require.True(t, ok)
	require.Len(t, token, 32)

	_, contended, err := AcquireLock(ctx, rdb, "q:lock:job_1", time.Minute)
	require.NoError(t, err)
	require.False(t, contended)

	refreshed, err := RefreshLock(ctx, rdb, "q:lock:job_1", "wrong", time.Minute)
	require.NoError(t, err)
	require.False(t, refreshed)

	refreshed, err = RefreshLock(ctx, rdb, "q:lock:job_1", token, 2*time.Minute)
	require.NoError(t, err)
	require.True(t, refreshed)

	require.NoError(t, ReleaseLock(ctx, rdb, "q:lock:job_1", "wrong"))
	require.True(t, mr.Exists("q:lock:job_1"))

	require.NoError(t, ReleaseLock(ctx, rdb, "q:lock:job_1", token))
	require.False(t, mr.Exists("q:lock:job_1"))
}

// TestPollReservePollsUntilAvailableAndHonorsEmptyTimeout exercises PollReserve
// across an initially empty callback that succeeds on retry and a persistently
// empty callback. It checks retry recovery and bounded timeout return.
func TestPollReservePollsUntilAvailableAndHonorsEmptyTimeout(t *testing.T) {
	errEmpty := errors.New("empty")
	attempts := 0
	got, err := PollReserve(context.Background(), 50*time.Millisecond, 5*time.Millisecond, func(err error) bool {
		return errors.Is(err, errEmpty)
	}, func(context.Context) (string, error) {
		attempts++
		if attempts < 2 {
			return "", errEmpty
		}
		return "job_ok", nil
	})
	require.NoError(t, err)
	require.Equal(t, "job_ok", got)
	require.Equal(t, 2, attempts)

	_, err = PollReserve(context.Background(), 10*time.Millisecond, 5*time.Millisecond, func(err error) bool {
		return errors.Is(err, errEmpty)
	}, func(context.Context) (string, error) {
		return "", errEmpty
	})
	require.ErrorIs(t, err, errEmpty)
}
