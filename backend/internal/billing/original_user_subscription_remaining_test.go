//go:build unit

package billing_test

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

func quotaLimitPtr(v float64) *float64 {
	return &v
}

func TestUserSubscriptionAvailableQuotaUSD_IgnoresDisabledZeroLimit(t *testing.T) {
	sub := &billing.UserSubscription{
		DailyLimitUSD:   quotaLimitPtr(10),
		WeeklyLimitUSD:  quotaLimitPtr(0),
		MonthlyLimitUSD: quotaLimitPtr(100),
		DailyUsageUSD:   3,
		WeeklyUsageUSD:  99,
		MonthlyUsageUSD: 20,
	}

	require.NotNil(t, sub.RemainingDailyUSD())
	require.Nil(t, sub.RemainingWeeklyUSD())
	require.NotNil(t, sub.RemainingMonthlyUSD())
	require.Equal(t, 7.0, sub.AvailableQuotaUSD())
}

func TestUserSubscriptionAvailableQuotaUSD_NoPositiveLimitsReturnsZero(t *testing.T) {
	sub := &billing.UserSubscription{
		DailyLimitUSD:   quotaLimitPtr(0),
		MonthlyLimitUSD: quotaLimitPtr(0),
	}

	require.Nil(t, sub.RemainingDailyUSD())
	require.Nil(t, sub.RemainingWeeklyUSD())
	require.Nil(t, sub.RemainingMonthlyUSD())
	require.Equal(t, 0.0, sub.AvailableQuotaUSD())
}

func TestUserSubscriptionEffectiveStatus_DeletedAtReturnsRevoked(t *testing.T) {
	now := time.Now().UTC()
	deletedAt := now.Add(-time.Minute)
	sub := &billing.UserSubscription{
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
		Status:    billing.SubscriptionStatusActive,
		DeletedAt: &deletedAt,
	}

	require.Equal(t, billing.SubscriptionStatusRevoked, sub.EffectiveStatus(now))
	require.False(t, sub.IsActive())
}
