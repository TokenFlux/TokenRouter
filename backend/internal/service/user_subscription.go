// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package service

import (
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
)

const subscriptionDailyWindow = billing.SubscriptionDailyWindow

type UserSubscription = billing.UserSubscription

type SubscriptionWindowActivation = billing.SubscriptionWindowActivation
