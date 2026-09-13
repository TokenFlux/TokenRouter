// APIKeyBillingContext 是已解析资金来源的只读展示，不能视作新的结算授权。
package billing

type APIKeyBillingContext struct {
	Mode, Source string
	Subscription *UserSubscription
	Available    bool
}

// SubscriptionRemainingForDisplay 保留无额度上限时返回 -1 的旧公开契约。
func SubscriptionRemainingForDisplay(sub *UserSubscription) float64 {
	if sub == nil {
		return 0
	}
	if (sub.DailyLimitUSD == nil || *sub.DailyLimitUSD <= 0) && (sub.WeeklyLimitUSD == nil || *sub.WeeklyLimitUSD <= 0) && (sub.MonthlyLimitUSD == nil || *sub.MonthlyLimitUSD <= 0) {
		return -1
	}
	return sub.AvailableQuotaUSD()
}
