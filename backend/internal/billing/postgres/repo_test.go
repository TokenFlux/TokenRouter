package postgres

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestNormalizeUsageBillingWindow_ExpiryTailKeepsMonthlyUsage(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 4, 30, 0, 0, 0, 0, time.UTC)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{Time: windowStart, Valid: true},
		sql.NullFloat64{Float64: 100, Valid: true},
		90,
		startOfDay(now),
		30*24*time.Hour,
		now,
		startsAt,
		expiresAt,
		false,
	)

	require.NotNil(t, start)
	require.Equal(t, windowStart, *start)
	require.Equal(t, 90.0, used, "到期尾段不足完整月窗口时不应清零 monthly usage")
}

func TestNormalizeUsageBillingWindow_ExpiryTailMissingMonthlyWindowStaysNil(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{},
		sql.NullFloat64{Float64: 100, Valid: true},
		90,
		startOfDay(now),
		30*24*time.Hour,
		now,
		startsAt,
		expiresAt,
		false,
	)

	require.Nil(t, start, "到期尾段不足完整月窗口时不应补写 monthly window_start")
	require.Equal(t, 90.0, used)
}

func TestNormalizeUsageBillingWindow_DailyCardDoesNotResetAfterMidnight(t *testing.T) {
	now := time.Date(2026, 5, 31, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 5, 30, 12, 0, 0, 0, time.UTC)
	expiresAt := startsAt.Add(24 * time.Hour)
	windowStart := time.Date(2026, 5, 30, 0, 0, 0, 0, time.UTC)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{Time: windowStart, Valid: true},
		sql.NullFloat64{Float64: 10, Valid: true},
		10,
		startOfDay(now),
		24*time.Hour,
		now,
		startsAt,
		expiresAt,
		true,
	)

	require.NotNil(t, start)
	require.Equal(t, windowStart, *start)
	require.Equal(t, 10.0, used, "1 日卡跨 0 点后不应刷新第二份 daily quota")
}

func TestNormalizeUsageBillingWindow_MultiDayDailyStillResetsWhenFullWindowFits(t *testing.T) {
	now := time.Date(2026, 5, 29, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 28, 0, 0, 0, 0, time.UTC)
	resetStart := startOfDay(now)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{Time: windowStart, Valid: true},
		sql.NullFloat64{Float64: 10, Valid: true},
		10,
		resetStart,
		24*time.Hour,
		now,
		startsAt,
		expiresAt,
		false,
	)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "多日订阅仍应在可覆盖完整日窗口时重置 daily usage")
}

func TestNormalizeUsageBillingWindow_DailyTailResetsWithFiniteOuterLimit(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 29, 0, 0, 0, 0, time.UTC)
	resetStart := startOfDay(now)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{Time: windowStart, Valid: true},
		sql.NullFloat64{Float64: 10, Valid: true},
		10,
		resetStart,
		24*time.Hour,
		now,
		startsAt,
		expiresAt,
		true,
	)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "有限周或月额度存在时，订阅尾段仍应刷新日额度")
}

func TestNormalizeUsageBillingWindow_WeeklyTailResetsWithFiniteMonthlyLimit(t *testing.T) {
	now := time.Date(2026, 5, 30, 0, 5, 0, 0, time.UTC)
	startsAt := time.Date(2026, 4, 30, 8, 0, 0, 0, time.UTC)
	expiresAt := time.Date(2026, 5, 30, 8, 0, 0, 0, time.UTC)
	windowStart := time.Date(2026, 5, 23, 0, 0, 0, 0, time.UTC)
	resetStart := startOfDay(now)

	start, used := normalizeUsageBillingWindow(
		sql.NullTime{Time: windowStart, Valid: true},
		sql.NullFloat64{Float64: 50, Valid: true},
		50,
		resetStart,
		7*24*time.Hour,
		now,
		startsAt,
		expiresAt,
		true,
	)

	require.NotNil(t, start)
	require.Equal(t, resetStart, *start)
	require.Equal(t, 0.0, used, "有限月额度存在时，订阅尾段仍应刷新周额度")
}

func TestHasFiniteUsageBillingLimit_RequiresPositiveConfiguredLimit(t *testing.T) {
	tests := []struct {
		name  string
		limit sql.NullFloat64
		want  bool
	}{
		{name: "未配置", limit: sql.NullFloat64{}, want: false},
		{name: "零表示无限", limit: sql.NullFloat64{Float64: 0, Valid: true}, want: false},
		{name: "负数表示无限", limit: sql.NullFloat64{Float64: -1, Valid: true}, want: false},
		{name: "正数有限额度", limit: sql.NullFloat64{Float64: 100, Valid: true}, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, hasFiniteUsageBillingLimit(tt.limit))
		})
	}
}
