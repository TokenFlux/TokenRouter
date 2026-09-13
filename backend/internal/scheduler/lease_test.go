package scheduler

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// 取消与显式完成竞争时，资源只归还一次，且后取得的账号资源先于用户资源释放。
func TestLeaseConcurrentReleaseOwnership(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var order []string
	l := NewLease(ctx, ReleaseOnCancel, func() { order = append(order, "user") }, func() { order = append(order, "account") })
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() { defer wg.Done(); l.Release() }()
	}
	cancel()
	wg.Wait()
	require.Equal(t, []string{"account", "user"}, order)
}

// Qoder 完成释放模式在客户端已取消时仍保持容量，直到上游收尾明确释放。
func TestLeaseCompletionIgnoresClientCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	var count atomic.Int64
	l := NewLease(ctx, ReleaseOnCompletion, func() { count.Add(1) })
	cancel()
	require.Zero(t, count.Load())
	l.Release()
	l.Release()
	require.EqualValues(t, 1, count.Load())
}

// 已结束请求不能接纳晚到资源，资源必须立即归还给原拥有者。
func TestLeaseRejectsLateResource(t *testing.T) {
	l := NewLease(context.Background(), ReleaseOnCompletion)
	l.Release()
	var count int
	require.False(t, l.Own(func() { count++ }))
	require.Equal(t, 1, count)
}

// 本次成功会话保留与另一未完成尝试的失败清理互不覆盖。
func TestAttemptLeaseRetainsCompletionOutcome(t *testing.T) {
	parent := NewLease(context.Background(), ReleaseOnCompletion)
	var outcomes []bool
	var released int
	a := NewAttemptLease(parent, func(o AttemptOutcome) { outcomes = append(outcomes, o.Served) }, func() { released++ })
	a.Finish(AttemptOutcome{Served: true})
	NewAttemptLease(parent, func(o AttemptOutcome) { outcomes = append(outcomes, o.Served) }, func() { released++ })
	parent.Release()
	a.Release()
	require.Equal(t, []bool{true, false}, outcomes)
	require.Equal(t, 2, released)
}
