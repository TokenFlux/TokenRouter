// 旧入口只投影锁与日志，运行状态由 payment.OrderExpiry 唯一持有。
package service

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/google/uuid"
)

type PaymentOrderExpiryService struct {
	runner     *payment.OrderExpiry
	instanceID string
}

func NewPaymentOrderExpiryService(service *PaymentService, interval time.Duration) *PaymentOrderExpiryService {
	var reconciler payment.OrderReconciler
	if service != nil {
		reconciler = service
	}
	return &PaymentOrderExpiryService{runner: payment.NewOrderExpiry(reconciler, interval, payment.ExpiryRuntime{Observe: observePaymentExpiry}), instanceID: uuid.NewString()}
}
func (s *PaymentOrderExpiryService) SetLeaderLock(cache LeaderLockCache, db *sql.DB) {
	if s == nil {
		return
	}
	s.runner.ConfigureLease(func(ctx context.Context) (func(), bool) {
		return tryAcquireSingletonLeaderLock(ctx, cache, db, payment.OrderExpiryLeaderKey, s.instanceID, payment.OrderExpiryLeaderTTL)
	})
}
func (s *PaymentOrderExpiryService) Start() { s.StartContext(context.Background()) }
func (s *PaymentOrderExpiryService) StartContext(ctx context.Context) {
	if s != nil {
		s.runner.Start(ctx)
	}
}
func (s *PaymentOrderExpiryService) Stop() {
	if s != nil {
		_ = s.runner.StopContext(context.Background())
	}
}
func (s *PaymentOrderExpiryService) StopContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.runner.StopContext(ctx)
}

// 日志保持原名称和级别，后续完整支付装配时移入 app。
func observePaymentExpiry(step string, count int, err error) {
	if err != nil {
		switch step {
		case "pending":
			slog.Warn("[PaymentOrderExpiry] failed to reconcile pending payment orders", "error", err)
		case "processing":
			slog.Warn("[PaymentOrderExpiry] failed to reconcile processing payment orders", "error", err)
		case "fulfillment":
			slog.Warn("[PaymentOrderExpiry] failed to reconcile paid order fulfillment", "error", err)
		case "expire":
			slog.Error("[PaymentOrderExpiry] failed to expire orders", "error", err)
		}
		return
	}
	if count <= 0 {
		return
	}
	switch step {
	case "pending":
		slog.Info("[PaymentOrderExpiry] reconciled paid orders", "count", count)
	case "processing":
		slog.Info("[PaymentOrderExpiry] reconciled paid processing orders", "count", count)
	case "fulfillment":
		slog.Info("[PaymentOrderExpiry] reconciled paid order fulfillment", "count", count)
	case "expire":
		slog.Info("[PaymentOrderExpiry] expired timed-out orders", "count", count)
	}
}

// WrapPaymentOrderExpiryService 只兼容应用生命周期旧字段，不重新构造锁或 worker。
func WrapPaymentOrderExpiryService(runner *payment.OrderExpiry) *PaymentOrderExpiryService {
	return &PaymentOrderExpiryService{runner: runner}
}
