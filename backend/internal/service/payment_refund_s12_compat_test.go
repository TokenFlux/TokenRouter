//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentService) getOrderProviderInstance(ctx context.Context, o *dbent.PaymentOrder) (*dbent.PaymentProviderInstance, error) {
	v, e := s.paymentBindings().GetOrderProviderInstance(ctx, paymentOrderValue(o))
	return paymentInstanceToEnt(v), e
}

func (s *PaymentService) validateRefundRequest(ctx context.Context, oid, uid int64) (*dbent.PaymentOrder, error) {
	v, err := s.paymentRefunds().ValidateRefundRequest(ctx, oid, uid)
	return paymentOrderEntity(v), err
}

func (s *PaymentService) prepDeduct(ctx context.Context, o *dbent.PaymentOrder, p *RefundPlan, force bool) *RefundResult {
	v := paymentRefundPlan(p)
	result := s.paymentRefunds().PrepDeduct(ctx, paymentOrderValue(o), v, force)
	*p = *paymentRefundLegacyPlan(v)
	return result
}

func (s *PaymentService) gwRefund(ctx context.Context, p *RefundPlan) (*payment.RefundResponse, error) {
	return s.paymentRefunds().GwRefund(ctx, paymentRefundPlan(p))
}

func formatGatewayRefundAmount(amount float64, order *dbent.PaymentOrder) string {
	return payment.FormatGatewayRefundAmount(amount, paymentOrderValue(order))
}

func validateRefundProviderResponse(resp *payment.RefundResponse) error {
	return payment.ValidateRefundProviderResponse(resp)
}

func (s *PaymentService) finishRefund(ctx context.Context, p *RefundPlan, resp *payment.RefundResponse) (*RefundResult, error) {
	v := paymentRefundPlan(p)
	current, err := s.paymentRefundStore().Order(ctx, p.OrderID)
	if err != nil {
		return nil, err
	}
	receipt, err := s.paymentRefundStore().RefundRecovery(ctx, current)
	if err != nil {
		return nil, err
	}
	result, err := s.paymentRefunds().FinishRefund(ctx, v, receipt, resp)
	copyPaymentRefundPlan(p, v)
	return result, err
}

func (s *PaymentService) finalizePendingRefundSuccess(ctx context.Context, p *RefundPlan) (_ *RefundResult, err error) {
	v := paymentRefundPlan(p)
	result, err := s.paymentRefunds().FinalizePendingRefundSuccess(ctx, v)
	copyPaymentRefundPlan(p, v)
	return result, err
}

func (s *PaymentService) refundFinalizePlan(o *dbent.PaymentOrder, detail refundPendingAuditDetail) *RefundPlan {
	return paymentRefundLegacyPlan(s.paymentRefunds().RefundFinalizePlan(paymentOrderValue(o), detail))
}

type refundPendingAuditDetail = payment.RefundPendingDetail

func (s *PaymentService) latestRefundPendingDetail(ctx context.Context, oid int64) refundPendingAuditDetail {
	v, _ := s.paymentRefundStore().PendingDetail(ctx, oid)
	return v
}
