//go:build unit

package batchimage_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	"github.com/stretchr/testify/require"
)

func TestBatchImageWorker_ProcessesJobOnce(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_once")
	processor := &fakeBatchImageProcessor{}
	worker := batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{ReserveBlockTimeout: time.Millisecond})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Equal(t, []string{"imgbatch_worker_once"}, processor.processed)
	require.Len(t, queue.requeued, 1)
	require.Equal(t, batchimage.DefaultBatchImageWorkerRequeueDelay, queue.requeued[0].delay)
	require.Equal(t, 1, queue.releaseCount)
}

func TestBatchImageWorker_RequeuesNonTerminalResultWithRequestedDelay(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_requeue")
	processor := &fakeBatchImageProcessor{result: batchimage.BatchImageProcessResult{RequeueAfter: 42 * time.Second}}
	worker := batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Len(t, queue.requeued, 1)
	require.Equal(t, "imgbatch_worker_requeue", queue.requeued[0].batchID)
	require.Equal(t, 42*time.Second, queue.requeued[0].delay)
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_AcksTerminalResult(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_terminal")
	processor := &fakeBatchImageProcessor{result: batchimage.BatchImageProcessResult{Terminal: true}}
	worker := batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Equal(t, []string{"imgbatch_worker_terminal"}, queue.acked)
	require.Empty(t, queue.requeued)
}

func TestBatchImageWorker_RequeuesOnProcessorError(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_error")
	processor := &fakeBatchImageProcessor{err: errors.New("processor failed")}
	worker := batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{ErrorRetryDelay: 7 * time.Second})

	require.NoError(t, worker.RunOnce(context.Background()))
	require.Len(t, queue.requeued, 1)
	require.Equal(t, 7*time.Second, queue.requeued[0].delay)
	require.Empty(t, queue.acked)
}

func TestBatchImageWorker_RequeuesWhenJobLockNotAcquired(t *testing.T) {
	queue := newFakeBatchImageQueue("imgbatch_worker_locked")
	queue.lockAcquired = false
	processor := &fakeBatchImageProcessor{}
	worker := batchimage.NewBatchImageWorker(queue, processor, batchimage.BatchImageWorkerOptions{LockConflictDelay: 3 * time.Second})

	// 锁冲突必须按冲突延迟重新入队；直接丢弃会让 job 滞留 active zset，
	// 要等 StaleActiveAfter（默认 10 分钟）才被恢复。
	require.NoError(t, worker.RunOnce(context.Background()))
	require.Empty(t, processor.processed)
	require.Len(t, queue.requeued, 1)
	require.Equal(t, 3*time.Second, queue.requeued[0].delay)
	require.Empty(t, queue.acked)
}

type fakeBatchImageQueue struct {
	reserved     batchimage.ReservedBatchImageJob
	lockAcquired bool
	acked        []string
	requeued     []fakeBatchImageRequeue
	releaseCount int
}

type fakeBatchImageRequeue struct {
	batchID string
	delay   time.Duration
}

func newFakeBatchImageQueue(batchID string) *fakeBatchImageQueue {
	return &fakeBatchImageQueue{
		reserved:     batchimage.ReservedBatchImageJob{BatchID: batchID},
		lockAcquired: true,
	}
}

func (q *fakeBatchImageQueue) Enqueue(context.Context, string) error {
	return nil
}

func (q *fakeBatchImageQueue) Reserve(context.Context, time.Duration) (batchimage.ReservedBatchImageJob, error) {
	return q.reserved, nil
}

func (q *fakeBatchImageQueue) RequeueAfter(_ context.Context, batchID string, delay time.Duration) error {
	q.requeued = append(q.requeued, fakeBatchImageRequeue{batchID: batchID, delay: delay})
	return nil
}

func (q *fakeBatchImageQueue) Ack(_ context.Context, batchID string) error {
	q.acked = append(q.acked, batchID)
	return nil
}

func (q *fakeBatchImageQueue) Heartbeat(context.Context, string) error {
	return nil
}

func (q *fakeBatchImageQueue) MoveDueDelayedToReady(context.Context, int) (int, error) {
	return 0, nil
}

func (q *fakeBatchImageQueue) RecoverStaleActive(context.Context, time.Duration, int) (int, error) {
	return 0, nil
}

func (q *fakeBatchImageQueue) TryAcquireJobLock(context.Context, string, time.Duration) (batchimage.BatchImageJobLock, bool, error) {
	if !q.lockAcquired {
		return nil, false, nil
	}
	return fakeBatchImageLock{release: func() { q.releaseCount++ }, queue: q}, true, nil
}

type fakeBatchImageLock struct {
	release func()
	queue   *fakeBatchImageQueue
}

func (l fakeBatchImageLock) Release(context.Context) error {
	if l.release != nil {
		l.release()
	}
	return nil
}

type fakeBatchImageProcessor struct {
	result    batchimage.BatchImageProcessResult
	err       error
	processed []string
}

func (p *fakeBatchImageProcessor) Process(_ context.Context, batchID string) (batchimage.BatchImageProcessResult, error) {
	p.processed = append(p.processed, batchID)
	return p.result, p.err
}

// 测试锁也通过自己持有的任务执行队列写入，保留旧数量断言。
func (l fakeBatchImageLock) Heartbeat(ctx context.Context) error {
	return l.queue.Heartbeat(ctx, l.queue.reserved.BatchID)
}
func (l fakeBatchImageLock) Ack(ctx context.Context) error {
	return l.queue.Ack(ctx, l.queue.reserved.BatchID)
}
func (l fakeBatchImageLock) RequeueAfter(ctx context.Context, d time.Duration) error {
	return l.queue.RequeueAfter(ctx, l.queue.reserved.BatchID, d)
}
