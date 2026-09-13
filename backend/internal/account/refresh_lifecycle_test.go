package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 本地交换器用闸门控制是否响应取消，不依赖真实供应商或定时睡眠。
type lifecycleRefreshExecutor struct {
	started      chan struct{}
	release      chan struct{}
	ignoreCancel bool
	calls        atomic.Int32
}

func (e *lifecycleRefreshExecutor) CacheKey(*Record) string                  { return "lifecycle:account" }
func (e *lifecycleRefreshExecutor) CanRefresh(*Record) bool                  { return true }
func (e *lifecycleRefreshExecutor) NeedsRefresh(*Record, time.Duration) bool { return true }
func (e *lifecycleRefreshExecutor) Refresh(ctx context.Context, _ *Record) (map[string]any, error) {
	if e.calls.Add(1) == 1 {
		close(e.started)
	}
	if e.ignoreCancel {
		<-e.release
		return map[string]any{"access_token": "late"}, nil
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.release:
		return map[string]any{"access_token": "new"}, nil
	}
}

type lifecycleRefreshRepository struct{ writes atomic.Int32 }

func (*lifecycleRefreshRepository) GetByID(context.Context, int64) (*Record, error) {
	return &Record{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Status: StatusActive}, nil
}
func (r *lifecycleRefreshRepository) UpdateOAuthCredentialsIfUnchanged(context.Context, CredentialVersion, map[string]any) (bool, error) {
	r.writes.Add(1)
	return true, nil
}

func TestRefreshStopCancelsExchangeAndQueuedWork(t *testing.T) {
	repo := &lifecycleRefreshRepository{}
	executor := &lifecycleRefreshExecutor{started: make(chan struct{}), release: make(chan struct{})}
	api := NewOAuthRefreshAPI(repo, nil, RefreshOptions{})
	require.Zero(t, executor.calls.Load())
	results := make(chan error, 2)
	refresh := func() {
		_, err := api.RefreshIfNeeded(context.Background(), &Record{ID: 1}, executor, time.Minute)
		results <- err
	}
	go refresh()
	<-executor.started
	go refresh()
	require.Eventually(t, func() bool {
		api.activity.mu.Lock()
		defer api.activity.mu.Unlock()
		return len(api.activity.active) == 2
	}, time.Second, time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, api.StopContext(ctx))
	for range 2 {
		require.ErrorIs(t, <-results, context.Canceled)
	}
	require.Equal(t, int32(1), executor.calls.Load())
	require.Zero(t, repo.writes.Load())
	_, err := api.RefreshIfNeeded(context.Background(), &Record{ID: 1}, executor, time.Minute)
	require.ErrorIs(t, err, ErrRefreshStopped)
	require.NoError(t, api.StopContext(context.Background()))
}

func TestRefreshStopTimeoutDoesNotReportDrainOrPersistLateResult(t *testing.T) {
	repo := &lifecycleRefreshRepository{}
	executor := &lifecycleRefreshExecutor{started: make(chan struct{}), release: make(chan struct{}), ignoreCancel: true}
	api := NewOAuthRefreshAPI(repo, nil, RefreshOptions{})
	finished := make(chan error, 1)
	go func() {
		_, err := api.RefreshIfNeeded(context.Background(), &Record{ID: 1}, executor, time.Minute)
		finished <- err
	}()
	<-executor.started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := api.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	close(executor.release)
	require.ErrorIs(t, <-finished, context.Canceled)
	require.Zero(t, repo.writes.Load())
	// 第一次停止已超时，后续即使任务退出也不能改写那次停止的结果。
	require.Same(t, err, api.StopContext(context.Background()))
}
