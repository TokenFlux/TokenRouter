//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func quotaLimitPtr(v float64) *float64 {
	return &v
}

func TestUserSubscriptionAvailableQuotaUSD_IgnoresDisabledZeroLimit(t *testing.T) {
	sub := &UserSubscription{
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
	sub := &UserSubscription{
		DailyLimitUSD:   quotaLimitPtr(0),
		MonthlyLimitUSD: quotaLimitPtr(0),
	}

	require.Nil(t, sub.RemainingDailyUSD())
	require.Nil(t, sub.RemainingWeeklyUSD())
	require.Nil(t, sub.RemainingMonthlyUSD())
	require.Equal(t, 0.0, sub.AvailableQuotaUSD())
}
