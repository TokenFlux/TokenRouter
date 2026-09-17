//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

const checkPaidResultCancelled = payment.LifecycleCheckPaidResultCancelled

const checkPaidResultProcessing = payment.LifecycleCheckPaidResultProcessing

const processingReconcileLimit = payment.LifecycleProcessingReconcileLimit

const fulfillmentRetryDelay = payment.LifecycleFulfillmentRetryDelay

const processingStaleAfter = payment.LifecycleProcessingStaleAfter

const paymentExpiryRetryDelay = payment.LifecyclePaymentExpiryRetryDelay

func paymentOrderQueryReference(order *dbent.PaymentOrder, prov payment.Provider) string {
	return payment.PaymentOrderQueryReference(paymentOrderValue(order), prov)
}

func (s *PaymentService) expireTimedOutOrdersAt(ctx context.Context, now time.Time) (int, error) {
	return s.paymentOrderLifecycle().ExpireTimedOutOrdersAt(ctx, now)
}

func (s *PaymentService) reconcileProcessingOrdersAt(ctx context.Context, now time.Time) (int, error) {
	return s.paymentOrderLifecycle().ReconcileProcessingOrdersAt(ctx, now)
}

func (s *PaymentService) reconcilePaidFulfillmentOrdersAt(ctx context.Context, now time.Time) (int, error) {
	return s.paymentOrderLifecycle().ReconcilePaidFulfillmentOrdersAt(ctx, now)
}

func reconcilePageIDs(ids []int64, cursor uint64, limit int) []int64 {
	return payment.ReconcilePageIDs(ids, cursor, limit)
}

func paymentOrderAllowsRegistryFallback(order *dbent.PaymentOrder) bool {
	return payment.PaymentOrderAllowsRegistryFallback(paymentOrderValue(order))
}
