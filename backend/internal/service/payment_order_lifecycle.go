package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
)

// --- Cancel & Expire ---

var createPaymentProviderFromInstance = provider.CreateProvider

func (s *PaymentService) CancelOrder(ctx context.Context, orderID, userID int64) (string, error) {
	return s.paymentOrderLifecycle().CancelOrder(ctx, orderID, userID)
}

func (s *PaymentService) AdminCancelOrder(ctx context.Context, orderID int64) (string, error) {
	return s.paymentOrderLifecycle().AdminCancelOrder(ctx, orderID)
}

func (s *PaymentService) ForceExpireOrder(ctx context.Context, orderID int64, reason string) error {
	return s.paymentOrderLifecycle().ForceExpireOrder(ctx, orderID, reason)
}

func (s *PaymentService) VerifyOrderByOutTradeNo(ctx context.Context, outTradeNo string, userID int64) (*dbent.PaymentOrder, error) {
	v, e := s.paymentOrderLifecycle().VerifyOrderByOutTradeNo(ctx, outTradeNo, userID)
	return paymentOrderEntity(v), e
}

func (s *PaymentService) ReconcilePendingPaymentOrders(ctx context.Context) (int, error) {
	return s.paymentOrderLifecycle().ReconcilePendingPaymentOrders(ctx)
}

func (s *PaymentService) VerifyOrderPublic(ctx context.Context, outTradeNo string) (*dbent.PaymentOrder, error) {
	v, e := s.paymentOrderLifecycle().VerifyOrderPublic(ctx, outTradeNo)
	return paymentOrderEntity(v), e
}

func (s *PaymentService) ExpireTimedOutOrders(ctx context.Context) (int, error) {
	return s.paymentOrderLifecycle().ExpireTimedOutOrders(ctx)
}

func (s *PaymentService) ReconcileProcessingOrders(ctx context.Context) (int, error) {
	return s.paymentOrderLifecycle().ReconcileProcessingOrders(ctx)
}

func (s *PaymentService) ReconcilePaidFulfillmentOrders(ctx context.Context) (int, error) {
	return s.paymentOrderLifecycle().ReconcilePaidFulfillmentOrders(ctx)
}
