package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	completion "github.com/TokenFlux/TokenRouter/internal/gateway/completion"
)

func resolveUsageSubscription(
	ctx context.Context,
	current *UserSubscription,
	repo UserSubscriptionRepository,
	resolver usageSubscriptionResolver,
	userID int64,
	groupID *int64,
) *UserSubscription {
	return completion.ResolveSubscription(ctx, current, resolver, userID, groupID)
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

func SubscriptionAllowsGroup(subscription *UserSubscription, groupID int64) bool {
	return billing.SubscriptionAllowsGroup(subscription, groupID)
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
	var projected *completion.GroupSnapshot
	if group != nil {
		projected = completionKey(&APIKey{Group: group}).Group
	}
	return completion.ResolveUsageRateMultiplier(ctx, userID, groupID, projected, defaultMultiplier, subscription, resolveUserGroupRate)
}
