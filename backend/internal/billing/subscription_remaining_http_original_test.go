//go:build unit

package billing_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

func subscriptionLimitPtr(v float64) *float64 {
	return &v
}

func TestCalculateSubscriptionRemaining_IgnoresDisabledZeroLimit(t *testing.T) {
	sub := &billing.UserSubscription{
		DailyLimitUSD:   subscriptionLimitPtr(10),
		WeeklyLimitUSD:  subscriptionLimitPtr(0),
		MonthlyLimitUSD: subscriptionLimitPtr(100),
		DailyUsageUSD:   3,
		WeeklyUsageUSD:  99,
		MonthlyUsageUSD: 20,
	}

	require.Equal(t, 7.0, billing.SubscriptionRemainingForDisplay(sub))
}

func TestCalculateSubscriptionRemaining_NoPositiveLimitsReturnsUnlimited(t *testing.T) {
	sub := &billing.UserSubscription{
		DailyLimitUSD:   subscriptionLimitPtr(0),
		MonthlyLimitUSD: subscriptionLimitPtr(0),
	}

	require.Equal(t, -1.0, billing.SubscriptionRemainingForDisplay(sub))
}
