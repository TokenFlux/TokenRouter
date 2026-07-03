//go:build unit

package service

import (
	"context"
	"testing"
)

func TestResolveUsageRateMultiplier_SubscriptionUsesPlanGroupRate(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	got := resolveUsageRateMultiplier(
		context.Background(),
		101,
		&groupID,
		&Group{ID: groupID, RateMultiplier: 0.17},
		0.17,
		&UserSubscription{
			ID: 20,
			Plan: &SubscriptionPlan{
				ID:       30,
				GroupIDs: []int64{groupID},
				GroupRateMultipliers: map[int64]float64{
					groupID: 1,
				},
			},
		},
		nil,
	)

	if got != 1 {
		t.Fatalf("resolveUsageRateMultiplier() = %v, want 1", got)
	}
}

func TestResolveUsageRateMultiplier_SubscriptionFallsBackToGroupRateWhenPlanGroupRateMissing(t *testing.T) {
	t.Parallel()

	groupID := int64(7)
	got := resolveUsageRateMultiplier(
		context.Background(),
		101,
		&groupID,
		&Group{ID: groupID, RateMultiplier: 0.17},
		0.17,
		&UserSubscription{
			ID: 20,
			Plan: &SubscriptionPlan{
				ID:                   30,
				GroupIDs:             []int64{groupID},
				GroupRateMultipliers: map[int64]float64{},
			},
		},
		nil,
	)

	if got != 0.17 {
		t.Fatalf("resolveUsageRateMultiplier() = %v, want 0.17", got)
	}
}
