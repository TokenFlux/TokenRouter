package provider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// record_quota_test.go verifies fixed and rolling quota reset boundaries,
// configuration validation, injected clocks, and nil timezone-loader fallbacks.

// TestLastFixedDailyReset_BeforeResetHour evaluates the fixed daily schedule
// before today's reset. The preceding boundary lies on yesterday's date at the
// configured local hour.
func TestLastFixedDailyReset_BeforeResetHour(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 14, 6, 0, 0, 0, tz)
	got := LastFixedDailyReset(9, tz, now)
	want := time.Date(2026, 3, 13, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestLastFixedDailyReset_AtResetHour evaluates the exact daily reset instant
// in UTC. The preceding boundary is that same instant, protecting inclusive
// handling of an already active reset.
func TestLastFixedDailyReset_AtResetHour(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 14, 9, 0, 0, 0, tz)
	got := LastFixedDailyReset(9, tz, now)
	want := time.Date(2026, 3, 14, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestLastFixedDailyReset_AfterResetHour evaluates a time after the configured
// daily reset. The preceding boundary remains on today's date at the selected
// reset hour.
func TestLastFixedDailyReset_AfterResetHour(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 14, 15, 0, 0, 0, tz)
	got := LastFixedDailyReset(9, tz, now)
	want := time.Date(2026, 3, 14, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestLastFixedWeeklyReset_SameDay_AfterHour supplies Monday afternoon with a
// Monday morning reset. The returned preceding boundary is the reset from that
// same calendar day.
func TestLastFixedWeeklyReset_SameDay_AfterHour(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 16, 15, 0, 0, 0, tz)
	got := LastFixedWeeklyReset(1, 9, tz, now)
	want := time.Date(2026, 3, 16, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestLastFixedWeeklyReset_SameDay_BeforeHour supplies Monday before its
// scheduled reset. The preceding boundary is the previous Monday, protecting
// the remaining lifetime of last week's window.
func TestLastFixedWeeklyReset_SameDay_BeforeHour(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 16, 6, 0, 0, 0, tz)
	got := LastFixedWeeklyReset(1, 9, tz, now)
	want := time.Date(2026, 3, 9, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestLastFixedWeeklyReset_DifferentDay supplies Wednesday with a Monday weekly
// schedule. The preceding boundary is the earlier Monday of that week at the
// configured reset hour.
func TestLastFixedWeeklyReset_DifferentDay(t *testing.T) {
	tz := time.UTC
	now := time.Date(2026, 3, 18, 10, 0, 0, 0, tz)
	got := LastFixedWeeklyReset(1, 9, tz, now)
	want := time.Date(2026, 3, 16, 9, 0, 0, 0, tz)
	assert.Equal(t, want, got)
}

// TestIsFixedDailyPeriodExpired_ZeroPeriodStart supplies an absent stored start
// under a fixed daily schedule. The period reports expired so a later usage
// increment can initialize a current window.
func TestIsFixedDailyPeriodExpired_ZeroPeriodStart(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_daily_reset_mode": "fixed",
		"quota_daily_reset_hour": float64(9),
		"quota_reset_timezone":   "UTC",
	}}
	assert.True(t, a.IsFixedDailyPeriodExpired(time.Time{}))
}

// TestIsFixedDailyPeriodExpired_NotExpired places the start at noon on today's
// UTC date under a morning schedule. That chosen start is not before the latest
// daily reset.
func TestIsFixedDailyPeriodExpired_NotExpired(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_daily_reset_mode": "fixed",
		"quota_daily_reset_hour": float64(9),
		"quota_reset_timezone":   "UTC",
	}}
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	assert.False(t, a.IsFixedDailyPeriodExpired(periodStart))
}

// TestIsFixedDailyPeriodExpired_Expired supplies a stored start three elapsed
// days in the past. The fixed daily comparison reports expired under the
// configured UTC reset schedule.
func TestIsFixedDailyPeriodExpired_Expired(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_daily_reset_mode": "fixed",
		"quota_daily_reset_hour": float64(9),
		"quota_reset_timezone":   "UTC",
	}}
	periodStart := time.Now().Add(-72 * time.Hour)
	assert.True(t, a.IsFixedDailyPeriodExpired(periodStart))
}

// TestIsFixedDailyPeriodExpired_InvalidTimezone uses an invalid location name
// and a start three days old. The helper still reports expiration through its
// UTC handling of the failed timezone lookup.
func TestIsFixedDailyPeriodExpired_InvalidTimezone(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_daily_reset_mode": "fixed",
		"quota_daily_reset_hour": float64(9),
		"quota_reset_timezone":   "Invalid/Timezone",
	}}
	periodStart := time.Now().Add(-72 * time.Hour)
	assert.True(t, a.IsFixedDailyPeriodExpired(periodStart))
}

// TestIsFixedWeeklyPeriodExpired_ZeroPeriodStart supplies no stored start under
// a Monday fixed schedule. The period reports expired, allowing the consumption
// writer to initialize the weekly window.
func TestIsFixedWeeklyPeriodExpired_ZeroPeriodStart(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_weekly_reset_mode": "fixed",
		"quota_weekly_reset_day":  float64(1),
		"quota_weekly_reset_hour": float64(9),
		"quota_reset_timezone":    "UTC",
	}}
	assert.True(t, a.IsFixedWeeklyPeriodExpired(time.Time{}))
}

// TestIsFixedWeeklyPeriodExpired_NotExpired places the start at noon on today's
// UTC date. Under the Monday morning schedule, that selected start is not
// before the latest weekly reset.
func TestIsFixedWeeklyPeriodExpired_NotExpired(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_weekly_reset_mode": "fixed",
		"quota_weekly_reset_day":  float64(1),
		"quota_weekly_reset_hour": float64(9),
		"quota_reset_timezone":    "UTC",
	}}
	now := time.Now().UTC()
	periodStart := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, time.UTC)
	assert.False(t, a.IsFixedWeeklyPeriodExpired(periodStart))
}

// TestIsFixedWeeklyPeriodExpired_Expired supplies a stored start ten elapsed
// days in the past. The fixed Monday schedule reports expiration because that
// start predates the current weekly boundary.
func TestIsFixedWeeklyPeriodExpired_Expired(t *testing.T) {
	a := &Record{LoadLocation: time.LoadLocation, Extra: map[string]any{
		"quota_weekly_reset_mode": "fixed",
		"quota_weekly_reset_day":  float64(1),
		"quota_weekly_reset_hour": float64(9),
		"quota_reset_timezone":    "UTC",
	}}
	periodStart := time.Now().Add(-240 * time.Hour)
	assert.True(t, a.IsFixedWeeklyPeriodExpired(periodStart))
}

// TestValidateQuotaResetConfig_NilExtra submits no Extra configuration to reset
// validation. The result succeeds, allowing absent reset settings to keep the
// existing default interpretation.
func TestValidateQuotaResetConfig_NilExtra(t *testing.T) {
	assert.NoError(t, ValidateQuotaResetConfig(nil, time.LoadLocation))
}

// TestValidateQuotaResetConfig_EmptyExtra submits an empty object to reset
// validation. The result succeeds because no reset modes, clock values, or
// timezone names were supplied for checking.
func TestValidateQuotaResetConfig_EmptyExtra(t *testing.T) {
	assert.NoError(t, ValidateQuotaResetConfig(map[string]any{}, time.LoadLocation))
}

// TestValidateQuotaResetConfig_ValidFixed combines valid daily and weekly clock
// settings with a Shanghai timezone. Validation accepts the fixed modes, local
// hour ranges, weekday value, and resolved location.
func TestValidateQuotaResetConfig_ValidFixed(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_mode":  "fixed",
		"quota_daily_reset_hour":  float64(9),
		"quota_weekly_reset_mode": "fixed",
		"quota_weekly_reset_day":  float64(1),
		"quota_weekly_reset_hour": float64(0),
		"quota_reset_timezone":    "Asia/Shanghai",
	}
	assert.NoError(t, ValidateQuotaResetConfig(extra, time.LoadLocation))
}

// TestValidateQuotaResetConfig_ValidRolling supplies rolling labels for the
// daily and weekly dimensions. Validation succeeds, keeping duration-based
// reset configuration available alongside the fixed calendar modes.
func TestValidateQuotaResetConfig_ValidRolling(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_mode":  "rolling",
		"quota_weekly_reset_mode": "rolling",
	}
	assert.NoError(t, ValidateQuotaResetConfig(extra, time.LoadLocation))
}

// TestValidateQuotaResetConfig_InvalidTimezone supplies a location name that
// the loader cannot resolve. Validation returns an error naming
// quota_reset_timezone, so management can identify the rejected setting.
func TestValidateQuotaResetConfig_InvalidTimezone(t *testing.T) {
	extra := map[string]any{
		"quota_reset_timezone": "Not/A/Timezone",
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_reset_timezone")
}

// TestValidateQuotaResetConfig_InvalidDailyMode submits an unsupported daily
// mode string. Validation returns an error naming quota_daily_reset_mode,
// protecting the accepted rolling and fixed configuration labels.
func TestValidateQuotaResetConfig_InvalidDailyMode(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_mode": "invalid",
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_daily_reset_mode")
}

// TestValidateQuotaResetConfig_InvalidDailyHour_TooHigh submits hour
// twenty-four for a daily reset. Validation rejects it and identifies the
// daily-hour key because local clock hours end at twenty-three.
func TestValidateQuotaResetConfig_InvalidDailyHour_TooHigh(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_hour": float64(24),
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_daily_reset_hour")
}

// TestValidateQuotaResetConfig_InvalidDailyHour_Negative submits a negative
// daily reset hour. Validation returns an error identifying the daily-hour key
// because the accepted local range starts at zero.
func TestValidateQuotaResetConfig_InvalidDailyHour_Negative(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_hour": float64(-1),
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_daily_reset_hour")
}

// TestValidateQuotaResetConfig_InvalidWeeklyMode submits an unsupported weekly
// mode string. Validation rejects it with the weekly-mode key, protecting the
// accepted fixed and rolling labels.
func TestValidateQuotaResetConfig_InvalidWeeklyMode(t *testing.T) {
	extra := map[string]any{
		"quota_weekly_reset_mode": "unknown",
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_weekly_reset_mode")
}

// TestValidateQuotaResetConfig_InvalidWeeklyDay_TooHigh submits weekday seven
// to the Sunday-zero configuration. Validation rejects the value and identifies
// the weekly-day key because the accepted week ends at six.
func TestValidateQuotaResetConfig_InvalidWeeklyDay_TooHigh(t *testing.T) {
	extra := map[string]any{
		"quota_weekly_reset_day": float64(7),
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_weekly_reset_day")
}

// TestValidateQuotaResetConfig_InvalidWeeklyDay_Negative supplies a negative
// weekday index for a fixed weekly schedule. Validation rejects it and names
// the weekly-day key, preserving the Sunday-zero lower bound.
func TestValidateQuotaResetConfig_InvalidWeeklyDay_Negative(t *testing.T) {
	extra := map[string]any{
		"quota_weekly_reset_day": float64(-1),
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_weekly_reset_day")
}

// TestValidateQuotaResetConfig_InvalidWeeklyHour supplies hour twenty-five for
// a weekly reset. Validation rejects it and identifies quota_weekly_reset_hour
// because the accepted local clock range ends at twenty-three.
func TestValidateQuotaResetConfig_InvalidWeeklyHour(t *testing.T) {
	extra := map[string]any{
		"quota_weekly_reset_hour": float64(25),
	}
	err := ValidateQuotaResetConfig(extra, time.LoadLocation)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "quota_weekly_reset_hour")
}

// TestValidateQuotaResetConfig_BoundaryValues checks both ends of the accepted
// clock and weekday ranges. Midnight, hour twenty-three, Sunday zero, and
// Saturday six all pass the configuration validator.
func TestValidateQuotaResetConfig_BoundaryValues(t *testing.T) {
	extra := map[string]any{
		"quota_daily_reset_hour":  float64(23),
		"quota_weekly_reset_day":  float64(0),
		"quota_weekly_reset_hour": float64(0),
		"quota_reset_timezone":    "UTC",
	}
	assert.NoError(t, ValidateQuotaResetConfig(extra, time.LoadLocation))

	extra2 := map[string]any{
		"quota_daily_reset_hour":  float64(0),
		"quota_weekly_reset_day":  float64(6),
		"quota_weekly_reset_hour": float64(23),
	}
	assert.NoError(t, ValidateQuotaResetConfig(extra2, time.LoadLocation))
}

// TestMonthlyQuotaAdmission exercises monthly exhaustion alongside an expired daily
// allowance. Expired and missing monthly starts release the monthly restriction,
// while an active exhausted total allowance still rejects the same provider.
func TestMonthlyQuotaAdmission(t *testing.T) {
	now := time.Now().UTC()
	r := &Record{LoadLocation: time.LoadLocation, Now: func() time.Time { return now }, Extra: map[string]any{
		"quota_monthly_limit": 100.0, "quota_monthly_used": 100.0,
		"quota_monthly_start": now.Add(-24 * time.Hour).Format(time.RFC3339),
		"quota_daily_limit":   1.0, "quota_daily_used": 1.0,
		"quota_daily_start": now.Add(-48 * time.Hour).Format(time.RFC3339),
	}}
	require.True(t, r.HasAnyQuotaLimit())
	require.True(t, r.IsQuotaExceeded())
	r.Extra["quota_monthly_start"] = now.Add(-31 * 24 * time.Hour).Format(time.RFC3339)
	require.False(t, r.IsQuotaExceeded())
	delete(r.Extra, "quota_monthly_start")
	require.False(t, r.IsQuotaExceeded())
	r.Extra["quota_limit"], r.Extra["quota_used"] = 10.0, 10.0
	require.True(t, r.IsQuotaExceeded())
}

// TestMonthlyQuotaConfigurationRejectsInvalidDates exercises monthly clock
// validation.
// Fractions and out-of-range dates fail before persistence, while valid month-end
// schedules remain accepted for billing's short-month clamping rule.
func TestMonthlyQuotaConfigurationRejectsInvalidDates(t *testing.T) {
	for _, day := range []float64{0, -1, 32, 1.5} {
		require.Error(t, ValidateQuotaResetConfig(map[string]any{"quota_monthly_reset_day": day}, time.LoadLocation))
	}
	for _, hour := range []float64{-1, 24, 1.5} {
		require.Error(t, ValidateQuotaResetConfig(map[string]any{"quota_monthly_reset_hour": hour}, time.LoadLocation))
	}
	require.Error(t, ValidateQuotaResetConfig(map[string]any{"quota_monthly_reset_mode": "invalid"}, time.LoadLocation))
	require.NoError(t, ValidateQuotaResetConfig(map[string]any{"quota_monthly_reset_day": 31, "quota_monthly_reset_hour": 23}, time.LoadLocation))
}

// TestMonthlyFixedQuotaAdmission checks equality and the monthly calendar rollover.
// A month-end schedule exhausts at its recorded current boundary and becomes
// available after the next month's clamped reset, independently of rolling time.
func TestMonthlyFixedQuotaAdmission(t *testing.T) {
	now := time.Date(2026, 2, 28, 9, 0, 0, 0, time.UTC)
	r := &Record{LoadLocation: time.LoadLocation, Now: func() time.Time { return now }, Extra: map[string]any{
		"quota_monthly_limit": 100.0, "quota_monthly_used": 100.0,
		"quota_monthly_reset_mode": "fixed", "quota_monthly_reset_day": 31,
		"quota_monthly_reset_hour": 9, "quota_reset_timezone": "UTC",
		"quota_monthly_start": now.Format(time.RFC3339),
	}}
	require.True(t, r.IsQuotaExceeded())
	now = time.Date(2026, 3, 31, 9, 0, 0, 0, time.UTC)
	require.False(t, r.IsQuotaExceeded())
}

// TestQuotaPeriodExpirationUsesInjectedClockAndNilLocationLoader exercises
// fixed and rolling quota checks on a record with nil LoadLocation and a
// future simulated clock. Fixed windows resolve timezones without panicking,
// and rolling windows expire relative to Record.Now instead of wall-clock time.
func TestQuotaPeriodExpirationUsesInjectedClockAndNilLocationLoader(t *testing.T) {
	base := time.Date(2030, 6, 10, 12, 0, 0, 0, time.UTC)
	now := base
	r := &Record{
		Now: func() time.Time { return now },
		Extra: map[string]any{
			"quota_daily_limit":    10.0,
			"quota_daily_used":     10.0,
			"quota_daily_start":    base.Add(-23 * time.Hour).Format(time.RFC3339),
			"quota_reset_timezone": "Asia/Shanghai",
		},
	}
	require.False(t, r.IsDailyQuotaPeriodExpired())
	require.True(t, r.IsQuotaExceeded())

	now = base.Add(2 * time.Hour)
	require.True(t, r.IsDailyQuotaPeriodExpired())
	require.False(t, r.IsQuotaExceeded())

	r.Extra["quota_daily_reset_mode"] = "fixed"
	r.Extra["quota_daily_reset_hour"] = 9
	r.Extra["quota_daily_start"] = base.Format(time.RFC3339)
	now = base.Add(24 * time.Hour)
	require.True(t, r.IsDailyQuotaPeriodExpired())
}
