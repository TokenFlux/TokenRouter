package rediscache

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/creative"
)

// TestRecoverStaleActiveFencesOldLeaseAndBlocksDuplicateEnqueue reserves one
// creative run, backdates its active heartbeat, and runs RecoverStaleActive.
// It checks that the stale worker's lease token is rejected by Heartbeat and
// Ack while a concurrent Enqueue for the same run still returns
// ErrCreativeAlreadyQueued until the next reservation acknowledges it.
func TestRecoverStaleActiveFencesOldLeaseAndBlocksDuplicateEnqueue(t *testing.T) {
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })

	q := NewCreativeQueue(rdb, nil)
	ctx := context.Background()
	runID := "crun_0123456789abcdef0123456789abcdef"

	require.NoError(t, q.Enqueue(ctx, runID))
	first, err := q.Reserve(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, runID, first.RunID)

	require.NoError(t, rdb.ZAdd(ctx, defaultCreativeActiveKey, redis.Z{
		Score:  float64(time.Now().Add(-10 * time.Minute).UnixMilli()),
		Member: runID,
	}).Err())

	recovered, err := q.RecoverStaleActive(ctx, time.Minute, 10)
	require.NoError(t, err)
	require.Equal(t, 1, recovered)

	require.ErrorIs(t, q.Enqueue(ctx, runID), creative.ErrCreativeAlreadyQueued)

	alive, err := q.Heartbeat(ctx, runID, first.LeaseToken)
	require.NoError(t, err)
	require.False(t, alive)
	require.ErrorIs(t, q.Ack(ctx, runID, first.LeaseToken), creative.ErrCreativeLeaseLost)

	second, err := q.Reserve(ctx, 0)
	require.NoError(t, err)
	require.Equal(t, runID, second.RunID)
	require.NotEqual(t, first.LeaseToken, second.LeaseToken)
	require.NoError(t, q.Ack(ctx, runID, second.LeaseToken))
	require.NoError(t, q.Enqueue(ctx, runID))
}
