package account

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// 用真实核心和 flight 验证：调用方取消不影响共享查询，资源停止仍能取消并等待它。
type oauthUsageLifecycleReader struct {
	OAuthUsageReader
	reads atomic.Int32
}

func (r *oauthUsageLifecycleReader) GetByID(context.Context, int64) (*Record, error) {
	r.reads.Add(1)
	return &Record{ID: 1, Platform: PlatformQoder, Type: AccountTypeOAuth, Status: StatusActive}, nil
}
func TestOAuthUsageLifecycleOwnsDetachedSharedQueries(t *testing.T) {
	reader := &oauthUsageLifecycleReader{}
	cache := NewOAuthUsageCache()
	entered := make(chan struct{})
	var calls atomic.Int32
	var core *OAuthUsageService
	var detached context.Context
	options := OAuthUsageOptions{Qoder: QoderUsageOptions{Fetch: func(ctx context.Context, _ *Record, _ func() time.Time) (*UsageInfo, error) {
		calls.Add(1)
		detached = ctx
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}}
	core = NewOAuthUsageService(reader, cache, nil, options)
	require.Zero(t, calls.Load())
	caller, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 2)
	go func() { _, err := core.GetUsage(caller, 1); done <- err }()
	<-entered
	go func() { _, err := core.GetUsage(context.Background(), 1); done <- err }()
	require.Eventually(t, func() bool {
		core.activity.mu.Lock()
		defer core.activity.mu.Unlock()
		return len(core.activity.active) == 5
	}, time.Second, time.Millisecond)
	cancel()
	require.NoError(t, detached.Err(), "调用方取消不得终止原共享查询")
	budget, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	require.NoError(t, core.StopContext(budget))
	for range 2 {
		require.ErrorIs(t, <-done, context.Canceled)
	}
	require.Equal(t, int32(1), calls.Load())
	reads := reader.reads.Load()
	_, err := core.GetUsage(context.Background(), 1)
	require.ErrorIs(t, err, ErrOAuthUsageStopped)
	require.Equal(t, reads, reader.reads.Load())
	require.NoError(t, core.StopContext(context.Background()))
}
func TestOAuthUsageLifecycleReportsUnfinishedProvider(t *testing.T) {
	reader := &oauthUsageLifecycleReader{}
	entered, release := make(chan struct{}), make(chan struct{})
	done := make(chan struct{})
	core := NewOAuthUsageService(reader, nil, nil, OAuthUsageOptions{Qoder: QoderUsageOptions{Fetch: func(context.Context, *Record, func() time.Time) (*UsageInfo, error) {
		close(entered)
		<-release
		return &UsageInfo{}, nil
	}}})
	go func() { defer close(done); _, _ = core.GetUsage(context.Background(), 1) }()
	<-entered
	budget, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	err := core.StopContext(budget)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.ErrorContains(t, err, "unfinished")
	close(release)
	<-done
	require.Same(t, err, core.StopContext(context.Background()))
}
