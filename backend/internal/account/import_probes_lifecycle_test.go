package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type importProbeLifecyclePort struct {
	calls                       atomic.Int32
	started, cancelled, release chan struct{}
	ignore                      bool
}

func (p *importProbeLifecyclePort) QueryQuota(ctx context.Context, _ int64) (*GrokImportProbeResult, error) {
	p.calls.Add(1)
	close(p.started)
	<-ctx.Done()
	close(p.cancelled)
	if p.ignore {
		<-p.release
	}
	return nil, ctx.Err()
}

// 待执行探测属于尽力工作；停止取消队列并等待已领取项，不能继续认领或宣称探测成功。
func TestImportProbesStopCancelsPendingAndWaits(t *testing.T) {
	p := &importProbeLifecyclePort{started: make(chan struct{}), cancelled: make(chan struct{})}
	queue := NewGrokImportProbeScheduler(GrokImportProbeOptions{Concurrency: 1})
	require.Zero(t, queue.workers)
	require.Zero(t, p.calls.Load())
	value := AccountSnapshot{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth}
	queue.Schedule(p, &value)
	waitUsageSignal(t, p.started)
	value.ID = 2
	queue.Schedule(p, &value)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, queue.StopContext(ctx))
	waitUsageSignal(t, p.cancelled)
	require.NoError(t, queue.StopContext(ctx))
	queue.Schedule(p, &value)
	require.Equal(t, int32(1), p.calls.Load())
	queue.mu.Lock()
	defer queue.mu.Unlock()
	require.Zero(t, queue.workers)
	require.Empty(t, queue.queue)
	require.Empty(t, queue.pending)
	require.Empty(t, queue.inFlight)
}
func TestImportProbesStopReportsNonCooperativeExecution(t *testing.T) {
	p := &importProbeLifecyclePort{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), ignore: true}
	queue := NewGrokImportProbeScheduler(GrokImportProbeOptions{Concurrency: 1})
	queue.Schedule(p, &AccountSnapshot{ID: 1, Platform: PlatformGrok, Type: AccountTypeOAuth})
	waitUsageSignal(t, p.started)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := queue.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "account import probes")
	close(p.release)
	require.Same(t, err, queue.StopContext(context.Background()))
}
