//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
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

func newBillingEligibilityService(t *testing.T, balance float64) *BillingCacheService {
	t.Helper()

	svc := NewBillingCacheService(nil, &mockUserRepo{
		getByIDUser: &User{ID: 1, Balance: balance},
	}, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeStandard})
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
				&User{ID: 1},
				nil,
				nil,
				activeBillingEligibilitySubscription(10, tt.used),
			)

			require.ErrorIs(t, err, ErrInsufficientBalance)
		})
	}
}

func TestBillingEligibility_ExhaustedSubscriptionFallsBackToBalance(t *testing.T) {
	svc := newBillingEligibilityService(t, 1)
	err := svc.CheckBillingEligibility(
		context.Background(),
		&User{ID: 1},
		nil,
		nil,
		activeBillingEligibilitySubscription(10, 10),
	)

	require.NoError(t, err)
}

func TestBillingEligibility_UnlimitedSubscriptionDoesNotRequireBalance(t *testing.T) {
	now := time.Now()
	svc := NewBillingCacheService(nil, nil, nil, nil, nil, nil, &config.Config{RunMode: config.RunModeStandard})
	t.Cleanup(svc.Stop)

	err := svc.CheckBillingEligibility(
		context.Background(),
		&User{ID: 1},
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
	)

	require.NoError(t, err)
}

func TestGetUsableSubscription_SkipsExhaustedSubscription(t *testing.T) {
	repo := newSubscriptionUserSubRepoStub()
	now := time.Now()
	windowStart := now.Add(-time.Hour)
	repo.seed(&UserSubscription{
		ID:               1,
		UserID:           1,
		PlanID:           1,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(time.Hour),
		Status:           SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    10,
	})
	repo.seed(&UserSubscription{
		ID:               2,
		UserID:           1,
		PlanID:           2,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(2 * time.Hour),
		Status:           SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    9,
	})
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

	sub, needsMaintenance, err := svc.GetUsableSubscription(context.Background(), 1)

	require.NoError(t, err)
	require.Equal(t, int64(2), sub.ID)
	require.False(t, needsMaintenance)
}

func TestGetUsableSubscription_AllExhaustedReturnsNotFound(t *testing.T) {
	repo := newSubscriptionUserSubRepoStub()
	now := time.Now()
	windowStart := now.Add(-time.Hour)
	repo.seed(&UserSubscription{
		ID:               1,
		UserID:           1,
		PlanID:           1,
		StartsAt:         now.Add(-2 * time.Hour),
		ExpiresAt:        now.Add(time.Hour),
		Status:           SubscriptionStatusActive,
		DailyWindowStart: &windowStart,
		DailyLimitUSD:    billingEligibilityLimitPtr(10),
		DailyUsageUSD:    10,
	})
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

	_, _, err := svc.GetUsableSubscription(context.Background(), 1)

	require.ErrorIs(t, err, ErrSubscriptionNotFound)
}
