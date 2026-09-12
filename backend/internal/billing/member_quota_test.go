package billing

import (
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/stretchr/testify/require"
)

// TestMemberQuotaCalendarProjection 验证 DST 日界、未过期窗口和读取投影隔离，不改变原消费计数。
func TestMemberQuotaCalendarProjection(t *testing.T) {
	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	calendar := timezone.NewCalendar(location)
	now := time.Date(2026, time.November, 1, 7, 30, 0, 0, time.UTC)
	stale := time.Date(2026, time.October, 31, 0, 0, 0, 0, location)
	week := calendar.StartOfWeek(now)
	source := MemberQuotaSnapshot{DailyLimitUSD: 5, WeeklyLimitUSD: 9, MonthlyLimitUSD: 30, DailyUsageUSD: 5, WeeklyUsageUSD: 7, MonthlyUsageUSD: 29, DailyWindowStart: &stale, WeeklyWindowStart: &week, MonthlyWindowStart: &stale}
	got := NormalizeMemberQuotaWindows(source, now, calendar)
	require.Zero(t, got.DailyUsageUSD)
	require.Equal(t, float64(7), got.WeeklyUsageUSD)
	require.Zero(t, got.MonthlyUsageUSD)
	require.Equal(t, calendar.StartOfDay(now), *got.DailyWindowStart)
	require.Same(t, source.WeeklyWindowStart, got.WeeklyWindowStart)
	require.Equal(t, float64(5), source.DailyUsageUSD)
	require.Equal(t, stale, *source.DailyWindowStart)
	require.Equal(t, 25*time.Hour, calendar.StartOfDay(now).AddDate(0, 0, 1).Sub(calendar.StartOfDay(now)))
	require.NoError(t, CheckMemberQuotaSnapshot(got))
}

// TestMemberQuotaThresholdOrder 保留精确边界和日/周/月拒绝顺序，校验本身不重置窗口。
func TestMemberQuotaThresholdOrder(t *testing.T) {
	snapshot := MemberQuotaSnapshot{DailyLimitUSD: 1, WeeklyLimitUSD: 2, MonthlyLimitUSD: 3, DailyUsageUSD: 1, WeeklyUsageUSD: 2, MonthlyUsageUSD: 3}
	require.ErrorIs(t, CheckMemberQuotaSnapshot(snapshot), ErrTeamMemberDailyExceeded)
	snapshot.DailyLimitUSD = 0
	require.ErrorIs(t, CheckMemberQuotaSnapshot(snapshot), ErrTeamMemberWeeklyExceeded)
	snapshot.WeeklyLimitUSD = 0
	require.ErrorIs(t, CheckMemberQuotaSnapshot(snapshot), ErrTeamMemberMonthlyExceeded)
	snapshot.MonthlyLimitUSD = 0
	require.NoError(t, CheckMemberQuotaSnapshot(snapshot))
}
