//go:build unit

package batchimage_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorkerRuntime_QueueDisabledDoesNotStart(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	runtime := batchimage.NewWorkerRuntime(
		batchimage.NewBatchImageWorker(queue, &fakeBatchImageProcessor{}, batchimage.BatchImageWorkerOptions{}), nil, false,
	)

	runtime.Start()

	require.False(t, runtime.Running())
	require.Zero(t, queue.reserveCalls.Load())
	require.NotPanics(t, runtime.Stop)
}

func TestBatchImageWorkerRuntime_QueueEnabledStartsAndStops(t *testing.T) {
	queue := &blockingBatchImageRuntimeQueue{}
	processor := &fakeBatchImageProcessor{}
	runtime := batchimage.NewWorkerRuntime(
		batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{
			DelayedPollInterval: time.Hour,
			RecoveryInterval:    time.Hour,
		}), nil, true,
	)

	runtime.Start()

	require.Eventually(t, func() bool {
		return runtime.Running() && queue.reserveCalls.Load() > 0
	}, time.Second, 10*time.Millisecond)
	require.Empty(t, processor.processed)
	require.NotPanics(t, runtime.Stop)
	require.False(t, runtime.Running())
	require.NotPanics(t, runtime.Stop)
}

type blockingBatchImageRuntimeQueue struct {
	reserveCalls atomic.Int64
}

func (q *blockingBatchImageRuntimeQueue) Enqueue(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Reserve(ctx context.Context, _ time.Duration) (batchimage.ReservedBatchImageJob, error) {
	q.reserveCalls.Add(1)
	<-ctx.Done()
	return batchimage.ReservedBatchImageJob{}, ctx.Err()
}

func (q *blockingBatchImageRuntimeQueue) RequeueAfter(context.Context, string, time.Duration) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Ack(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *blockingBatchImageRuntimeQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *blockingBatchImageRuntimeQueue) TryAcquireJobLock(context.Context, string, time.Duration) (batchimage.BatchImageJobLock, bool, error) {
	return nil, false, nil
}
