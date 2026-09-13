package account

import "slices"

// CloneUpstreamUsageResult 为每个等待方复制结果，保留 nil、空集合及所有金额类型。
func CloneUpstreamUsageResult(value *UpstreamUsageQueryResult) *UpstreamUsageQueryResult {
	if value == nil {
		return nil
	}
	out := *value
	out.Balance = cloneUsageAmount(value.Balance)
	out.Balances = slices.Clone(value.Balances)
	out.Available = clonePointer(value.Available)
	out.Limits = cloneUsageLimits(value.Limits)
	out.Subscription = cloneUsageSubscription(value.Subscription)
	out.ExpiresAt = clonePointer(value.ExpiresAt)
	if value.Usage != nil {
		usage := *value.Usage
		usage.Balance = cloneUsageAmount(usage.Balance)
		usage.Balances = slices.Clone(usage.Balances)
		usage.Available = clonePointer(usage.Available)
		usage.Limits = cloneUsageLimits(usage.Limits)
		usage.Subscription = cloneUsageSubscription(usage.Subscription)
		usage.ExpiresAt = clonePointer(usage.ExpiresAt)
		out.Usage = &usage
		// 正常查询同时提供平面 HTTP 字段与内部归一化值，保留这一结果内的金额别名。
		if value.Balance == value.Usage.Balance {
			out.Balance = usage.Balance
		}
		if value.Subscription == value.Usage.Subscription {
			out.Subscription = usage.Subscription
		}
	}
	return &out
}
func cloneUsageAmount(value *UpstreamUsageAmount) *UpstreamUsageAmount {
	if value == nil {
		return nil
	}
	out := *value
	out.Used = clonePointer(value.Used)
	out.Total = clonePointer(value.Total)
	out.Remaining = clonePointer(value.Remaining)
	return &out
}
func cloneUsageLimits(value []UpstreamUsageLimit) []UpstreamUsageLimit {
	out := slices.Clone(value)
	for i := range out {
		out[i].Used = clonePointer(out[i].Used)
		out[i].Limit = clonePointer(out[i].Limit)
		out[i].Remaining = clonePointer(out[i].Remaining)
		out[i].ResetAt = clonePointer(out[i].ResetAt)
	}
	return out
}
func cloneUsageSubscription(value *UpstreamUsageSubscription) *UpstreamUsageSubscription {
	if value == nil {
		return nil
	}
	out := *value
	out.Remaining = clonePointer(value.Remaining)
	out.ExpiresAt = clonePointer(value.ExpiresAt)
	out.Limits = cloneUsageLimits(value.Limits)
	return &out
}
