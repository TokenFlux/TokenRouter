//go:build unit

package batchimage

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type leaseLossQueue struct {
	BatchImageQueue
	lock *leaseLossLock
}

func (q leaseLossQueue) Reserve(context.Context, time.Duration) (ReservedBatchImageJob, error) {
	return ReservedBatchImageJob{BatchID: "imgbatch_lease"}, nil
}
func (q leaseLossQueue) TryAcquireJobLock(context.Context, string, time.Duration) (BatchImageJobLock, bool, error) {
	return q.lock, true, nil
}

type leaseLossLock struct {
	heartbeatErr, refreshErr  error
	acked, requeued, released atomic.Int64
}

func (l *leaseLossLock) Heartbeat(context.Context) error              { return l.heartbeatErr }
func (l *leaseLossLock) Refresh(context.Context, time.Duration) error { return l.refreshErr }
func (l *leaseLossLock) Ack(context.Context) error                    { l.acked.Add(1); return nil }
func (l *leaseLossLock) RequeueAfter(context.Context, time.Duration) error {
	l.requeued.Add(1)
	return nil
}
func (l *leaseLossLock) Release(context.Context) error { l.released.Add(1); return nil }

type leaseLossProcessor struct{ cause error }

func (p *leaseLossProcessor) Process(ctx context.Context, _ string) (BatchImageProcessResult, error) {
	<-ctx.Done()
	p.cause = context.Cause(ctx)
	return BatchImageProcessResult{Terminal: true}, nil
}

// B04 的取消必须传到实际 processor；即便其迟到返回终态，也不能 ACK 或重排接管者任务。
func TestWorkerLostLeaseCancelsProcessingWithoutQueueMutation(t *testing.T) {
	for _, test := range []struct {
		name               string
		heartbeat, refresh error
	}{
		{name: "heartbeat_owner_lost", heartbeat: ErrBatchImageLeaseLost},
		{name: "refresh_owner_lost", refresh: ErrBatchImageLeaseLost},
		{name: "redis_cannot_confirm_owner", heartbeat: errors.New("redis unavailable")},
	} {
		t.Run(test.name, func(t *testing.T) {
			lock := &leaseLossLock{heartbeatErr: test.heartbeat, refreshErr: test.refresh}
			processor := &leaseLossProcessor{}
			worker := NewBatchImageWorker(leaseLossQueue{lock: lock}, processor, BatchImageWorkerOptions{JobLockTTL: time.Second, StaleActiveAfter: time.Second})
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			require.ErrorIs(t, worker.RunOnce(ctx), ErrBatchImageLeaseLost)
			require.ErrorIs(t, processor.cause, ErrBatchImageLeaseLost)
			require.Zero(t, lock.acked.Load())
			require.Zero(t, lock.requeued.Load())
			require.Equal(t, int64(1), lock.released.Load())
		})
	}
}
