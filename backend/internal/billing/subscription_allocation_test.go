package billing

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// subscription_allocation_test.go verifies calendar-day daily quota resets and
// daylight-saving boundary parity between precheck and settlement normalization.

// TestNormalizeSettlementSubscription_AlignsDailyCalendarResetWithPrecheck checks
// a legacy afternoon daily anchor both before and after calendar midnight, plus a
// 23-hour spring-forward DST day. Settlement normalization must match
// AutomaticDailyWindowStartWithCalendar in every case.
func TestNormalizeSettlementSubscription_AlignsDailyCalendarResetWithPrecheck(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	require.NoError(t, err)
	shanghaiCal := timezone.NewCalendar(shanghai)

	limit := 10.0
	startsAt := time.Date(2026, 5, 1, 0, 0, 0, 0, shanghai)
	expiresAt := time.Date(2026, 5, 20, 0, 0, 0, 0, shanghai)
	afternoonAnchor := time.Date(2026, 5, 10, 15, 0, 0, 0, shanghai)

	base := SettlementSubscription{
		ID:               1,
		PlanID:           2,
		StartsAt:         startsAt,
		ExpiresAt:        expiresAt,
		DailyWindowStart: &afternoonAnchor,
		DailyLimitUSD:    &limit,
		DailyUsageUSD:    8.5,
	}
	sub := &UserSubscription{
		StartsAt:         startsAt,
		ExpiresAt:        expiresAt,
		DailyWindowStart: &afternoonAnchor,
		DailyLimitUSD:    &limit,
		DailyUsageUSD:    8.5,
	}

	sameDayLate := time.Date(2026, 5, 10, 23, 50, 0, 0, shanghai)
	require.False(t, sub.NeedsDailyResetAt(sameDayLate, shanghaiCal))
	kept := NormalizeSettlementSubscription(base, sameDayLate)
	require.Equal(t, afternoonAnchor, *kept.DailyWindowStart)
	require.Equal(t, 8.5, kept.DailyUsageUSD)

	nextDayEarly := time.Date(2026, 5, 11, 0, 5, 0, 0, shanghai)
	expectedMidnight, shouldReset := sub.AutomaticDailyWindowStartWithCalendar(nextDayEarly, shanghaiCal)
	require.True(t, shouldReset)
	reset := NormalizeSettlementSubscription(base, nextDayEarly)
	require.Equal(t, expectedMidnight, *reset.DailyWindowStart)
	require.Equal(t, 0.0, reset.DailyUsageUSD)

	ny, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	nyCal := timezone.NewCalendar(ny)
	dstStart := time.Date(2026, 3, 8, 0, 0, 0, 0, ny)
	dstNextEarly := time.Date(2026, 3, 9, 0, 15, 0, 0, ny)
	dstExpires := time.Date(2026, 3, 20, 0, 0, 0, 0, ny)

	dstRow := SettlementSubscription{
		ID:               3,
		PlanID:           4,
		StartsAt:         dstStart,
		ExpiresAt:        dstExpires,
		DailyWindowStart: &dstStart,
		DailyLimitUSD:    &limit,
		DailyUsageUSD:    9.0,
	}
	dstSub := &UserSubscription{
		StartsAt:         dstStart,
		ExpiresAt:        dstExpires,
		DailyWindowStart: &dstStart,
		DailyLimitUSD:    &limit,
		DailyUsageUSD:    9.0,
	}
	dstMidnight, dstShouldReset := dstSub.AutomaticDailyWindowStartWithCalendar(dstNextEarly, nyCal)
	require.True(t, dstShouldReset)
	dstNormalized := NormalizeSettlementSubscription(dstRow, dstNextEarly)
	require.Equal(t, dstMidnight, *dstNormalized.DailyWindowStart)
	require.Equal(t, 0.0, dstNormalized.DailyUsageUSD)
}
