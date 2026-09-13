package scheduler

import (
	"context"
	"time"
)

// WaitCounters 仅操作原有用户及账号计数，不承担槽位或资金校验。
type WaitCounters interface {
	IncrementWaitCount(context.Context, int64, int) (bool, error)
	DecrementWaitCount(context.Context, int64) error
	IncrementAccountWaitCount(context.Context, int64, int) (bool, error)
	DecrementAccountWaitCount(context.Context, int64) error
}

// WaitOwnership 区分未写入、确认取得和写入结果不明；只有确认取得才拥有补偿责任。
type WaitOwnership uint8

const (
	WaitNotCounted WaitOwnership = iota
	WaitCounted
	WaitUncertain
)

// WaitResult 即使被值复制也共用一次释放；故障放行不等于取得计数。
type WaitResult struct {
	Allowed   bool
	Ownership WaitOwnership
	resource  *Lease
}

func (r WaitResult) Release() { r.resource.Release() }

// EnterUserWait 保持原用户等待故障放行策略，同时明确计数所有权。
func EnterUserWait(ctx context.Context, cache WaitCounters, id int64, maxWait int, diagnostics Diagnostics) (WaitResult, error) {
	if cache == nil {
		return WaitResult{Allowed: true}, nil
	}
	return enterWait(ctx, id, maxWait, "user", cache.IncrementWaitCount, cache.DecrementWaitCount, diagnostics)
}

// EnterAccountWait 保持原账号等待上限和故障放行，不释放别的请求取得的计数。
func EnterAccountWait(ctx context.Context, cache WaitCounters, id int64, maxWait int, diagnostics Diagnostics) (WaitResult, error) {
	if cache == nil {
		return WaitResult{Allowed: true}, nil
	}
	return enterWait(ctx, id, maxWait, "account", cache.IncrementAccountWaitCount, cache.DecrementAccountWaitCount, diagnostics)
}

func enterWait(ctx context.Context, id int64, maxWait int, kind string, increment func(context.Context, int64, int) (bool, error), decrement func(context.Context, int64) error, diagnostics Diagnostics) (WaitResult, error) {
	allowed, err := increment(ctx, id, maxWait)
	if err != nil {
		diagnostics.printf("service.concurrency", "Warning: increment wait count failed for %s %d: %v", kind, id, err)
		return WaitResult{Allowed: true, Ownership: WaitUncertain}, nil
	}
	if !allowed {
		return WaitResult{}, nil
	}
	resource := NewLease(context.Background(), ReleaseOnCompletion, func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := decrement(cleanup, id); err != nil {
			diagnostics.printf("service.concurrency", "Warning: decrement wait count failed for %s %d: %v", kind, id, err)
		}
	})
	return WaitResult{Allowed: true, Ownership: WaitCounted, resource: resource}, nil
}
