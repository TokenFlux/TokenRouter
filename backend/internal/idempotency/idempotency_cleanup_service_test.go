package idempotency

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type idempotencyCleanupRepoStub struct {
	deleteCalls int
	lastLimit   int
	deleteErr   error
}

func (r *idempotencyCleanupRepoStub) CreateProcessing(context.Context, *IdempotencyRecord) (bool, error) {
	return false, nil
}
func (r *idempotencyCleanupRepoStub) GetByScopeAndKeyHash(context.Context, string, string) (*IdempotencyRecord, error) {
	return nil, nil
}
func (r *idempotencyCleanupRepoStub) TryReclaim(context.Context, int64, string, time.Time, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (r *idempotencyCleanupRepoStub) ExtendProcessingLock(context.Context, int64, string, time.Time, time.Time) (bool, error) {
	return false, nil
}
func (r *idempotencyCleanupRepoStub) MarkSucceeded(context.Context, int64, int, string, time.Time) error {
	return nil
}
func (r *idempotencyCleanupRepoStub) MarkFailedRetryable(context.Context, int64, string, time.Time, time.Time) error {
	return nil
}
func (r *idempotencyCleanupRepoStub) DeleteExpired(_ context.Context, _ time.Time, limit int) (int64, error) {
	r.deleteCalls++
	r.lastLimit = limit
	if r.deleteErr != nil {
		return 0, r.deleteErr
	}
	return 1, nil
}

func TestNewIdempotencyCleanupService_UsesConfig(t *testing.T) {
	repo := &idempotencyCleanupRepoStub{}
	cfg := CleanupOptions{
		Interval: 7 * time.Second,
		Batch:    321,
	}
	svc := NewIdempotencyCleanupService(repo, cfg)
	require.Equal(t, 7*time.Second, svc.interval)
	require.Equal(t, 321, svc.batch)
}

func TestIdempotencyCleanupService_CleanupOnce(t *testing.T) {
	repo := &idempotencyCleanupRepoStub{}
	svc := NewIdempotencyCleanupService(repo, CleanupOptions{
		Batch: 99,
	})

	svc.cleanupOnce()
	require.Equal(t, 1, repo.deleteCalls)
	require.Equal(t, 99, repo.lastLimit)
}

// 被数据库阻塞的首轮回收必须完成后 Stop 才返回，重复启动不能产生第二轮。
type blockingCleanupRepository struct {
	IdempotencyRepository
	entered, release chan struct{}
}

func (r *blockingCleanupRepository) DeleteExpired(ctx context.Context, _ time.Time, limit int) (int64, error) {
	close(r.entered)
	select {
	case <-r.release:
		return 0, nil
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}
func TestCleanupStopWaitsForFirstRound(t *testing.T) {
	repo := &blockingCleanupRepository{entered: make(chan struct{}), release: make(chan struct{})}
	svc := NewIdempotencyCleanupService(repo, CleanupOptions{})
	require.Equal(t, 60*time.Second, svc.interval)
	require.Equal(t, 500, svc.batch)
	svc.Start()
	svc.Start()
	<-repo.entered
	stopped := make(chan struct{})
	go func() { svc.Stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("回收尚未完成")
	case <-time.After(20 * time.Millisecond):
	}
	close(repo.release)
	<-stopped
	svc.Stop()
	svc.Start()
}
func TestCleanupStopBeforeStart(t *testing.T) {
	repo := &idempotencyCleanupRepoStub{}
	svc := NewIdempotencyCleanupService(repo, CleanupOptions{})
	svc.Stop()
	svc.Start()
	svc.Stop()
	require.Zero(t, repo.deleteCalls)
}
