package service

import (
	"context"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

func resolveUsageSubscription(
	ctx context.Context,
	current *UserSubscription,
	repo UserSubscriptionRepository,
	resolver usageSubscriptionResolver,
	userID int64,
	groupID *int64,
) *UserSubscription {
	if current != nil {
		return current
	}
	if groupID == nil || *groupID <= 0 || userID <= 0 {
		return nil
	}
	if resolver == nil {
		return nil
	}
	sub, err := resolver.ResolveUsableSubscriptionForGroup(ctx, userID, *groupID)
	if err == nil && sub != nil {
		return sub
	}
	return nil
}

// resolveUsageSubscriptionForAPIKey 防止后扣阶段绕过 API Key 已选择的资金来源。
// 指定订阅仅信任鉴权阶段传入的同一订阅，余额模式不再尝试查找任何套餐。
func resolveUsageSubscriptionForAPIKey(
	ctx context.Context,
	apiKey *APIKey,
	current *UserSubscription,
	repo UserSubscriptionRepository,
	resolver usageSubscriptionResolver,
	userID int64,
	groupID *int64,
) *UserSubscription {
	switch APIKeyEffectiveBillingMode(apiKey) {
	case APIKeyBillingModeBalance:
		return nil
	case APIKeyBillingModeSubscription:
		return current
	default:
		return resolveUsageSubscription(ctx, current, repo, resolver, userID, groupID)
	}
}

type usageSubscriptionResolver interface {
	ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
}

type usagePreferredSubscriptionResolver interface {
	ResolvePreferredSubscriptionForGroup(ctx context.Context, userID, subscriptionID, groupID int64) (*UserSubscription, error)
}

func usageSubscriptionResolverFrom(repo UsageBillingRepository) usageSubscriptionResolver {
	resolver, _ := repo.(usageSubscriptionResolver)
	return resolver
}

func usagePreferredSubscriptionResolverFrom(repo UsageBillingRepository) usagePreferredSubscriptionResolver {
	resolver, _ := repo.(usagePreferredSubscriptionResolver)
	return resolver
}

// resolvePreferredUsageSubscription 仅返回指定订阅；不存在、分组不匹配或额度耗尽均不回退到其它套餐。
func resolvePreferredUsageSubscription(ctx context.Context, resolver usagePreferredSubscriptionResolver, userID, subscriptionID int64, groupID *int64) *UserSubscription {
	if resolver == nil || userID <= 0 || subscriptionID <= 0 || groupID == nil || *groupID <= 0 {
		return nil
	}
	subscription, err := resolver.ResolvePreferredSubscriptionForGroup(ctx, userID, subscriptionID, *groupID)
	if err != nil {
		return nil
	}
	return subscription
}

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

func SubscriptionAllowsGroup(subscription *UserSubscription, groupID int64) bool {
	return billing.SubscriptionAllowsGroup(subscription, groupID)
}

func subscriptionPlanGroupRateMultiplier(plan *SubscriptionPlan, groupID int64) (float64, bool) {
	if plan == nil || groupID <= 0 {
		return 0, false
	}
	if !subscriptionPlanIncludesGroup(plan, groupID) {
		return 0, false
	}
	if multiplier, ok := plan.GroupRateMultipliers[groupID]; ok && multiplier > 0 {
		return multiplier, true
	}
	return 0, false
}

func resolveUsageRateMultiplier(
	ctx context.Context,
	userID int64,
	groupID *int64,
	group *Group,
	defaultMultiplier float64,
	subscription *UserSubscription,
	resolveUserGroupRate func(context.Context, int64, int64, float64) float64,
) float64 {
	multiplier := defaultMultiplier
	if groupID == nil || group == nil {
		return multiplier
	}
	if subscription != nil {
		if multiplier, ok := subscriptionPlanGroupRateMultiplier(subscription.Plan, *groupID); ok {
			return multiplier
		}
		if subscriptionPlanIncludesGroup(subscription.Plan, *groupID) {
			return group.RateMultiplier
		}
		return multiplier
	}
	groupDefault := group.RateMultiplier
	if resolveUserGroupRate == nil {
		return groupDefault
	}
	return resolveUserGroupRate(ctx, userID, *groupID, groupDefault)
}
