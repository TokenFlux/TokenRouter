package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type expiryLifecycleRepository struct {
	calls            atomic.Int32
	entered, release chan struct{}
	ignoreCancel     bool
}

func (r *expiryLifecycleRepository) AutoPauseExpiredAccounts(ctx context.Context, _ time.Time) (int64, error) {
	if r.calls.Add(1) == 1 {
		close(r.entered)
	}
	if r.ignoreCancel {
		<-r.release
		return 0, nil
	}
	<-ctx.Done()
	return 0, ctx.Err()
}
func TestExpiryConstructStartAndStopBoundaries(t *testing.T) {
	repo := &expiryLifecycleRepository{entered: make(chan struct{})}
	svc := NewExpiryService(repo, ExpiryOptions{Interval: time.Hour})
	require.Zero(t, repo.calls.Load())
	svc.Start()
	svc.Start()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, svc.StopContext(ctx))
	require.Equal(t, int32(1), repo.calls.Load())
	require.NoError(t, svc.StopContext(context.Background()))
	svc.Start()
	require.Equal(t, int32(1), repo.calls.Load())
}
func TestExpiryStopBudgetDoesNotReportUnfinishedScanAsComplete(t *testing.T) {
	repo := &expiryLifecycleRepository{entered: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true}
	svc := NewExpiryService(repo, ExpiryOptions{Interval: time.Hour})
	svc.Start()
	<-repo.entered
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	require.ErrorIs(t, svc.StopContext(ctx), context.DeadlineExceeded)
	close(repo.release)
	<-svc.runDone
	require.ErrorIs(t, svc.StopContext(context.Background()), context.DeadlineExceeded)
}
