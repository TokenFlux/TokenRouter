package account

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"sync/atomic"
	"testing"
	"time"
)

type usageQueryReader struct {
	value *Record
	reads atomic.Int32
}

func (r *usageQueryReader) GetByID(context.Context, int64) (*Record, error) {
	r.reads.Add(1)
	return CloneRecord(r.value), nil
}

type usageQueryExecution struct {
	started   chan struct{}
	cancelled chan struct{}
	release   chan struct{}
	ignore    bool
	calls     atomic.Int32
}

func (*usageQueryExecution) Available() bool        { return true }
func (*usageQueryExecution) Supports(string) bool   { return true }
func (*usageQueryExecution) BaseURL(*Record) string { return "https://usage.example" }
func (e *usageQueryExecution) Query(ctx context.Context, _ *Record, _ UpstreamUsageQueryConfig) (*UpstreamUsageInfo, error) {
	e.calls.Add(1)
	close(e.started)
	<-ctx.Done()
	close(e.cancelled)
	if e.ignore {
		<-e.release
	}
	return nil, ctx.Err()
}
func newUsageQueryTest(t *testing.T, ignore bool) (*UpstreamUsageService, *usageQueryReader, *usageQueryExecution) {
	t.Helper()
	r := &usageQueryReader{value: &Record{ID: 1, Type: AccountTypeAPIKey, Platform: PlatformOpenAI, Status: StatusActive}}
	e := &usageQueryExecution{started: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{}), ignore: ignore}
	return NewUpstreamUsageService(r, e, UpstreamUsageOptions{}), r, e
}
func waitUsageSignal(t *testing.T, ch <-chan struct{}) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("查询未到达预期阶段")
	}
}

// 首个 HTTP 等待方离开后，共享网络查询仍由账号实例持有并在停机时等待。
func TestUpstreamQueriesStopOwnsDetachedWork(t *testing.T) {
	s, r, e := newUsageQueryTest(t, false)
	require.Zero(t, r.reads.Load())
	require.Zero(t, e.calls.Load())
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := s.QueryAccount(ctx, 1); result <- err }()
	waitUsageSignal(t, e.started)
	cancel()
	require.ErrorIs(t, <-result, context.Canceled)
	select {
	case <-e.cancelled:
		t.Fatal("等待方取消提前终止共享查询")
	default:
	}
	stopCtx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	require.NoError(t, s.StopContext(stopCtx))
	waitUsageSignal(t, e.cancelled)
	require.NoError(t, s.StopContext(stopCtx))
	reads := r.reads.Load()
	_, err := s.QueryAccount(context.Background(), 1)
	require.ErrorIs(t, err, ErrUpstreamUsageStopped)
	require.Equal(t, reads, r.reads.Load())
}

// 不合作的供应商执行仍必须报告在途超时，迟到完成不得改写首次停止结果。
func TestUpstreamQueriesStopTimeoutRemainsFailure(t *testing.T) {
	s, _, e := newUsageQueryTest(t, true)
	waiter := make(chan error, 1)
	go func() { _, err := s.QueryAccount(context.Background(), 1); waiter <- err }()
	waitUsageSignal(t, e.started)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	err := s.StopContext(ctx)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.Contains(t, err.Error(), "upstream usage work remains unfinished")
	require.ErrorIs(t, <-waiter, context.Canceled)
	waitUsageSignal(t, e.cancelled)
	close(e.release)
	require.Same(t, err, s.StopContext(context.Background()))
}

// 结果复制保持 nil/空集合以及内部与 HTTP 的金额字段，并隔离所有嵌套可变值。
func TestUpstreamUsageResultCopiesAllValues(t *testing.T) {
	now := time.Now()
	amount := 3.0
	flag := true
	usage := &UpstreamUsageInfo{Balance: &UpstreamUsageAmount{Remaining: &amount}, Balances: []UpstreamUsageBalanceEntry{}, Available: &flag, Limits: []UpstreamUsageLimit{{Name: "day", Used: &amount, ResetAt: &now}}, Subscription: &UpstreamUsageSubscription{PlanName: "test", Remaining: &amount, ExpiresAt: &now}, ExpiresAt: &now}
	source := &UpstreamUsageQueryResult{Usage: usage, Balance: usage.Balance, Balances: usage.Balances, Available: usage.Available, Limits: usage.Limits, Subscription: usage.Subscription, ExpiresAt: &now}
	out := CloneUpstreamUsageResult(source)
	require.Equal(t, source, out)
	require.Same(t, out.Balance, out.Usage.Balance)
	*out.Balance.Remaining = 91
	*out.Limits[0].Used = 92
	*out.Limits[0].ResetAt = time.Time{}
	*out.Available = false
	*out.Subscription.Remaining = 93
	*out.ExpiresAt = time.Time{}
	require.Equal(t, 3.0, amount)
	require.True(t, flag)
	require.False(t, now.IsZero())
	require.Empty(t, out.Balances)
	require.NotNil(t, out.Balances)
	require.Nil(t, CloneUpstreamUsageResult(nil))
	require.Nil(t, CloneUpstreamUsageResult(&UpstreamUsageQueryResult{}).Balances)
}

// 缺失实例沿用原不可用错误和空指标，不因委托增加 panic。
func TestUpstreamQueriesNilCompatibility(t *testing.T) {
	var s *UpstreamUsageService
	_, err := s.QueryAccount(context.Background(), 1)
	require.True(t, errors.Is(err, ErrUpstreamUsageUnavailable))
	require.Empty(t, s.SnapshotMetrics().Counts)
}
