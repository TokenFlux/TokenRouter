package billing

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// TestSubscriptionCalendarBoundaries 保留日历日的 DST 边界，不把日额度改为固定 24 小时。
func TestSubscriptionCalendarBoundaries(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	calendar := timezone.NewCalendar(location)
	for _, item := range []struct {
		name  string
		day   time.Time
		hours time.Duration
	}{
		{"spring", time.Date(2026, 3, 8, 0, 0, 0, 0, location), 23 * time.Hour},
		{"autumn", time.Date(2026, 11, 1, 0, 0, 0, 0, location), 25 * time.Hour},
	} {
		t.Run(item.name, func(t *testing.T) {
			limit := 10.0
			previous := item.day.AddDate(0, 0, -1)
			sub := &UserSubscription{StartsAt: previous, ExpiresAt: item.day.AddDate(0, 0, 4), DailyWindowStart: &previous, DailyLimitUSD: &limit}
			start, reset := sub.AutomaticDailyWindowStartWithCalendar(item.day.Add(time.Hour), calendar)
			require.True(t, reset)
			require.True(t, start.Equal(item.day))
			sub.DailyWindowStart = &item.day
			next := sub.DailyResetTime(calendar)
			require.NotNil(t, next)
			require.Equal(t, item.hours, next.Sub(item.day))
			require.False(t, sub.NeedsDailyResetAt(next.Add(-time.Nanosecond), calendar))
			require.True(t, sub.NeedsDailyResetAt(*next, calendar))
		})
	}
}
