//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentService) createOrderInTx(ctx context.Context, req CreateOrderRequest, user *User, plan *SubscriptionPlan, cfg *PaymentConfig, orderAmount, limitAmount float64, feeBreakdown payment.FeeBreakdown, sel *payment.InstanceSelection) (*dbent.PaymentOrder, error) {
	v, e := s.paymentCheckout().CreateOrderInTx(ctx, req, paymentBuyer(user), plan, cfg, orderAmount, limitAmount, feeBreakdown, sel)
	return paymentOrderEntity(v), e
}

func buildPaymentOrderProviderSnapshot(sel *payment.InstanceSelection, req CreateOrderRequest) map[string]any {
	return payment.BuildPaymentOrderProviderSnapshot(sel, req)
}

func (s *PaymentService) persistCreatePaymentResponse(ctx context.Context, orderID int64, sel *payment.InstanceSelection, pr *payment.CreatePaymentResponse) (*dbent.PaymentOrder, error) {
	v, e := s.paymentCheckout().PersistCreatePaymentResponse(ctx, orderID, sel, pr)
	return paymentOrderEntity(v), e
}
