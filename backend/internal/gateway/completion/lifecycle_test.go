package completion

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 停止必须包含已经进入同步兜底的任务，而不只等待 pond 队列。
func TestStopWaitsForSynchronousOverflow(t *testing.T) {
	pool := NewUsageRecordWorkerPoolWithOptions(UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, TaskTimeout: time.Second, OverflowPolicy: "sync"})
	first, releaseQueue := make(chan struct{}), make(chan struct{})
	inline, releaseInline := make(chan struct{}), make(chan struct{})
	submitted := make(chan UsageRecordSubmitMode, 1)
	require.Equal(t, UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { close(first); <-releaseQueue }))
	<-first
	require.Equal(t, UsageRecordSubmitModeEnqueued, pool.Submit(func(context.Context) { <-releaseQueue }))
	go func() { submitted <- pool.Submit(func(context.Context) { close(inline); <-releaseInline }) }()
	<-inline
	close(releaseQueue)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := pool.StopContext(ctx)
	close(releaseInline)
	require.Equal(t, UsageRecordSubmitModeSync, <-submitted)
	require.NoError(t, pool.StopContext(context.Background()))
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "unfinished tasks")
}

// 停止是不可逆屏障，顺序与并发的重复调用都不能重开 worker。
func TestStartAfterStopDoesNotCreateWorker(t *testing.T) {
	pool := NewUsageRecordWorkerPoolWithOptions(UsageRecordWorkerPoolOptions{WorkerCount: 1, QueueSize: 1, AutoScaleEnabled: true, AutoScaleMinWorkers: 1, AutoScaleMaxWorkers: 2})
	pool.Stop()
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); pool.Start(); pool.Stop() }()
	}
	wg.Wait()
	require.Nil(t, pool.autoScaleCancel)
	require.Equal(t, UsageRecordSubmitModeDroppedStopped, pool.Submit(func(context.Context) { t.Error("停止后任务不应执行") }))
}
