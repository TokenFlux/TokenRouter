// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package billing

func subscriptionPlanIncludesGroup(plan *SubscriptionPlan, groupID int64) bool {
	if plan == nil || groupID <= 0 {
		return false
	}
	if len(plan.GroupIDs) == 0 {
		return true
	}
	for _, id := range plan.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// SubscriptionAllowsGroup 返回订阅套餐是否覆盖目标分组。
// 未配置套餐分组代表套餐不限制分组；缺失套餐或未知分组不应被指定订阅模式放行。
func SubscriptionAllowsGroup(subscription *UserSubscription, groupID int64) bool {
	if subscription == nil || subscription.Plan == nil || groupID <= 0 {
		return false
	}
	return subscriptionPlanIncludesGroup(subscription.Plan, groupID)
}
