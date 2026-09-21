package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 对账按需启动的读取也必须被拥有者取消，而不只跟踪周期刷新。
type grokReconcileBlockingPager struct {
	entered, release, exited chan struct{}
	ignoreCancel             bool
	calls                    atomic.Int32
}

func (p *grokReconcileBlockingPager) ListOAuthRefreshCandidatePage(ctx context.Context, _ OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error) {
	p.calls.Add(1)
	close(p.entered)
	defer close(p.exited)
	if p.ignoreCancel {
		<-p.release
		return nil, ctx.Err()
	}
	<-ctx.Done()
	return nil, ctx.Err()
}
func TestGrokReconciliationLifecycleCancelsAcceptedRequest(t *testing.T) {
	pager := &grokReconcileBlockingPager{entered: make(chan struct{}), exited: make(chan struct{})}
	core := NewGrokReconciliationService(GrokReconciliationOptions{Pager: pager, Skew: time.Minute})
	require.Zero(t, pager.calls.Load())
	done := make(chan error, 1)
	go func() {
		_, err := core.ReconcileGrokOAuth(context.Background(), GrokOAuthReconcileInput{DryRun: true})
		done <- err
	}()
	<-pager.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, core.StopContext(ctx))
	require.ErrorIs(t, <-done, context.Canceled)
	_, err := core.ReconcileGrokOAuth(context.Background(), GrokOAuthReconcileInput{DryRun: true})
	require.ErrorIs(t, err, ErrRefreshStopped)
	require.Equal(t, int32(1), pager.calls.Load())
	require.NoError(t, core.StopContext(context.Background()))
}
func TestGrokReconciliationLifecycleReportsBlockedRead(t *testing.T) {
	pager := &grokReconcileBlockingPager{entered: make(chan struct{}), exited: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true}
	done := make(chan error, 1)
	t.Cleanup(func() { close(pager.release); <-done })
	core := NewGrokReconciliationService(GrokReconciliationOptions{Pager: pager, Skew: time.Minute})
	go func() {
		_, err := core.ReconcileGrokOAuth(context.Background(), GrokOAuthReconcileInput{DryRun: true})
		done <- err
	}()
	<-pager.entered
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := core.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	require.Same(t, err, core.StopContext(context.Background()))
}
