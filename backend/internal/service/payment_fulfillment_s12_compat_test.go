//go:build unit

// 旧单测签名转接，新生产实现仅在所属模块；S15/S16 随测试迁移删除。
package service

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

const paymentFulfillmentLeaseDuration = payment.FulfillmentLeaseDuration

type paymentFulfillmentLease struct {
	version time.Time
}

func parseLegacyPaymentOrderID(orderID string, lookupErr error) (int64, bool) {
	return payment.ParseLegacyPaymentOrderID(orderID, dbent.IsNotFound(lookupErr))
}

func paymentAmountToleranceForCurrency(currency string) float64 {
	return payment.PaymentAmountToleranceForCurrency(currency)
}

func isValidProviderAmount(amount float64) bool { return payment.IsValidProviderAmount(amount) }

func validateProviderNotificationMetadata(order *dbent.PaymentOrder, providerKey string, metadata map[string]string) error {
	return payment.ValidateProviderNotificationMetadata(paymentOrderValue(order), providerKey, metadata)
}

func expectedNotificationProviderKey(registry *payment.Registry, orderPaymentType string, orderProviderKey string, instanceProviderKey string) string {
	return payment.ExpectedNotificationProviderKey(registry, orderPaymentType, orderProviderKey, instanceProviderKey)
}

func (s *PaymentService) executeFulfillment(ctx context.Context, oid int64) error {
	return s.paymentFulfillment().ExecuteFulfillment(ctx, oid)
}

func (s *PaymentService) acquirePaymentFulfillmentLease(ctx context.Context, o *dbent.PaymentOrder) (*paymentFulfillmentLease, error) {
	v, e := s.paymentFulfillment().AcquirePaymentFulfillmentLease(ctx, paymentOrderValue(o))
	return paymentFulfillmentLeaseLegacy(v), e
}

type redeemAction = payment.RedeemAction

const redeemActionCreate = payment.RedeemActionCreate

const redeemActionRedeem = payment.RedeemActionRedeem

const redeemActionSkipCompleted = payment.RedeemActionSkipCompleted

func resolveRedeemAction(existing *RedeemCode, lookupErr error) redeemAction {
	return payment.ResolveRedeemAction(existing, lookupErr)
}

func (s *PaymentService) markCompleted(ctx context.Context, o *dbent.PaymentOrder, lease *paymentFulfillmentLease, auditAction string) error {
	return s.paymentFulfillment().MarkCompleted(ctx, paymentOrderValue(o), paymentFulfillmentLeaseValue(lease), auditAction)
}

func (s *PaymentService) hasAuditLog(ctx context.Context, orderID int64, action string) bool {
	return s.paymentFulfillment().HasAuditLog(ctx, orderID, action)
}

func affiliateRebateBasePoints(o *dbent.PaymentOrder) float64 {
	return payment.AffiliateRebateBasePoints(paymentOrderValue(o))
}

func (s *PaymentService) markFailed(ctx context.Context, oid int64, lease *paymentFulfillmentLease, cause error) {
	s.paymentFulfillment().MarkFailed(ctx, oid, paymentFulfillmentLeaseValue(lease), cause)
}
