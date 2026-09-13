package account

import (
	"context"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

// 同时接纳的周期扫描和管理对账由同一拥有者取消，不提前关闭其共享依赖。
type backgroundRefreshBlockingPager struct {
	entered  chan struct{}
	released atomic.Int32
	calls    atomic.Int32
}

func (p *backgroundRefreshBlockingPager) ListOAuthRefreshCandidatePage(ctx context.Context, _ OAuthRefreshPageOptions) (*OAuthRefreshCandidatePage, error) {
	p.calls.Add(1)
	p.entered <- struct{}{}
	<-ctx.Done()
	p.released.Add(1)
	return nil, ctx.Err()
}

type backgroundRefreshUnusedProvider struct{}

func (backgroundRefreshUnusedProvider) CanRefresh(*Record) bool                  { return true }
func (backgroundRefreshUnusedProvider) NeedsRefresh(*Record, time.Duration) bool { return true }
func (backgroundRefreshUnusedProvider) Refresh(context.Context, *Record) (map[string]any, error) {
	return nil, nil
}
func TestBackgroundRefreshStopsBothEntrypoints(t *testing.T) {
	pager := &backgroundRefreshBlockingPager{entered: make(chan struct{}, 2)}
	core := NewBackgroundRefreshService(BackgroundRefreshOptions{Tuning: &RefreshTuning{Enabled: true}, Pager: pager, Registrations: []RefreshRegistration{{Platform: PlatformGrok, Refresher: backgroundRefreshUnusedProvider{}}}, Reconciliation: GrokReconciliationOptions{Skew: time.Minute}})
	require.Zero(t, pager.calls.Load())
	scanDone := make(chan struct{})
	go func() { core.ScanCycle(context.Background()); close(scanDone) }()
	reconcileDone := make(chan error, 1)
	go func() {
		_, err := core.ReconcileGrokOAuth(context.Background(), GrokOAuthReconcileInput{DryRun: true})
		reconcileDone <- err
	}()
	<-pager.entered
	<-pager.entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, core.StopContext(ctx))
	<-scanDone
	require.ErrorIs(t, <-reconcileDone, context.Canceled)
	require.Equal(t, int32(2), pager.released.Load())
	require.NoError(t, core.StopContext(context.Background()))
	require.NoError(t, core.StartContext(context.Background()))
	core.ScanCycle(context.Background())
	_, err := core.ReconcileGrokOAuth(context.Background(), GrokOAuthReconcileInput{})
	require.ErrorIs(t, err, ErrRefreshStopped)
	require.Equal(t, int32(2), pager.calls.Load())
}
