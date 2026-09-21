//go:build integration

package rediscache

import (
	"context"
	"errors"
	"testing"
	"time"

	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/rediscache/codec"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/stretchr/testify/require"
)

// B05：真实 Redis 中旧锁自然过期后，旧句柄不能释放继任持有者的锁。
func TestS07BucketLeaseExpiredOwnerCannotReleaseSuccessor(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	cache := NewSnapshotCache(rdb, codec.AccountCodec{})
	bucket := scheduler.SchedulerBucket{GroupID: 91001, Platform: capability.PlatformOpenAI, Mode: scheduler.SchedulerModeSingle}
	first, acquired, err := cache.AcquireBucketLease(ctx, bucket, 20*time.Millisecond)
	require.NoError(t, err)
	require.True(t, acquired)
	time.Sleep(40 * time.Millisecond)
	second, acquired, err := cache.AcquireBucketLease(ctx, bucket, time.Minute)
	require.NoError(t, err)
	require.True(t, acquired)
	key := "sched:v2:lock:" + bucket.String()
	owner, err := rdb.Get(ctx, key).Result()
	require.NoError(t, err)
	require.ErrorIs(t, first.Release(ctx), scheduler.ErrBucketLeaseLost)
	current, err := rdb.Get(ctx, key).Result()
	require.NoError(t, err)
	require.Equal(t, owner, current)
	_, acquired, err = cache.AcquireBucketLease(ctx, bucket, time.Minute)
	require.NoError(t, err)
	require.False(t, acquired)
	require.NoError(t, second.Release(ctx))
	require.NoError(t, second.Release(ctx))
	count, err := rdb.Exists(ctx, key).Result()
	require.NoError(t, err)
	require.Zero(t, count)
}

// 仅注入增加操作的传输错误，查询和释放继续操作真实 Redis。
type s07WaitIncrementFault struct{ scheduler.ConcurrencyCache }

func (c s07WaitIncrementFault) IncrementWaitCount(context.Context, int64, int) (bool, error) {
	return false, errors.New("增加等待计数未确认")
}
func (c s07WaitIncrementFault) IncrementAccountWaitCount(context.Context, int64, int) (bool, error) {
	return false, errors.New("增加账号等待计数未确认")
}

// B02：错误放行后立即退出，不能递减 Redis 中另一个请求持有的用户或账号计数。
func TestS07WaitFailOpenDoesNotReleaseOtherRequest(t *testing.T) {
	ctx := context.Background()
	rdb := testRedis(t)
	cache := NewConcurrencyCache(rdb, 15, 900)
	userAllowed, err := cache.IncrementWaitCount(ctx, 91001, 20)
	require.NoError(t, err)
	require.True(t, userAllowed)
	accountAllowed, err := cache.IncrementAccountWaitCount(ctx, 91002, 20)
	require.NoError(t, err)
	require.True(t, accountAllowed)
	concurrency := scheduler.NewConcurrencyService(s07WaitIncrementFault{cache}, scheduler.Diagnostics{Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
	user, err := concurrency.EnterUserWait(ctx, 91001, 20)
	require.NoError(t, err)
	require.True(t, user.Allowed)
	account, err := concurrency.EnterAccountWait(ctx, 91002, 20)
	require.NoError(t, err)
	require.True(t, account.Allowed)
	user.Release()
	account.Release()
	user.Release()
	account.Release()
	count, err := rdb.Get(ctx, "concurrency:wait:91001").Int()
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = cache.GetAccountWaitingCount(ctx, 91002)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}
