// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	sync "sync"
	time "time"
)

type RefreshRateGate struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

type RefreshConcurrencyGate struct {
	slots chan struct{}
}

func NewRefreshRateGate(qps int) *RefreshRateGate {
	if qps <= 0 {
		return &RefreshRateGate{}
	}
	return NewRefreshRateGateWithInterval(time.Second / time.Duration(qps))
}

// NewRefreshRateGateWithInterval 是测试速率槽预约和取消的窄化时间注入点。
func NewRefreshRateGateWithInterval(interval time.Duration) *RefreshRateGate {
	return &RefreshRateGate{interval: interval}
}

func (g *RefreshRateGate) ReserveSlot(now time.Time) time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.next.Before(now) {
		g.next = now
	}
	slot := g.next
	g.next = g.next.Add(g.interval)
	return slot
}

func (g *RefreshRateGate) Wait(ctx context.Context) error {
	if g == nil || g.interval <= 0 {
		return nil
	}
	slot := g.ReserveSlot(time.Now())

	Wait := time.Until(slot)
	if Wait <= 0 {
		return nil
	}
	timer := time.NewTimer(Wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (g *RefreshRateGate) Acquire(ctx context.Context) (func(), error) {
	if err := g.Wait(ctx); err != nil {
		return nil, err
	}
	return func() {}, nil
}

func NewRefreshConcurrencyGate(concurrency int) *RefreshConcurrencyGate {
	if concurrency < 1 {
		concurrency = 1
	}
	return &RefreshConcurrencyGate{slots: make(chan struct{}, concurrency)}
}

func (g *RefreshConcurrencyGate) Acquire(ctx context.Context) (func(), error) {
	if g == nil {
		return func() {}, nil
	}
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// InFlight 返回当前占用，不暴露计数信道或允许外部释放槽位。
func (g *RefreshConcurrencyGate) InFlight() int {
	if g == nil {
		return 0
	}
	return len(g.slots)
}
