package billing

import "context"

// PreferredSubscriptionReader 只查询明确指定的订阅，不提供自动选择或写入能力。
type PreferredSubscriptionReader interface {
	ResolvePreferredSubscriptionForGroup(context.Context, int64, int64, int64) (*UserSubscription, error)
}

// ResolvePreferredSubscription 保留无效输入、读取失败与不存在时不回退的语义。
func ResolvePreferredSubscription(ctx context.Context, reader PreferredSubscriptionReader, userID, subscriptionID int64, groupID *int64) *UserSubscription {
	if reader == nil || userID <= 0 || subscriptionID <= 0 || groupID == nil || *groupID <= 0 {
		return nil
	}
	value, err := reader.ResolvePreferredSubscriptionForGroup(ctx, userID, subscriptionID, *groupID)
	if err != nil {
		return nil
	}
	return value
}
