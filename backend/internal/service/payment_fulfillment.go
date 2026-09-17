package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

var ErrOrderNotFound = payment.ErrOrderNotFound

// --- Payment Notification & Fulfillment ---

func (s *PaymentService) HandlePaymentNotification(ctx context.Context, n *payment.PaymentNotification, pk string) error {
	return s.paymentFulfillment().HandlePaymentNotification(ctx, n, pk)
}

func (s *PaymentService) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
	return s.paymentFulfillment().ExecuteBalanceFulfillment(ctx, oid)
}

func (s *PaymentService) ExecuteSubscriptionFulfillment(ctx context.Context, oid int64) error {
	return s.paymentFulfillment().ExecuteSubscriptionFulfillment(ctx, oid)
}

func (s *PaymentService) RetryFulfillment(ctx context.Context, oid int64) error {
	return s.paymentFulfillment().RetryFulfillment(ctx, oid)
}
