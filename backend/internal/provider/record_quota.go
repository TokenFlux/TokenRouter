package provider

import (
	"errors"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// record_quota.go reads provider quota limits, usage counters, notification
// thresholds, and fixed or rolling reset windows.
// Total, daily, weekly, and monthly limits apply independently during admission
// checks. Fixed windows evaluate calendar boundaries in the configured timezone
// using the record's injected location loader and clock, defaulting to standard
// time functions when those hooks are nil so zero-initialized and decoded
// records remain safe to evaluate.
//
// Rolling windows measure elapsed durations against the record clock rather
// than wall-clock time.Since, keeping simulated and cached checks consistent
// with fixed resets. Expired windows ignore their stored usage during admission
// while leaving counter rollover to the billing writer.

const (
	// Notification labels construct dimension-specific Extra keys and supply the
	// missing threshold interpretation used by daily, weekly, monthly, and total quota
	// readers.
	thresholdTypeFixed = "fixed"   // fixed quota notification threshold
	quotaDimDaily      = "daily"   // daily quota notification dimension
	quotaDimWeekly     = "weekly"  // weekly quota notification dimension
	quotaDimMonthly    = "monthly" // monthly quota notification dimension
	quotaDimTotal      = "total"   // total quota notification dimension
)

// GetQuotaLimit reads the total provider quota limit in USD. Nonpositive limits
// leave the total dimension inactive in IsQuotaExceeded, while periodic limits
// are evaluated independently against their own usage.
func (r *Record) GetQuotaLimit() float64 {
	return r.GetExtraFloat64("quota_limit")
}

// GetQuotaUsed reads the stored total provider consumption in USD.
func (r *Record) GetQuotaUsed() float64 {
	return r.GetExtraFloat64("quota_used")
}

// GetQuotaDailyLimit reads the provider's daily quota limit in USD. Nonpositive
// limits leave that dimension inactive; a positive limit is compared with usage
// only while the daily period remains current.
func (r *Record) GetQuotaDailyLimit() float64 {
	return r.GetExtraFloat64("quota_daily_limit")
}

// GetQuotaDailyUsed reads stored daily provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaDailyUsed() float64 {
	return r.GetExtraFloat64("quota_daily_used")
}

// GetQuotaWeeklyLimit reads the provider's weekly quota limit in USD.
// Nonpositive limits leave that dimension inactive; a positive limit applies to
// the stored consumption only while its weekly period remains current.
func (r *Record) GetQuotaWeeklyLimit() float64 {
	return r.GetExtraFloat64("quota_weekly_limit")
}

// GetQuotaMonthlyLimit reads the provider's monthly quota limit in USD.
// Nonpositive limits leave that dimension inactive; a positive limit applies to
// the stored consumption only while its monthly period remains current.
func (r *Record) GetQuotaMonthlyLimit() float64 {
	return r.GetExtraFloat64("quota_monthly_limit")
}

// GetQuotaWeeklyUsed reads stored weekly provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaWeeklyUsed() float64 {
	return r.GetExtraFloat64("quota_weekly_used")
}

// GetQuotaMonthlyUsed reads stored monthly provider consumption in USD before
// expiration handling.
func (r *Record) GetQuotaMonthlyUsed() float64 {
	return r.GetExtraFloat64("quota_monthly_used")
}

// GetQuotaDailyResetMode returns fixed for the exact saved reset-mode label.
// Every other value selects rolling, including missing configuration, allowing
// stored records to retain the default duration-based daily window.
func (r *Record) GetQuotaDailyResetMode() string {
	if m := r.GetExtraString("quota_daily_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaDailyResetHour reads the configured local daily reset hour as an
// integer. Missing values return zero; this accessor leaves range checking to
// validation and normalization to the billing wrapper where applicable.
func (r *Record) GetQuotaDailyResetHour() int {
	return r.GetExtraInt("quota_daily_reset_hour")
}

// GetQuotaWeeklyResetMode returns fixed for the exact saved reset-mode label.
// Missing, invalid, or differently cased values select rolling, allowing weekly
// consumers to use their duration-based default calculation.
func (r *Record) GetQuotaWeeklyResetMode() string {
	if m := r.GetExtraString("quota_weekly_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaMonthlyResetMode returns fixed for the exact saved reset-mode label.
// Missing, invalid, or differently cased values select rolling, allowing monthly
// consumers to use their duration-based default calculation.
func (r *Record) GetQuotaMonthlyResetMode() string {
	if m := r.GetExtraString("quota_monthly_reset_mode"); m == "fixed" {
		return "fixed"
	}
	return "rolling"
}

// GetQuotaWeeklyResetDay reads the configured Sunday-zero weekday for fixed
// weekly resets. A missing key selects Monday, while a present unusable value
// can parse to zero and is left to validation.
func (r *Record) GetQuotaWeeklyResetDay() int {
	if r.Extra == nil {
		return 1
	}
	if _, ok := r.Extra["quota_weekly_reset_day"]; !ok {
		return 1
	}
	return r.GetExtraInt("quota_weekly_reset_day")
}

// GetQuotaMonthlyResetDay reads the configured day of month for fixed
// monthly resets. A missing key selects the first day, while a present unusable
// value can parse to zero and is left to validation.
func (r *Record) GetQuotaMonthlyResetDay() int {
	if r.Extra == nil {
		return 1
	}
	if _, ok := r.Extra["quota_monthly_reset_day"]; !ok {
		return 1
	}
	return r.GetExtraInt("quota_monthly_reset_day")
}

// GetQuotaWeeklyResetHour reads the configured local weekly reset hour as an
// integer. Missing values return zero; range checks belong to configuration
// validation or the billing wrapper before calendar calculations.
func (r *Record) GetQuotaWeeklyResetHour() int {
	return r.GetExtraInt("quota_weekly_reset_hour")
}

// GetQuotaMonthlyResetHour reads the configured local monthly reset hour as an
// integer. Missing values return zero; range checks belong to configuration
// validation or the billing wrapper before calendar calculations.
func (r *Record) GetQuotaMonthlyResetHour() int {
	return r.GetExtraInt("quota_monthly_reset_hour")
}

// GetQuotaResetTimezone returns the saved nonempty timezone text, defaulting to
// UTC. It does not validate or trim that string; the supplied location loader
// and each reset consumer decide how invalid names are handled.
func (r *Record) GetQuotaResetTimezone() string {
	if tz := r.GetExtraString("quota_reset_timezone"); tz != "" {
		return tz
	}
	return "UTC"
}

// QuotaNotifyConfig reads the enabled flag, threshold, and threshold type for a
// daily, weekly, monthly, or total dimension. Missing values select false, zero, and
// fixed; supplied nonempty threshold-type text is returned for the notification
// consumer to interpret.
func (r *Record) QuotaNotifyConfig(dim string) (enabled bool, threshold float64, thresholdType string) {
	enabled = r.GetExtraBool("quota_notify_" + dim + "_enabled")
	threshold = r.GetExtraFloat64("quota_notify_" + dim + "_threshold")
	thresholdType = r.GetExtraStringDefault("quota_notify_"+dim+"_threshold_type", thresholdTypeFixed)
	return
}

// GetQuotaNotifyDailyEnabled reads the daily quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyDailyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimDaily)
	return e
}

// GetQuotaNotifyDailyThreshold reads the configured daily quota notification
// threshold value.
func (r *Record) GetQuotaNotifyDailyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimDaily)
	return t
}

// GetQuotaNotifyDailyThresholdType returns the daily threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed, while supplied
// nonempty values remain for the notification consumer to validate or
// interpret.
func (r *Record) GetQuotaNotifyDailyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimDaily)
	return tt
}

// GetQuotaNotifyWeeklyEnabled reads the weekly quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyWeeklyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimWeekly)
	return e
}

// GetQuotaNotifyMonthlyEnabled reads the monthly quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyMonthlyEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimMonthly)
	return e
}

// GetQuotaNotifyWeeklyThreshold reads the configured weekly quota notification
// threshold value.
func (r *Record) GetQuotaNotifyWeeklyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimWeekly)
	return t
}

// GetQuotaNotifyMonthlyThreshold reads the configured monthly quota notification
// threshold value.
func (r *Record) GetQuotaNotifyMonthlyThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimMonthly)
	return t
}

// GetQuotaNotifyWeeklyThresholdType returns the weekly threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; other nonempty
// saved strings are returned for the notification consumer's own threshold
// handling.
func (r *Record) GetQuotaNotifyWeeklyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimWeekly)
	return tt
}

// GetQuotaNotifyMonthlyThresholdType returns the monthly threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; other nonempty
// saved strings are returned for the notification consumer's own threshold
// handling.
func (r *Record) GetQuotaNotifyMonthlyThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimMonthly)
	return tt
}

// GetQuotaNotifyTotalEnabled reads the total quota notification switch from
// Extra.
func (r *Record) GetQuotaNotifyTotalEnabled() bool {
	e, _, _ := r.QuotaNotifyConfig(quotaDimTotal)
	return e
}

// GetQuotaNotifyTotalThreshold reads the configured total quota notification
// threshold value.
func (r *Record) GetQuotaNotifyTotalThreshold() float64 {
	_, t, _ := r.QuotaNotifyConfig(quotaDimTotal)
	return t
}

// GetQuotaNotifyTotalThresholdType returns the total threshold interpretation
// from QuotaNotifyConfig. Missing or empty text selects fixed; the notification
// consumer interprets other supplied nonempty strings when applying the
// threshold.
func (r *Record) GetQuotaNotifyTotalThresholdType() string {
	_, _, tt := r.QuotaNotifyConfig(quotaDimTotal)
	return tt
}

// LastFixedDailyReset delegates the preceding local daily reset date to
// billing. Supply a nonnil timezone and the intended clock hour; equality with
// today's reset selects today and earlier times select the preceding calendar
// day.
func LastFixedDailyReset(hour int, tz *time.Location, now time.Time) time.Time {
	return billing.LastFixedDailyReset(hour, tz, now)
}

// LastFixedWeeklyReset delegates the preceding local weekly reset date to
// billing. Supply a nonnil timezone, Sunday-zero weekday, and intended clock
// hour; the selected boundary moves back a week before its matching-day reset.
func LastFixedWeeklyReset(day, hour int, tz *time.Location, now time.Time) time.Time {
	return billing.LastFixedWeeklyReset(day, hour, tz, now)
}

// IsFixedDailyPeriodExpired compares a stored start with the most recent
// configured local daily reset. A zero start is expired, an invalid timezone
// uses UTC, and equality with the reset remains current. Nil LoadLocation
// defaults to time.LoadLocation.
func (r *Record) IsFixedDailyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := r.loadLocation(r.GetQuotaResetTimezone())
	if err != nil {
		// A failed saved location lookup cannot define the calendar reset. Use
		// UTC to compare the stored start with a usable reset date for admission.
		tz = time.UTC
	}
	lastReset := LastFixedDailyReset(r.GetQuotaDailyResetHour(), tz, r.now())
	return periodStart.Before(lastReset)
}

// IsFixedWeeklyPeriodExpired compares a stored start with the most recent
// configured local weekly reset. A zero start is expired, invalid timezone text
// uses UTC, and an equal or later start remains current. Nil LoadLocation
// defaults to time.LoadLocation.
func (r *Record) IsFixedWeeklyPeriodExpired(periodStart time.Time) bool {
	if periodStart.IsZero() {
		return true
	}
	tz, err := r.loadLocation(r.GetQuotaResetTimezone())
	if err != nil {
		// A failed saved location lookup cannot define the calendar reset. Use
		// UTC to compare the stored start with a usable reset date for admission.
		tz = time.UTC
	}
	lastReset := LastFixedWeeklyReset(r.GetQuotaWeeklyResetDay(), r.GetQuotaWeeklyResetHour(), tz, r.now())
	return periodStart.Before(lastReset)
}

// ValidateQuotaResetConfig checks supplied reset modes, numeric ranges, and
// nonempty string timezone names. Nil and empty Extra succeed; a nil loader
// defaults to time.LoadLocation when timezone validation is active.
//
// Mode checks apply to strings. Daily and weekly numeric fields use
// ParseExtraFloat64 and integer conversion before their bounds are tested.
// Monthly dates and hours reject fractional values before persistence. Storage
// helpers apply their documented defaults when reading saved configuration for
// reset calculations.
func ValidateQuotaResetConfig(extra map[string]any, loadLocation func(string) (*time.Location, error)) error {
	if extra == nil {
		return nil
	}
	if loadLocation == nil {
		loadLocation = time.LoadLocation
	}
	if tz, ok := extra["quota_reset_timezone"].(string); ok && tz != "" {
		if _, err := loadLocation(tz); err != nil {
			return errors.New("invalid quota_reset_timezone: must be a valid IANA timezone name")
		}
	}
	if mode, ok := extra["quota_daily_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_daily_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// Bounds apply after the shared numeric conversion. Fractional JSON values
	// are truncated before range checking, and unsupported representations can
	// become zero, matching the readers that consume these saved clock fields.
	if v, ok := extra["quota_daily_reset_hour"]; ok {
		hour := int(ParseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_daily_reset_hour must be between 0 and 23")
		}
	}
	if mode, ok := extra["quota_weekly_reset_mode"].(string); ok {
		if mode != "rolling" && mode != "fixed" {
			return errors.New("quota_weekly_reset_mode must be 'rolling' or 'fixed'")
		}
	}
	// Sunday is zero and Saturday is six in the weekly calendar helpers.
	if v, ok := extra["quota_weekly_reset_day"]; ok {
		day := int(ParseExtraFloat64(v))
		if day < 0 || day > 6 {
			return errors.New("quota_weekly_reset_day must be between 0 (Sunday) and 6 (Saturday)")
		}
	}
	if v, ok := extra["quota_weekly_reset_hour"]; ok {
		hour := int(ParseExtraFloat64(v))
		if hour < 0 || hour > 23 {
			return errors.New("quota_weekly_reset_hour must be between 0 and 23")
		}
	}
	if mode, ok := extra["quota_monthly_reset_mode"].(string); ok && mode != "rolling" && mode != "fixed" {
		return errors.New("quota_monthly_reset_mode must be 'rolling' or 'fixed'")
	}
	if v, ok := extra["quota_monthly_reset_hour"]; ok {
		hour := ParseExtraFloat64(v)
		if hour < 0 || hour > 23 || hour != float64(int(hour)) {
			return errors.New("quota_monthly_reset_hour must be an integer between 0 and 23")
		}
	}
	if v, ok := extra["quota_monthly_reset_day"]; ok {
		day := ParseExtraFloat64(v)
		if day < 1 || day > 31 || day != float64(int(day)) {
			return errors.New("quota_monthly_reset_day must be an integer between 1 and 31")
		}
	}
	return nil
}

// HasAnyQuotaLimit reports whether total, daily, weekly, or monthly provider
// quota is positive.
func (r *Record) HasAnyQuotaLimit() bool {
	return r.GetQuotaLimit() > 0 ||
		r.GetQuotaDailyLimit() > 0 ||
		r.GetQuotaWeeklyLimit() > 0 ||
		r.GetQuotaMonthlyLimit() > 0
}

// IsPeriodExpired compares real elapsed time since periodStart with dur. A zero
// start is expired so the next usage increment can initialize it; this helper
// uses time.Since independently of Record.Now.
func IsPeriodExpired(periodStart time.Time, dur time.Duration) bool {
	if periodStart.IsZero() {
		return true // The next usage increment initializes an unused period.
	}
	return time.Since(periodStart) >= dur
}

// isRollingPeriodExpired compares elapsed time against dur using the record's
// injected clock so simulated and cached evaluations remain deterministic.
func (r *Record) isRollingPeriodExpired(periodStart time.Time, dur time.Duration) bool {
	if periodStart.IsZero() {
		return true
	}
	return r.now().Sub(periodStart) >= dur
}

// IsDailyQuotaPeriodExpired reports whether the stored daily usage window has
// elapsed. Fixed mode uses the configured provider clock and timezone; rolling
// mode uses a twenty-four-hour elapsed duration on the provider clock.
func (r *Record) IsDailyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_daily_start")
	if r.GetQuotaDailyResetMode() == "fixed" {
		return r.IsFixedDailyPeriodExpired(start)
	}
	// Rolling admission measures twenty-four elapsed hours across local clock
	// changes; fixed mode above instead follows the configured calendar reset.
	return r.isRollingPeriodExpired(start, 24*time.Hour)
}

// IsWeeklyQuotaPeriodExpired reports whether the stored weekly usage window has
// elapsed. Fixed mode uses the provider clock and local weekday schedule;
// rolling mode uses seven elapsed days on the provider clock.
func (r *Record) IsWeeklyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_weekly_start")
	if r.GetQuotaWeeklyResetMode() == "fixed" {
		return r.IsFixedWeeklyPeriodExpired(start)
	}
	// Seven elapsed days keep the rolling window duration independent of local
	// timezone transitions; calendar-week rules apply only in the fixed branch.
	return r.isRollingPeriodExpired(start, 7*24*time.Hour)
}

// IsMonthlyQuotaPeriodExpired reports whether monthly consumption has expired.
// Fixed mode follows the configured local date, clamping short months to their
// final date. Rolling mode expires thirty elapsed days after its recorded start
// on the provider clock; missing starts expire immediately so the next
// increment initializes usage. Nil LoadLocation defaults to time.LoadLocation,
// and loader errors fall back to UTC.
func (r *Record) IsMonthlyQuotaPeriodExpired() bool {
	start := r.GetExtraTime("quota_monthly_start")
	if r.GetQuotaMonthlyResetMode() == "fixed" {
		if start.IsZero() {
			return true
		}
		tz, err := r.loadLocation(r.GetQuotaResetTimezone())
		if err != nil {
			// Invalid saved timezone names use UTC so admission can still evaluate
			// the configured calendar window consistently with provider configuration
			// reconciliation.
			tz = time.UTC
		}
		last := billing.LastFixedMonthlyReset(r.GetQuotaMonthlyResetDay(), r.GetQuotaMonthlyResetHour(), tz, r.now())
		return start.Before(last)
	}
	// Thirty elapsed days match the monthly duration used by billing SQL,
	// including when local clock transitions shorten or lengthen calendar days.
	return r.isRollingPeriodExpired(start, 30*24*time.Hour)
}

// IsQuotaExceeded combines the provider's total, daily, weekly, and monthly
// consumption limits. A positive limit is exhausted at equality or above; total
// consumption has no expiration window.
//
// Daily, weekly, and monthly consumption count only while their stored periods
// remain current. Fixed periods compare against local calendar resets, while
// rolling periods use elapsed durations on the provider clock. An expired or
// missing start makes the periodic amount irrelevant to admission until the
// usage writer initializes its next period. The first exhausted active
// dimension rejects the provider, so one current limit can still block it when
// another dimension's stored usage has expired.
func (r *Record) IsQuotaExceeded() bool {
	// Total consumption remains applicable across periodic rollovers. Its limit
	// can therefore reject a provider even when the daily and weekly windows
	// have already expired and their usage is ignored below.
	if limit := r.GetQuotaLimit(); limit > 0 && r.GetQuotaUsed() >= limit {
		return true
	}
	// Daily expiration affects the admission comparison without rewriting Extra.
	// A later billing increment owns resetting the counter and start together,
	// so this reader leaves the persisted amount available for that writer.
	if limit := r.GetQuotaDailyLimit(); limit > 0 {
		if !r.IsDailyQuotaPeriodExpired() && r.GetQuotaDailyUsed() >= limit {
			return true
		}
	}
	// Weekly usage has its own start and reset policy. Evaluate that period
	// separately so expiration of the daily window cannot release a still
	// exhausted weekly allowance.
	if limit := r.GetQuotaWeeklyLimit(); limit > 0 {
		if !r.IsWeeklyQuotaPeriodExpired() && r.GetQuotaWeeklyUsed() >= limit {
			return true
		}
	}
	// Monthly consumption remains an independent restriction when shorter quota
	// periods expire; this read leaves counter rollover to the next billing increment.
	if limit := r.GetQuotaMonthlyLimit(); limit > 0 &&
		!r.IsMonthlyQuotaPeriodExpired() && r.GetQuotaMonthlyUsed() >= limit {
		return true
	}
	return false
}
