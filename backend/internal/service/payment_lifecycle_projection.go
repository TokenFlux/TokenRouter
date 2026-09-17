// 旧运行入口委托唯一 payment 对账实例；游标和轮次只在新实例保存。
package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/servertiming"
)

func (s *PaymentService) paymentOrderLifecycle() *payment.OrderLifecycle {
	s.orderLifecycleOnce.Do(func() {
		if s.orderLifecycle == nil {
			s.orderLifecycle = payment.NewOrderLifecycle(s.paymentFulfillment(), s.paymentResume(), func(ctx context.Context) func() { return servertiming.ObserveDependency(ctx, "payment") })
		}
	})
	return s.orderLifecycle
}
func (s *PaymentService) BindOrderLifecycle(core *payment.OrderLifecycle) { s.orderLifecycle = core }
