//go:build unit

package billing_test

import (
	"context"
	"testing"
	"time"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

func billingEligibilityLimitPtr(v float64) *float64 {
	return &v
}

func TestGetUsableSubscription_SkipsExhaustedSubscription(t *testing.T) {
	repo := billingtestkit.NewSubscriptionRepository()
	now := time.Now()
	windowStart := now.Add(-time.Hour)
	repo.Seed(&billing.UserSubscription{
		ID:               1,
		UserID:           1,
		PlanID:           1,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(time.Hour),
		Status:           billing.SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    10,
	})
	repo.Seed(&billing.UserSubscription{
		ID:               2,
		UserID:           1,
		PlanID:           2,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(2 * time.Hour),
		Status:           billing.SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    9,
	})
	svc := billing.NewSubscriptionService(subscriptionSelectionGroupFixture{}, repo, nil)

	sub, needsMaintenance, err := svc.GetUsableSubscription(context.Background(), 1)

	require.NoError(t, err)
	require.Equal(t, int64(2), sub.ID)
	require.False(t, needsMaintenance)
}

func TestGetUsableSubscription_AllExhaustedReturnsNotFound(t *testing.T) {
	repo := billingtestkit.NewSubscriptionRepository()
	now := time.Now()
	windowStart := now.Add(-time.Hour)
	repo.Seed(&billing.UserSubscription{
		ID:               1,
		UserID:           1,
		PlanID:           1,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(time.Hour),
		Status:           billing.SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    10,
	})
	svc := billing.NewSubscriptionService(subscriptionSelectionGroupFixture{}, repo, nil)

	_, _, err := svc.GetUsableSubscription(context.Background(), 1)

	require.ErrorIs(t, err, billing.ErrSubscriptionNotFound)
}
