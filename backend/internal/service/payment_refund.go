package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// --- Refund Flow ---

func (s *PaymentService) RequestRefund(ctx context.Context, oid, uid int64, reason string) error {
	return s.paymentRefunds().RequestRefund(ctx, oid, uid, reason)
}

func (s *PaymentService) PrepareRefund(ctx context.Context, oid int64, amt float64, reason string, force, deduct bool) (*RefundPlan, *RefundResult, error) {
	v, result, err := s.paymentRefunds().PrepareRefund(ctx, oid, amt, reason, force, deduct)
	return paymentRefundLegacyPlan(v), result, err
}

func (s *PaymentService) ExecuteRefund(ctx context.Context, p *RefundPlan) (*RefundResult, error) {
	v := paymentRefundPlan(p)
	result, err := s.paymentRefunds().ExecuteRefund(ctx, v)
	copyPaymentRefundPlan(p, v)
	return result, err
}

func (s *PaymentService) QueryAndFinalizeRefund(ctx context.Context, oid int64) (*RefundResult, error) {
	return s.paymentRefunds().QueryAndFinalizeRefund(ctx, oid)
}

func (s *PaymentService) getRefundProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, error) {
	return s.paymentBindings().GetRefundProvider(ctx, paymentOrderValue(o))
}
