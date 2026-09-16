package search

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type quotaFixture struct {
	mu               sync.Mutex
	used, decrements int64
	uncertain        bool
	releaseDeadline  bool
}

func (q *quotaFixture) Increment(context.Context, string, time.Duration) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.used++
	if q.uncertain {
		return 0, errors.New("unknown result")
	}
	return q.used, nil
}
func (q *quotaFixture) Decrement(ctx context.Context, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.decrements++
	_, q.releaseDeadline = ctx.Deadline()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	q.used--
	return nil
}
func (q *quotaFixture) Usage(context.Context, string) (int64, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.used, nil
}
func (q *quotaFixture) Reset(context.Context, string) error                   { return nil }
func (q *quotaFixture) MarkProxy(context.Context, int64, time.Duration) error { return nil }
func (q *quotaFixture) ProxyAvailable(context.Context, int64) bool            { return true }
func TestS10ConfirmedReservationReleasesOnlyOnce(t *testing.T) {
	q := &quotaFixture{used: 1}
	manager := NewManager(nil, q, noSearchExecutor{}, nil)
	lease := &quotaReservation{manager: manager, config: ProviderConfig{Type: ProviderTypeBrave, QuotaLimit: 10}, acquired: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var wg sync.WaitGroup
	for range 16 {
		wg.Go(func() { lease.release(ctx) })
	}
	wg.Wait()
	require.Equal(t, int64(0), q.used)
	require.Equal(t, int64(1), q.decrements)
	require.True(t, q.releaseDeadline)
}
func TestS10UncertainReservationDoesNotCompensate(t *testing.T) {
	q := &quotaFixture{uncertain: true}
	manager := NewManager(nil, q, noSearchExecutor{}, nil)
	allowed, acquired := manager.tryReserveQuota(context.Background(), ProviderConfig{Type: ProviderTypeBrave, QuotaLimit: 10})
	require.True(t, allowed)
	require.False(t, acquired)
	lease := &quotaReservation{manager: manager, config: ProviderConfig{Type: ProviderTypeBrave, QuotaLimit: 10}, acquired: acquired}
	lease.release(context.Background())
	require.Zero(t, q.decrements)
}
func TestS10SearchStopWaitsAndRejectsNewWork(t *testing.T) {
	group := NewWorkGroup()
	done, err := group.Begin()
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	require.ErrorIs(t, group.Stop(ctx), context.DeadlineExceeded)
	_, err = group.Begin()
	require.Error(t, err)
	done()
	done()
	require.NoError(t, group.Stop(context.Background()))
}

// B07 清理预算耗尽时停止等待仍可报告未完成，不能把退额失败记成成功。
type blockedQuotaCleanup struct {
	quotaFixture
	entered chan context.Context
	err     error
}

func (q *blockedQuotaCleanup) Decrement(ctx context.Context, _ string) error {
	q.entered <- ctx
	<-ctx.Done()
	q.err = ctx.Err()
	return q.err
}

type canceledSearchExecutor struct {
	noSearchExecutor
	cancel context.CancelFunc
}

func (e canceledSearchExecutor) Search(ctx context.Context, _ ProviderConfig, _ SearchRequest) (*SearchResponse, error) {
	e.cancel()
	return nil, ctx.Err()
}

func TestS10QuotaCleanupBudgetAndShutdownWait(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	quota := &blockedQuotaCleanup{entered: make(chan context.Context, 1)}
	work := NewWorkGroup()
	manager := NewManager([]ProviderConfig{{Type: ProviderTypeBrave, APIKey: "fixture", QuotaLimit: 10}}, quota, canceledSearchExecutor{cancel: cancel}, work)
	result := make(chan error, 1)
	go func() {
		_, _, err := manager.SearchWithBestProvider(ctx, SearchRequest{Query: "fixture"})
		result <- err
	}()
	cleanup := <-quota.entered
	require.NoError(t, cleanup.Err())
	deadline, ok := cleanup.Deadline()
	require.True(t, ok)
	require.LessOrEqual(t, time.Until(deadline), 3*time.Second)
	stop, stopCancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stopCancel()
	require.ErrorIs(t, work.Stop(stop), context.DeadlineExceeded)
	select {
	case err := <-result:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(4 * time.Second):
		t.Fatal("搜索额度清理未遵守三秒预算")
	}
	require.ErrorIs(t, quota.err, context.DeadlineExceeded)
	require.Equal(t, int64(1), quota.used)
	require.NoError(t, work.Stop(context.Background()))
}
