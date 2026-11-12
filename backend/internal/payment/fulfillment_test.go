package payment

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fulfillment_test.go checks first-payment transaction binding
// against adapter-confirmed merchant numbers and persisted lifecycle state.
// These pure checks complement PostgreSQL tests that exercise competing
// callbacks, queries, and entitlement grants through the public payment flow.

// TestEasyPayMerchantNumberBindingLimits permits a confirmed initial echo only
// before payment in recoverable states. Other references, providers, and
// settled observations retain their original transaction binding.
func TestEasyPayMerchantNumberBindingLimits(t *testing.T) {
	confirmed := map[string]string{EasyPayConfirmedMerchantOrderNo: "merchant-order"}
	for _, status := range []string{OrderStatusPending, OrderStatusProcessing, OrderStatusExpired, OrderStatusCancelled} {
		t.Run(status, func(t *testing.T) {
			order := &Order{OutTradeNo: "merchant-order", PaymentTradeNo: "merchant-order", Status: status}
			require.NoError(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade", confirmed))
			require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade", nil))
			require.Error(t, validatePaymentTradeNumber(order, TypeEPUSDT, "real-trade", confirmed))
			require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "", confirmed))
			require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade",
				map[string]string{EasyPayConfirmedMerchantOrderNo: "another-order"}))
			now := time.Now()
			order.PaidAt = &now
			require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade", confirmed))
			order.PaidAt = nil
			order.PaymentTradeNo = "different-create-reference"
			require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade", confirmed))
		})
	}
	for _, status := range []string{OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted, OrderStatusFailed, OrderStatusRefunded, "UNKNOWN"} {
		order := &Order{OutTradeNo: "merchant-order", PaymentTradeNo: "merchant-order", Status: status}
		require.Error(t, validatePaymentTradeNumber(order, TypeEasyPay, "real-trade", confirmed), status)
		require.NoError(t, validatePaymentTradeNumber(order, TypeEasyPay, "merchant-order", nil), status)
	}
}
