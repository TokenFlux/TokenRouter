//go:build unit

// 这些旧测试入口只委托新实现；生产已无消费者。
package handler

import (
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"
)

func (h *GatewayHandler) calculateSubscriptionRemaining(sub *billingcore.UserSubscription) float64 {
	return billingcore.SubscriptionRemainingForDisplay(sub)
}
