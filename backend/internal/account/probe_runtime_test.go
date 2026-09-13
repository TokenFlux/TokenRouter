package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 同 key 只执行一次；任一等待者取消不能取消其他等待者的实际工作。
func TestProbeRuntimeSharedCancellationAndStop(t *testing.T) {
	var runtime ProbeRuntime
	var calls atomic.Int32
	entered, exited := make(chan struct{}), make(chan struct{})
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	probe := func(ctx context.Context) (any, error) {
		calls.Add(1)
		close(entered)
		defer close(exited)
		<-ctx.Done()
		return nil, ctx.Err()
	}
	go func() { _, err := runtime.Run(caller, "same", time.Minute, probe); done <- err }()
	<-entered
	go func() { _, err := runtime.Run(context.Background(), "same", time.Minute, probe); done <- err }()
	require.Eventually(t, func() bool {
		runtime.activity.mu.Lock()
		defer runtime.activity.mu.Unlock()
		return len(runtime.activity.active) == 3
	}, time.Second, time.Millisecond)
	cancel()
	require.ErrorIs(t, <-done, context.Canceled)
	select {
	case <-exited:
		t.Fatal("等待者取消结束了共享工作")
	default:
	}
	budget, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.NoError(t, runtime.StopContext(budget))
	require.ErrorIs(t, <-done, context.Canceled)
	require.Equal(t, int32(1), calls.Load())
	_, err := runtime.Run(context.Background(), "later", time.Minute, probe)
	require.ErrorIs(t, err, ErrProbeStopped)
	require.False(t, runtime.Schedule("later", time.Minute, func(context.Context) { t.Error("停止后启动") }))
}

// 停止必须等待忽略取消的任务并报告超时，不能被稍后完成覆盖首次结果。
func TestProbeRuntimeBackgroundStopReportsUnfinished(t *testing.T) {
	var runtime ProbeRuntime
	entered, release, exited := make(chan struct{}), make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release); <-exited })
	require.True(t, runtime.Schedule("models", time.Minute, func(context.Context) {
		close(entered)
		defer close(exited)
		<-release
	}))
	<-entered
	require.False(t, runtime.Schedule("models", time.Minute, func(context.Context) { t.Error("同 key 重复执行") }))
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := runtime.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	require.Same(t, err, runtime.StopContext(context.Background()))
}
