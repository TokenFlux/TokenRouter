package account

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 生产运行拥有者覆盖构造、并发启动、取消、等待以及重复停止。
func TestOllamaUsageRuntimeStartsOnceAndStopsAllWork(t *testing.T) {
	var calls atomic.Int32
	entered := make(chan struct{})
	runtime := NewOllamaUsageRuntime(func(ctx context.Context) error {
		if calls.Add(1) == 1 {
			close(entered)
		}
		<-ctx.Done()
		return ctx.Err()
	}, nil)
	require.Zero(t, calls.Load())
	var starters sync.WaitGroup
	for range 10 {
		starters.Go(func() { require.NoError(t, runtime.StartContext(context.Background())) })
	}
	starters.Wait()
	<-entered
	ctx, finish, err := runtime.Begin(context.Background())
	require.NoError(t, err)
	done := make(chan struct{})
	go func() { <-ctx.Done(); finish(); close(done) }()
	budget, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, runtime.StopContext(budget))
	<-done
	require.Equal(t, int32(1), calls.Load())
	require.NoError(t, runtime.StartContext(context.Background()))
	require.Equal(t, int32(1), calls.Load())
	_, _, err = runtime.Begin(context.Background())
	require.ErrorIs(t, err, ErrOllamaUsageStopped)
	require.NoError(t, runtime.StopContext(context.Background()))
}
func TestOllamaUsageRuntimeRetainsIncompleteStopResult(t *testing.T) {
	runtime := NewOllamaUsageRuntime(func(context.Context) error { t.Fatal("停止后不得启动"); return nil }, nil)
	ctx, finish, err := runtime.Begin(context.Background())
	require.NoError(t, err)
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err = runtime.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	finish()
	require.Same(t, err, runtime.StopContext(context.Background()))
	require.NoError(t, runtime.StartContext(context.Background()))
}
