// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"errors"
	"sync"
)

// RefreshAttemptGate 是实际交换前的准入端口，不能提前消耗锁等待阶段的配额。
type RefreshAttemptGate interface {
	Acquire(context.Context) (func(), error)
}

// RefreshProviderState 只持有单轮平台失败状态；限速与并发门仍由整个 worker 共用。
type RefreshProviderState struct {
	rateGate            RefreshAttemptGate
	poolGate            *RefreshConcurrencyGate
	failureThreshold    int
	nonRetryable        func(error) bool
	mu                  sync.Mutex
	consecutiveFailures int
	tripped             bool
}

func NewRefreshProviderState(rate RefreshAttemptGate, pool *RefreshConcurrencyGate, threshold int, nonRetryable func(error) bool) *RefreshProviderState {
	return &RefreshProviderState{rateGate: rate, poolGate: pool, failureThreshold: threshold, nonRetryable: nonRetryable}
}
func (p *RefreshProviderState) IsTripped() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.tripped
}
func (p *RefreshProviderState) Acquire(ctx context.Context) (func(), error) {
	if p == nil || p.IsTripped() {
		return nil, ErrRefreshSkipped
	}
	release, err := p.poolGate.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	if p.IsTripped() {
		release()
		return nil, ErrRefreshSkipped
	}
	return release, nil
}
func (p *RefreshProviderState) AcquireRate(ctx context.Context) (func(), error) {
	if p == nil || p.IsTripped() {
		return nil, ErrRefreshSkipped
	}
	release := func() {}
	if p.rateGate != nil {
		var err error
		release, err = p.rateGate.Acquire(ctx)
		if err != nil {
			return nil, err
		}
	}
	if p.IsTripped() {
		release()
		return nil, ErrRefreshSkipped
	}
	return release, nil
}
func (p *RefreshProviderState) RecordResult(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if err == nil {
		p.consecutiveFailures = 0
		return
	}
	if errors.Is(err, ErrRefreshSkipped) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		var attemptTimeoutErr *RefreshAttemptTimeoutError
		if !errors.As(err, &attemptTimeoutErr) {
			return
		}
	}
	var attemptTimeoutErr *RefreshAttemptTimeoutError
	if errors.As(err, &attemptTimeoutErr) {
		p.consecutiveFailures++
		if p.consecutiveFailures >= p.failureThreshold {
			p.tripped = true
		}
		return
	}
	var providerErr *ProviderConfigurationRefreshError
	if errors.As(err, &providerErr) {
		p.tripped = true
		return
	}
	var containmentErr *ProviderCycleContainmentRefreshError
	if errors.As(err, &containmentErr) {
		p.tripped = true
		return
	}
	var permanentErr *AccountPermanentRefreshError
	if errors.As(err, &permanentErr) {
		p.consecutiveFailures = 0
		return
	}
	if p.nonRetryable != nil && p.nonRetryable(err) {
		// 永久账号凭据错误只影响该账号，不代表整个平台不健康。
		p.consecutiveFailures = 0
		return
	}
	p.consecutiveFailures++
	if p.consecutiveFailures >= p.failureThreshold {
		p.tripped = true
	}
}
