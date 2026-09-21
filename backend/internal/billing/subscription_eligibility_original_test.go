//go:build unit

package billing

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func billingEligibilityLimitPtr(v float64) *float64 {
	return &v
}

func activeBillingEligibilitySubscription(limit float64, used float64) *UserSubscription {
	now := time.Now()
	windowStart := now.Add(-time.Hour)
	return &UserSubscription{
		ID:               1,
		UserID:           1,
		PlanID:           1,
		StartsAt:         now.Add(-time.Hour),
		ExpiresAt:        now.Add(time.Hour),
		Status:           SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(limit),
		DailyUsageUSD:    used,
	}
}

func newBillingEligibilityService(t *testing.T, balance float64) *Eligibility {
	t.Helper()

	svc := newOriginalEligibility(nil, &balanceLoadUserRepoStub{balance: balance}, nil, nil, &EligibilityOptions{RunMode: "standard"})
	svc.Start()
	t.Cleanup(svc.Stop)
	return svc
}

func TestBillingEligibility_ExhaustedSubscriptionWithZeroBalanceFails(t *testing.T) {
	for _, tt := range []struct {
		name string
		used float64
	}{
		{name: "exactly exhausted", used: 10},
		{name: "over limit", used: 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			svc := newBillingEligibilityService(t, 0)
			err := svc.CheckBillingEligibility(
				context.Background(),
				&UserSummary{ID: 1},
				nil,
				nil,
				activeBillingEligibilitySubscription(10, tt.used),
				"",
			)

			require.ErrorIs(t, err, ErrInsufficientBalance)
		})
	}
}

func TestBillingEligibility_ExhaustedSubscriptionFallsBackToBalance(t *testing.T) {
	svc := newBillingEligibilityService(t, 1)
	err := svc.CheckBillingEligibility(
		context.Background(),
		&UserSummary{ID: 1},
		nil,
		nil,
		activeBillingEligibilitySubscription(10, 10),
		"",
	)

	require.NoError(t, err)
}

func TestBillingEligibility_PreferredSubscriptionDoesNotFallBackToBalance(t *testing.T) {
	for _, tt := range []struct {
		name string
		used float64
	}{
		{name: "exactly exhausted", used: 10},
		{name: "already over limit", used: 12},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// 即使余额足够，锁定订阅的 Key 也不能在额度耗尽后自动切换成按量模式。
			svc := newBillingEligibilityService(t, 100)
			err := svc.CheckBillingEligibility(
				context.Background(),
				&UserSummary{ID: 1},
				&KeySnapshot{BillingMode: APIKeyBillingModeSubscription},
				nil,
				activeBillingEligibilitySubscription(10, tt.used),
				"",
			)

			require.ErrorIs(t, err, ErrPreferredSubscriptionInsufficient)
		})
	}
}

func TestBillingEligibility_PreferredSubscriptionRejectsFallbackGroupOutsidePlan(t *testing.T) {
	svc := newBillingEligibilityService(t, 1)
	subscription := activeBillingEligibilitySubscription(10, 0)
	subscription.Plan = &SubscriptionPlan{GroupIDs: []int64{10}}
	err := svc.CheckBillingEligibility(
		context.Background(),
		&UserSummary{ID: 1},
		&KeySnapshot{BillingMode: APIKeyBillingModeSubscription},
		&GroupSnapshot{ID: 11},
		subscription,
		"",
	)

	require.ErrorIs(t, err, ErrPreferredSubscriptionGroup)
}

func TestBillingEligibility_BalanceModeIgnoresProvidedSubscription(t *testing.T) {
	svc := newBillingEligibilityService(t, 0)
	err := svc.CheckBillingEligibility(
		context.Background(),
		&UserSummary{ID: 1},
		&KeySnapshot{BillingMode: APIKeyBillingModeBalance},
		nil,
		activeBillingEligibilitySubscription(10, 0),
		"",
	)

	require.ErrorIs(t, err, ErrInsufficientBalance)
}

func TestBillingEligibility_UnlimitedSubscriptionDoesNotRequireBalance(t *testing.T) {
	now := time.Now()
	svc := newOriginalEligibility(nil, nil, nil, nil, &EligibilityOptions{RunMode: "standard"})
	svc.Start()
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(
		context.Background(),
		&UserSummary{ID: 1},
		nil,
		nil,
		&UserSubscription{
			ID:              1,
			UserID:          1,
			PlanID:          1,
			StartsAt:        now.Add(-time.Hour),
			ExpiresAt:       now.Add(time.Hour),
			Status:          SubscriptionStatusActive,
			DailyLimitUSD:   billingEligibilityLimitPtr(0),
			WeeklyLimitUSD:  nil,
			MonthlyLimitUSD: billingEligibilityLimitPtr(0),
		},
		"",
	)

	require.NoError(t, err)
}
