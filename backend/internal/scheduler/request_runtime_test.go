package scheduler

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 只实现本用例需要的缓存方法，断言真实租约释放完成先于停止成功。
type runtimeSlotCache struct {
	ConcurrencyCache
	acquired        bool
	releaseStarted  chan struct{}
	releaseContinue chan struct{}
	releases        atomic.Int64
}

func (c *runtimeSlotCache) AcquireAccountSlot(context.Context, int64, int, string) (bool, error) {
	return c.acquired, nil
}
func (c *runtimeSlotCache) ReleaseAccountSlot(context.Context, int64, string) error {
	c.releases.Add(1)
	if c.releaseStarted != nil {
		close(c.releaseStarted)
		<-c.releaseContinue
	}
	return nil
}
func TestRequestLeaseStopWaitsForActualRelease(t *testing.T) {
	cache := &runtimeSlotCache{acquired: true, releaseStarted: make(chan struct{}), releaseContinue: make(chan struct{})}
	core := NewConcurrencyService(cache)
	slot, err := core.AcquireAccountSlot(context.Background(), 7, 1)
	require.NoError(t, err)
	stopped := make(chan error, 1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	go func() { stopped <- core.StopContext(ctx) }()
	<-core.runtime.Context().Done()
	select {
	case err := <-stopped:
		t.Fatalf("释放前停止已返回：%v", err)
	default:
	}
	released := make(chan struct{})
	go func() { slot.ReleaseFunc(); close(released) }()
	<-cache.releaseStarted
	select {
	case err := <-stopped:
		t.Fatalf("Redis 释放未完成即停止：%v", err)
	default:
	}
	close(cache.releaseContinue)
	<-released
	require.NoError(t, <-stopped)
	slot.ReleaseFunc()
	require.Equal(t, int64(1), cache.releases.Load())
	_, err = core.AcquireAccountSlot(context.Background(), 7, 1)
	require.ErrorIs(t, err, ErrRuntimeStopped)
}
func TestRequestLeaseStopTimeoutRetainsFailure(t *testing.T) {
	cache := &runtimeSlotCache{acquired: true}
	core := NewConcurrencyService(cache)
	slot, err := core.AcquireAccountSlot(context.Background(), 9, 1)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()
	stopErr := core.StopContext(ctx)
	require.ErrorIs(t, stopErr, context.DeadlineExceeded)
	require.ErrorContains(t, stopErr, "account-slot:9")
	slot.ReleaseFunc()
	require.Equal(t, stopErr, core.StopContext(context.Background()))
}
func TestSlotWaitStopCancelsObserverLoop(t *testing.T) {
	core := NewConcurrencyService(&runtimeSlotCache{})
	waiting := make(chan struct{})
	finished := make(chan error, 1)
	go func() {
		_, err := core.WaitForSlot(context.Background(), "account", 1, 1, time.Hour, false, WaitObserver{Begin: func() error { close(waiting); return nil }})
		finished <- err
	}()
	<-waiting
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, core.StopContext(ctx))
	require.ErrorIs(t, <-finished, context.Canceled)
}
func TestSlotWaitObserverFailureDoesNotAcquire(t *testing.T) {
	core := NewConcurrencyService(&runtimeSlotCache{})
	failed := errors.New("下游心跳写失败")
	_, err := core.WaitForSlot(context.Background(), "account", 1, 1, time.Second, false, WaitObserver{Interval: time.Millisecond, Heartbeat: func() error { return failed }})
	require.ErrorIs(t, err, failed)
	require.NoError(t, core.StopContext(context.Background()))
}

// 迁移契约：无上限槽位沿用原立即放行，取消策略由外层 ReleaseMode 决定。
func TestUnlimitedSlotPreservesCallerCancellation(t *testing.T) {
	for _, user := range []bool{false, true} {
		core := NewConcurrencyService(nil)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var slot *AcquireResult
		var err error
		if user {
			slot, err = core.AcquireUserSlot(ctx, 4, 0)
		} else {
			slot, err = core.AcquireAccountSlot(ctx, 4, 0)
		}
		require.NoError(t, err)
		require.True(t, slot.Acquired)
		slot.ReleaseFunc()
		require.NoError(t, core.StopContext(context.Background()))
	}
}
