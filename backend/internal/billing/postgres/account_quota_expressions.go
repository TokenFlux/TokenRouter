// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package postgres

// NowUTC is a SQL expression to generate a UTC RFC3339 timestamp string.
const NowUTC = `to_char(NOW() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS.US"Z"')`

// DailyExpiredExpr is a SQL expression that evaluates to TRUE when daily quota period has expired.
// Supports both rolling (24h from start) and fixed (pre-computed reset_at) modes.
const DailyExpiredExpr = `(
	CASE WHEN COALESCE(extra->>'quota_daily_reset_mode', 'rolling') = 'fixed'
	THEN NOW() >= COALESCE((extra->>'quota_daily_reset_at')::timestamptz, '1970-01-01'::timestamptz)
	ELSE COALESCE((extra->>'quota_daily_start')::timestamptz, '1970-01-01'::timestamptz)
		+ '24 hours'::interval <= NOW()
	END
)`

// WeeklyExpiredExpr is a SQL expression that evaluates to TRUE when weekly quota period has expired.
const WeeklyExpiredExpr = `(
	CASE WHEN COALESCE(extra->>'quota_weekly_reset_mode', 'rolling') = 'fixed'
	THEN NOW() >= COALESCE((extra->>'quota_weekly_reset_at')::timestamptz, '1970-01-01'::timestamptz)
	ELSE COALESCE((extra->>'quota_weekly_start')::timestamptz, '1970-01-01'::timestamptz)
		+ '168 hours'::interval <= NOW()
	END
)`

// NextDailyResetAtExpr is a SQL expression to compute the next daily reset_at when a reset occurs.
// For fixed mode: computes the next future reset time based on NOW(), timezone, and configured hour.
// This correctly handles long-inactive accounts by jumping directly to the next valid reset point.
const NextDailyResetAtExpr = `(
	CASE WHEN COALESCE(extra->>'quota_daily_reset_mode', 'rolling') = 'fixed'
	THEN to_char((
		-- Compute today's reset point in the configured timezone, then pick next future one
		CASE WHEN NOW() >= (
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_daily_reset_hour')::int, 0) || ' hours')::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		-- NOW() is at or past today's reset point → next reset is tomorrow
		THEN (
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_daily_reset_hour')::int, 0) || ' hours')::interval
			+ '1 day'::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		-- NOW() is before today's reset point → next reset is today
		ELSE (
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_daily_reset_hour')::int, 0) || ' hours')::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		END
	) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	ELSE NULL END
)`

// NextWeeklyResetAtExpr is a SQL expression to compute the next weekly reset_at when a reset occurs.
// For fixed mode: computes the next future reset time based on NOW(), timezone, configured day and hour.
// This correctly handles long-inactive accounts by jumping directly to the next valid reset point.
const NextWeeklyResetAtExpr = `(
	CASE WHEN COALESCE(extra->>'quota_weekly_reset_mode', 'rolling') = 'fixed'
	THEN to_char((
		-- Compute this week's reset point in the configured timezone
		-- Step 1: get today's date at reset hour in configured tz
		-- Step 2: compute days forward to target weekday
		-- Step 3: if same day but past reset hour, advance 7 days
		CASE
		WHEN (
			-- days_forward = (target_day - current_day + 7) % 7
			(COALESCE((extra->>'quota_weekly_reset_day')::int, 1)
			 - EXTRACT(DOW FROM NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))::int
			 + 7) % 7
		) = 0 AND NOW() >= (
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_weekly_reset_hour')::int, 0) || ' hours')::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		-- Same weekday and past reset hour → next week
		THEN (
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_weekly_reset_hour')::int, 0) || ' hours')::interval
			+ '7 days'::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		ELSE (
			-- Advance to target weekday this week (or next if days_forward > 0)
			date_trunc('day', NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))
			+ (COALESCE((extra->>'quota_weekly_reset_hour')::int, 0) || ' hours')::interval
			+ ((
				(COALESCE((extra->>'quota_weekly_reset_day')::int, 1)
				 - EXTRACT(DOW FROM NOW() AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC'))::int
				 + 7) % 7
			) || ' days')::interval
		) AT TIME ZONE COALESCE(extra->>'quota_reset_timezone', 'UTC')
		END
	) AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
	ELSE NULL END
)`
