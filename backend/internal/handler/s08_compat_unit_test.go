//go:build unit

// 这些旧测试入口只委托新实现；生产已无消费者。
package handler

import (
	billingcore "github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/service"
)

func (h *GatewayHandler) calculateSubscriptionRemaining(sub *service.UserSubscription) float64 {
	return billingcore.SubscriptionRemainingForDisplay(sub)
}
