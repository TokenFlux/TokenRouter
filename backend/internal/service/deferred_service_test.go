package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 通过阻塞实际仓储调用，验证最终 flush 与周期 flush 串行且不会丢失待写项。
type deferredDrainRepository struct {
	AccountRepository
	mu               sync.Mutex
	calls            int
	entered, release chan struct{}
	err              error
}

func (r *deferredDrainRepository) BatchUpdateLastUsed(ctx context.Context, updates map[int64]time.Time) error {
	r.mu.Lock()
	r.calls++
	call := r.calls
	r.mu.Unlock()
	if call == 1 && r.entered != nil {
		close(r.entered)
		select {
		case <-r.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return r.err
}
func TestDeferredStopSerializesFinalFlush(t *testing.T) {
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	require.NoError(t, wheel.Start())
	defer wheel.Stop()
	repo := &deferredDrainRepository{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewDeferredService(repo, wheel, time.Hour)
	svc.Start()
	svc.ScheduleLastUsedUpdate(1)
	flushed := make(chan struct{})
	go func() { svc.flushLastUsed(); close(flushed) }()
	<-repo.entered
	svc.ScheduleLastUsedUpdate(2)
	stopped := make(chan error, 1)
	go func() { stopped <- svc.Stop() }()
	select {
	case <-stopped:
		t.Fatal("周期写回未结束")
	case <-time.After(20 * time.Millisecond):
	}
	close(repo.release)
	<-flushed
	require.NoError(t, <-stopped)
	require.NoError(t, svc.Stop())
	svc.Start()
	repo.mu.Lock()
	require.Equal(t, 2, repo.calls)
	repo.mu.Unlock()
}
func TestDeferredFinalFlushReportsFailure(t *testing.T) {
	failure := errors.New("database unavailable")
	repo := &deferredDrainRepository{err: failure}
	wheel, err := NewTimingWheelService()
	require.NoError(t, err)
	svc := NewDeferredService(repo, wheel, time.Hour)
	svc.ScheduleLastUsedUpdate(1)
	require.ErrorIs(t, svc.Stop(), failure)
	require.ErrorIs(t, svc.Stop(), failure)
	_, retained := svc.lastUsedUpdates.Load(int64(1))
	require.True(t, retained)
}
