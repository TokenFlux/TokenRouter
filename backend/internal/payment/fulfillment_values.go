// 履约共享的纯值规则只有此处一份实现。
package payment

import (
	"math"
	"strings"
)

func ExpectedNotificationProviderKeyForOrder(registry *Registry, order *Order, instanceProviderKey string) string {
	if order == nil {
		return strings.TrimSpace(instanceProviderKey)
	}

	orderProviderKey := refundStringValue(order.ProviderKey)
	if snapshot := PsOrderProviderSnapshot(order); snapshot != nil && snapshot.ProviderKey != "" {
		orderProviderKey = snapshot.ProviderKey
	}

	return ExpectedNotificationProviderKey(registry, order.PaymentType, orderProviderKey, instanceProviderKey)
}
func IsRefundStatus(s string) bool {
	switch s {
	case OrderStatusRefundRequested, OrderStatusRefunding, OrderStatusRefundPending, OrderStatusPartiallyRefunded, OrderStatusRefunded, OrderStatusRefundFailed:
		return true
	}
	return false
}
func OrderErrorMessage(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// OrderPurchasedReasoningPoints 返回订单用于邀请返利的推理积分基数。
// 余额订单使用到账积分；30 天订阅套餐按月、周、日的优先级取第一个有效额度。
func OrderPurchasedReasoningPoints(order *Order) (float64, bool) {
	if order == nil {
		return 0, false
	}

	var points float64
	switch order.OrderType {
	case OrderTypeBalance:
		points = order.Amount
	case OrderTypeSubscription:
		limits := []*float64{
			order.PlanSnapshot.MonthlyLimitUSD,
			order.PlanSnapshot.WeeklyLimitUSD,
			order.PlanSnapshot.DailyLimitUSD,
		}
		for _, limit := range limits {
			if limit == nil || *limit <= 0 || math.IsNaN(*limit) || math.IsInf(*limit, 0) {
				continue
			}
			return *limit, true
		}
		return 0, false
	default:
		return 0, false
	}

	if points <= 0 || math.IsNaN(points) || math.IsInf(points, 0) {
		return 0, false
	}
	return points, true
}
func FirstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
func ApplyInvoiceMetadata(change *OrderTransition, metadata map[string]string) {
	change.InvoiceID = strings.TrimSpace(metadata["invoice_id"])
	change.InvoiceURL = strings.TrimSpace(metadata["invoice_url"])
	change.InvoicePDF = strings.TrimSpace(metadata["invoice_pdf"])
	change.InvoiceStatus = strings.TrimSpace(metadata["invoice_status"])
}
