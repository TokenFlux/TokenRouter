package service

import (
	"context"
	"sort"
	"time"
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
	if resolver != nil {
		sub, err := resolver.ResolveUsableSubscriptionForGroup(ctx, userID, *groupID)
		if err == nil && sub != nil {
			return sub
		}
	}
	if repo == nil {
		return nil
	}
	subs, err := repo.ListActiveByUserID(ctx, userID)
	if err != nil {
		return nil
	}
	sort.SliceStable(subs, func(i, j int) bool {
		if subs[i].ExpiresAt.Equal(subs[j].ExpiresAt) {
			if subs[i].StartsAt.Equal(subs[j].StartsAt) {
				return subs[i].ID < subs[j].ID
			}
			return subs[i].StartsAt.Before(subs[j].StartsAt)
		}
		return subs[i].ExpiresAt.Before(subs[j].ExpiresAt)
	})
	now := time.Now()
	for i := range subs {
		sub := &subs[i]
		if sub.EffectiveStatus(now) != SubscriptionStatusActive {
			continue
		}
		if !subscriptionPlanIncludesGroup(sub.Plan, *groupID) {
			continue
		}
		if !subscriptionHasBillableCapacity(sub) {
			continue
		}
		return sub
	}
	return nil
}

type usageSubscriptionResolver interface {
	ResolveUsableSubscriptionForGroup(ctx context.Context, userID, groupID int64) (*UserSubscription, error)
}

func usageSubscriptionResolverFrom(repo UsageBillingRepository) usageSubscriptionResolver {
	resolver, _ := repo.(usageSubscriptionResolver)
	return resolver
}

func subscriptionPlanIncludesGroup(plan *SubscriptionPlan, groupID int64) bool {
	if plan == nil || groupID <= 0 {
		return false
	}
	for _, id := range plan.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

func subscriptionPlanGroupRateMultiplier(plan *SubscriptionPlan, groupID int64) (float64, bool) {
	if plan == nil || groupID <= 0 {
		return 0, false
	}
	if !subscriptionPlanIncludesGroup(plan, groupID) {
		return 0, false
	}
	if plan.GroupRateMultipliers == nil {
		return 1, true
	}
	if multiplier, ok := plan.GroupRateMultipliers[groupID]; ok && multiplier > 0 {
		return multiplier, true
	}
	return 1, true
}

func subscriptionHasBillableCapacity(sub *UserSubscription) bool {
	if sub == nil {
		return false
	}
	return subscriptionWindowHasCapacity(sub.DailyLimitUSD, sub.DailyUsageUSD) &&
		subscriptionWindowHasCapacity(sub.WeeklyLimitUSD, sub.WeeklyUsageUSD) &&
		subscriptionWindowHasCapacity(sub.MonthlyLimitUSD, sub.MonthlyUsageUSD)
}

func subscriptionWindowHasCapacity(limit *float64, used float64) bool {
	return limit == nil || *limit <= 0 || used < *limit
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
		return 1
	}
	groupDefault := group.RateMultiplier
	if resolveUserGroupRate == nil {
		return groupDefault
	}
	return resolveUserGroupRate(ctx, userID, *groupID, groupDefault)
}
