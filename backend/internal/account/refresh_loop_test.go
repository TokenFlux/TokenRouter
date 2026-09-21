package account

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 多个同时到达的启动调用只能取得一次立即扫描，停止等待该轮实际退出。
func TestRefreshLoopConcurrentStartAndBoundedStop(t *testing.T) {
	var cycles, starts atomic.Int32
	entered := make(chan struct{})
	finished := make(chan struct{})
	loop := NewRefreshLoop(func(ctx context.Context) { cycles.Add(1); close(entered); <-ctx.Done(); close(finished) })
	require.Zero(t, cycles.Load())
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Go(func() {
			started, err := loop.StartContext(context.Background(), time.Hour)
			if err != nil {
				t.Error(err)
			}
			if started {
				starts.Add(1)
			}
		})
	}
	wg.Wait()
	waitUsageSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, loop.StopContext(ctx))
	waitUsageSignal(t, finished)
	require.Equal(t, int32(1), cycles.Load())
	require.Equal(t, int32(1), starts.Load())
	started, err := loop.StartContext(context.Background(), time.Hour)
	require.NoError(t, err)
	require.False(t, started)
	require.NoError(t, loop.StopContext(ctx))
}
func TestRefreshLoopCannotStartAfterStop(t *testing.T) {
	loop := NewRefreshLoop(func(context.Context) { t.Error("停止后开始扫描") })
	require.NoError(t, loop.StopContext(context.Background()))
	started, err := loop.StartContext(context.Background(), time.Hour)
	require.NoError(t, err)
	require.False(t, started)
}
func TestRefreshLoopStopReportsIgnoredCancellation(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	loop := NewRefreshLoop(func(context.Context) { close(entered); <-release })
	started, err := loop.StartContext(context.Background(), time.Hour)
	require.True(t, started)
	require.NoError(t, err)
	waitUsageSignal(t, entered)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = loop.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "token refresh")
	close(release)
	require.Same(t, err, loop.StopContext(context.Background()))
}
func TestRefreshLoopCancelledStartDoesNotClaim(t *testing.T) {
	loop := NewRefreshLoop(func(context.Context) { t.Error("取消的启动产生扫描") })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	started, err := loop.StartContext(ctx, time.Hour)
	require.ErrorIs(t, err, context.Canceled)
	require.False(t, started)
	require.NoError(t, loop.StopContext(context.Background()))
}
