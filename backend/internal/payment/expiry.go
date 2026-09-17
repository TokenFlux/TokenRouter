// 支付后台只依赖对账用例和锁端口，构造不启动，停止后不可重新开启。
package payment

import (
	"context"
	"fmt"
	"sync"
	"time"
)

const (
	OrderExpiryLeaderKey   = "payment:order:expiry:leader"
	OrderExpiryLeaderTTL   = 3 * time.Minute
	orderExpiryStepTimeout = 30 * time.Second
)

type OrderReconciler interface {
	ReconcilePendingPaymentOrders(context.Context) (int, error)
	ReconcileProcessingOrders(context.Context) (int, error)
	ReconcilePaidFulfillmentOrders(context.Context) (int, error)
	ExpireTimedOutOrders(context.Context) (int, error)
}

// ExpiryRuntime 由 app 投影原锁策略和日志；核心不持有 SQL、Redis 或日志后端。
type ExpiryRuntime struct {
	Acquire func(context.Context) (func(), bool)
	Observe func(string, int, error)
}
type OrderExpiry struct {
	service  OrderReconciler
	interval time.Duration
	runtime  ExpiryRuntime
	mu       sync.Mutex
	started  bool
	stopped  bool
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewOrderExpiry(service OrderReconciler, interval time.Duration, runtime ExpiryRuntime) *OrderExpiry {
	return &OrderExpiry{service: service, interval: interval, runtime: runtime, done: make(chan struct{})}
}

// ConfigureLease 仅用于启动前装配，运行后不替换本轮的锁拥有者。
func (s *OrderExpiry) ConfigureLease(acquire func(context.Context) (func(), bool)) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started && !s.stopped {
		s.runtime.Acquire = acquire
	}
}
func (s *OrderExpiry) Start(ctx context.Context) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started || s.stopped || s.service == nil || s.interval <= 0 {
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.cancel = cancel
	s.started = true
	go s.run(runCtx)
}
func (s *OrderExpiry) run(ctx context.Context) {
	defer close(s.done)
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	s.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.runOnce(ctx)
		}
	}
}
func (s *OrderExpiry) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	if !s.stopped {
		s.stopped = true
		if s.started {
			s.cancel()
		} else {
			close(s.done)
		}
	}
	done := s.done
	s.mu.Unlock()
	select {
	case <-done:
		return nil
	default:
	}
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("payment order expiry unfinished: %w", ctx.Err())
	}
}
func (s *OrderExpiry) runOnce(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	if s.runtime.Acquire != nil {
		lockCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		release, ok := s.runtime.Acquire(lockCtx)
		cancel()
		if !ok {
			return
		}
		if release != nil {
			defer release()
		}
	}
	steps := []struct {
		name string
		run  func(context.Context) (int, error)
	}{
		{"pending", s.service.ReconcilePendingPaymentOrders},
		{"processing", s.service.ReconcileProcessingOrders},
		{"fulfillment", s.service.ReconcilePaidFulfillmentOrders},
		{"expire", s.service.ExpireTimedOutOrders},
	}
	for _, step := range steps {
		if ctx.Err() != nil {
			return
		}
		stepCtx, cancel := context.WithTimeout(ctx, orderExpiryStepTimeout)
		n, err := step.run(stepCtx)
		cancel()
		if s.runtime.Observe != nil {
			s.runtime.Observe(step.name, n, err)
		}
	}
}
