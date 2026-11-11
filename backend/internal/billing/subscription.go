package billing

import (
	"math"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

// subscription.go derives subscription status, quota availability and
// quota-window timing.
// Subscription status depends on deletion and its effective time interval,
// with suspension retained only after the interval checks pass. Stored labels
// and wall-clock helpers answer different questions: IsActive requires an
// active stored label, while EffectiveStatus derives the display state.
// Day/week/month usage describes overlapping quota windows, so admission uses
// the smallest enabled remainder and self-revocation checks the highest finite
// configured quota.
//
// Daily reset decisions follow injected calendar midnights, including
// daylight-saving changes and non-midnight legacy anchors. Weekly and monthly
// maintenance use elapsed seven- and thirty-day durations. A positive outer
// limit can permit a shorter window near expiry, but a one-calendar-day
// lifetime retains its daily quota once. These helpers calculate decisions and
// display times; services own status admission and persisted maintenance.

const (
	// Subscription status labels describe stored and derived grant states.
	// EffectiveStatus gives deletion and time precedence, then retains suspension
	// inside an effective interval.
	SubscriptionStatusActive    = "active"    // effective active grant label
	SubscriptionStatusPending   = "pending"   // future queued grant label
	SubscriptionStatusExpired   = "expired"   // elapsed grant lifetime label
	SubscriptionStatusSuspended = "suspended" // stored suspension status label

	SubscriptionStatusRevoked = "revoked" // soft-deleted grant display label

	// These elapsed durations govern window fit, weekly/monthly reset eligibility
	// and rounded remaining days. Calendar-day reset helpers separately follow
	// local midnight across offset changes.
	SubscriptionDailyWindow   = 24 * time.Hour      // 24-hour duration
	SubscriptionWeeklyWindow  = 7 * 24 * time.Hour  // seven-day elapsed duration
	SubscriptionMonthlyWindow = 30 * 24 * time.Hour // thirty-day elapsed duration
)

// APIKeyBillingContext carries the Key's selected funding mode, source and
// subscription availability.
type APIKeyBillingContext struct {
	// Mode and Source distinguish the Key's configured policy from the selected
	// funding resource. ResolveFunding can retain subscription Source even when
	// Available is false; auto Mode can select either resource.
	// Mode: effective Key billing policy.
	// Source: selected funding resource label.
	Mode, Source string
	// Subscription can be present when its funding is unavailable. ResolveFunding
	// keeps the loaded view, whose quota validation can contain in-memory
	// maintenance before admission persists and rereads it.
	Subscription *UserSubscription // selected service subscription view
	// Available reflects funding resolution's subscription status/quota check.
	// Balance selection starts true without reading a balance, and consuming
	// admission still performs its own resource checks.
	Available bool // funding resolution availability result
}

// UserSubscription combines an issued grant's lifetime, overlapping quota
// counters and hydrated relationships.
type UserSubscription struct {
	ID     int64 // issued grant record identifier
	UserID int64 // paying grant owner identifier
	PlanID int64 // issued source plan identifier

	// StartsAt and ExpiresAt define the half-open lifetime used by effective
	// admission. At expiry equality the grant is expired, while exact start
	// equality is already within the interval.
	StartsAt  time.Time // inclusive grant start time
	ExpiresAt time.Time // exclusive grant expiry time
	// Status is the stored label. EffectiveStatus gives DeletedAt and time
	// precedence and respects suspension inside the live interval; other stored
	// labels become active there.
	Status string // stored subscription status label

	// RefundHoldOperationID identifies the refund that blocks use of this
	// grant without changing its stored lifecycle, schedule, or quota.
	RefundHoldOperationID *string

	// DailyWindowStart, WeeklyWindowStart and MonthlyWindowStart identify
	// independent quota periods. Nil anchors can request first activation for a
	// positive limit; reset-time displays return nil for their missing anchors.
	// DailyWindowStart is compared in the injected calendar for daily reset
	// decisions. A non-midnight stored anchor becomes today's midnight only after
	// that calendar advances to another day.
	DailyWindowStart   *time.Time // stored daily quota anchor
	WeeklyWindowStart  *time.Time // stored weekly quota anchor
	MonthlyWindowStart *time.Time // stored monthly quota anchor

	// DailyLimitUSD, WeeklyLimitUSD, MonthlyLimitUSD and DailyUsageUSD,
	// WeeklyUsageUSD, MonthlyUsageUSD describe overlapping periods. Remaining
	// quota takes the smallest enabled period remainder; absent or nonpositive
	// limits are unlimited for ordinary quota helpers.
	DailyLimitUSD   *float64 // USD
	WeeklyLimitUSD  *float64 // USD
	MonthlyLimitUSD *float64 // USD

	DailyUsageUSD   float64 // USD
	WeeklyUsageUSD  float64 // USD
	MonthlyUsageUSD float64 // USD

	AssignedBy *int64    // optional issuing administrator identifier
	AssignedAt time.Time // grant issuance observation time
	// SourceOrderID links a payment issuance to its order.
	// AssignOrExtendSubscription reuses an existing order-sourced grant, while
	// manual and redemption issuance can leave this pointer nil.
	SourceOrderID *int64 // optional payment source order
	Notes         string // issuance or maintenance reason

	CreatedAt time.Time // grant record creation time
	UpdatedAt time.Time // grant record update time
	// DeletedAt takes precedence over every timestamp and stored status in
	// EffectiveStatus. A nonnil deletion marker yields revoked even when the
	// original lifetime has already expired.
	DeletedAt *time.Time // optional revocation deletion timestamp

	// User, Plan and AssignedByUser are optional hydrated relationships returned
	// by repository reads. Plan supplies current group coverage, and copies of
	// the subscription value retain these referenced objects unless a mapper
	// replaces them.
	User           *UserSummary      // optional paying user view
	Plan           *SubscriptionPlan // optional current plan view
	AssignedByUser *UserSummary      // optional issuing administrator view
}

// SubscriptionWindowActivation selects positive-limit windows that can receive
// their first stored anchor.
type SubscriptionWindowActivation struct {
	// Daily, Weekly and Monthly request each eligible first activation
	// independently. WindowActivationAt selects missing positive-limit anchors,
	// and the service persists these selected flags through ActivateWindows.
	Daily   bool // activate daily quota anchor
	Weekly  bool // activate weekly quota anchor
	Monthly bool // activate monthly quota anchor
}

// SubscriptionRemainingForDisplay reports remaining quota for an already
// selected grant, with a separate unlimited sentinel. Nil subscriptions return
// zero, and all absent or nonpositive limits return negative one. Otherwise
// AvailableQuotaUSD chooses the smallest enabled window remainder.
//
// The day, week and month counters can include the same charge, so their
// remainders are alternatives constraining one request rather than amounts to
// add. This helper performs no status check or persisted window maintenance;
// the caller supplies the view appropriate for display.
func SubscriptionRemainingForDisplay(sub *UserSubscription) float64 {
	if sub == nil {
		return 0
	}
	if (sub.DailyLimitUSD == nil || *sub.DailyLimitUSD <= 0) && (sub.WeeklyLimitUSD == nil || *sub.WeeklyLimitUSD <= 0) && (sub.MonthlyLimitUSD == nil || *sub.MonthlyLimitUSD <= 0) {
		return -1
	}
	return sub.AvailableQuotaUSD()
}

// Any reports whether at least one quota window requests first activation.
func (a SubscriptionWindowActivation) Any() bool {
	return a.Daily || a.Weekly || a.Monthly
}

// IsActive reports whether a nonnil grant has stored active status and
// currently lies in its half-open lifetime. Deleted or refund-held grants
// fail this check. Callers needing derived status use IsEffective.
func (s *UserSubscription) IsActive() bool {
	now := time.Now()
	return s.DeletedAt == nil && s.RefundHoldOperationID == nil && s.Status == SubscriptionStatusActive && !now.Before(s.StartsAt) && now.Before(s.ExpiresAt)
}

// IsPending reports a nondeleted stored pending grant whose start is still
// future at the wall clock. Nil grants return false; this predicate does not
// validate expiry or derive other stored labels.
func (s *UserSubscription) IsPending() bool {
	if s == nil {
		return false
	}
	return s.DeletedAt == nil && s.Status == SubscriptionStatusPending && time.Now().Before(s.StartsAt)
}

// IsEffective reports a grant whose wall-clock time lies in its half-open
// lifetime and whose derived status is active. Nil or deleted grants fail, and
// suspension is interpreted by EffectiveStatus.
func (s *UserSubscription) IsEffective() bool {
	if s == nil {
		return false
	}
	now := time.Now()
	return !now.Before(s.StartsAt) && now.Before(s.ExpiresAt) && s.EffectiveStatus(now) == SubscriptionStatusActive
}

// EffectiveStatus derives a grant's display state at the supplied instant. Nil
// yields expired, deletion yields revoked, and expiry equality or earlier
// yields expired before any future-start or suspension decision. A refund
// hold suspends a current or queued grant. A future start otherwise yields pending.
//
// Inside the live interval, stored suspension remains suspended and every
// other stored label becomes active. Callers therefore cannot treat a stored
// expired or revoked label alone as an effective prohibition when its
// deletion/time fields still describe a live grant. IsActive deliberately asks
// the separate stored-active question.
func (s *UserSubscription) EffectiveStatus(now time.Time) string {
	if s == nil {
		return SubscriptionStatusExpired
	}
	if s.DeletedAt != nil {
		return SubscriptionStatusRevoked
	}
	if !s.ExpiresAt.After(now) {
		return SubscriptionStatusExpired
	}
	if s.RefundHoldOperationID != nil {
		return SubscriptionStatusSuspended
	}
	if now.Before(s.StartsAt) {
		return SubscriptionStatusPending
	}
	switch s.Status {
	case SubscriptionStatusSuspended:
		return SubscriptionStatusSuspended
	default:
		return SubscriptionStatusActive
	}
}

// IsExpired reports whether a nonnil grant's expiry is at or before the
// current wall clock. It reads the deadline alone; callers interpret deletion,
// suspension and future start through the separate status helpers.
func (s *UserSubscription) IsExpired() bool {
	return !s.ExpiresAt.After(time.Now())
}

// DaysRemaining returns whole twenty-four-hour days rounded upward from the
// wall clock to a nonnil grant's expiry. Expired grants return zero;
// calendar-day boundaries and daylight-saving offsets do not change this
// duration-based display.
func (s *UserSubscription) DaysRemaining() int {
	return s.DaysRemainingAt(time.Now())
}

// DaysRemainingAt returns nonnegative twenty-four-hour days remaining from the
// supplied instant, rounded upward for any partial final day. A nonnil grant
// at or past expiry returns zero; this is elapsed-duration display rather than
// a calendar-date count.
func (s *UserSubscription) DaysRemainingAt(now time.Time) int {
	remaining := s.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}

	// Round a positive partial twenty-four-hour duration upward so a live
	// remainder never displays as zero days.
	days := int(remaining / SubscriptionDailyWindow)
	if remaining%SubscriptionDailyWindow != 0 {
		days++
	}
	return days
}

// HasQuotaLimit reports whether any quota pointer on a nonnil grant contains a
// positive number. Nil and nonpositive limits do not enable a window; positive
// infinity qualifies under this ordinary positivity predicate.
func (s *UserSubscription) HasQuotaLimit() bool {
	return PositiveSubscriptionLimit(s.DailyLimitUSD) ||
		PositiveSubscriptionLimit(s.WeeklyLimitUSD) ||
		PositiveSubscriptionLimit(s.MonthlyLimitUSD)
}

// HasOneTimeDailyQuota reports an expiry at or before the day after a nonzero
// start, using calendar addition in the start's zone. Nil or zero timestamps
// fail; callers validate lifetime ordering separately because reversed
// intervals also qualify.
func (s *UserSubscription) HasOneTimeDailyQuota() bool {
	if s == nil || s.StartsAt.IsZero() || s.ExpiresAt.IsZero() {
		return false
	}
	return !s.ExpiresAt.After(s.StartsAt.AddDate(0, 0, 1))
}

// NeedsWindowActivationAt reports whether WindowActivationAt selects any quota
// at the supplied instant. Nil or out-of-interval grants select none; the
// service uses this result to request first-anchor persistence before
// consumption.
func (s *UserSubscription) NeedsWindowActivationAt(now time.Time) bool {
	return s.WindowActivationAt(now).Any()
}

// WindowActivationAt selects missing positive-limit quota anchors inside the
// supplied grant interval. Nil, unlimited or out-of-interval inputs return no
// selections; it evaluates timestamp/limit fields and leaves status/deletion
// admission to its caller. The proposed anchor is midnight in now's own zone.
//
// Daily activation allows a one-time daily lifetime or the ordinary expiry-fit
// rule. Weekly and monthly activation use seven- and thirty-day fit, with
// positive outer limits permitting shorter lower windows near expiry. Existing
// nonnil anchors stay unchanged, and the result requests persistence rather
// than altering the grant.
func (s *UserSubscription) WindowActivationAt(now time.Time) SubscriptionWindowActivation {
	var activation SubscriptionWindowActivation
	if s == nil || !s.HasQuotaLimit() || now.Before(s.StartsAt) || !now.Before(s.ExpiresAt) {
		return activation
	}

	windowStart := startOfDay(now)
	if PositiveSubscriptionLimit(s.DailyLimitUSD) && s.DailyWindowStart == nil {
		activation.Daily = s.HasOneTimeDailyQuota() ||
			s.CanStartQuotaWindow(windowStart, SubscriptionDailyWindow)
	}
	if PositiveSubscriptionLimit(s.WeeklyLimitUSD) && s.WeeklyWindowStart == nil {
		activation.Weekly = s.CanStartQuotaWindow(windowStart, SubscriptionWeeklyWindow)
	}
	if PositiveSubscriptionLimit(s.MonthlyLimitUSD) && s.MonthlyWindowStart == nil {
		activation.Monthly = s.CanStartQuotaWindow(windowStart, SubscriptionMonthlyWindow)
	}
	return activation
}

// NeedsDailyReset evaluates calendar-day maintenance using the current wall
// clock and supplied calendar. Nil grants or missing anchors return false;
// AutomaticDailyWindowStartWithCalendar owns one-time lifetime and expiry-fit
// decisions.
func (s *UserSubscription) NeedsDailyReset(calendar timezone.Calendar) bool {
	return s.NeedsDailyResetAt(time.Now(), calendar)
}

// NeedsDailyResetAt reports whether calendar-day maintenance is eligible at
// the supplied instant. It delegates anchor selection to
// AutomaticDailyWindowStartWithCalendar and returns only its flag, leaving
// actual persistence to the service.
func (s *UserSubscription) NeedsDailyResetAt(now time.Time, calendar timezone.Calendar) bool {
	_, ok := s.AutomaticDailyWindowStartWithCalendar(now, calendar)
	return ok
}

// AutomaticDailyWindowStartWithCalendar returns today's calendar midnight when
// a stored daily anchor can advance. Nil grants, missing anchors, zero/past
// expiry and one-time daily lifetimes return zero time with false. It compares
// calendar dates rather than requiring twenty-four elapsed hours.
//
// That comparison also replaces legacy non-midnight anchors after the next
// calendar midnight. The candidate must satisfy CanStartQuotaWindow, allowing
// an expiry tail only under positive outer protection. The caller handles
// active-status validation and persistence; this method leaves the stored
// anchor and usage unchanged.
func (s *UserSubscription) AutomaticDailyWindowStartWithCalendar(now time.Time, calendar timezone.Calendar) (time.Time, bool) {
	if s == nil || s.DailyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return time.Time{}, false
	}
	if s.HasOneTimeDailyQuota() {
		return time.Time{}, false
	}
	today := calendar.StartOfDay(now)
	if !today.After(calendar.StartOfDay(*s.DailyWindowStart)) {
		return time.Time{}, false
	}
	if !s.CanStartQuotaWindow(today, SubscriptionDailyWindow) {
		return time.Time{}, false
	}
	return today, true
}

// NeedsWeeklyReset evaluates weekly maintenance at the current wall clock
// without mutating the grant. Nil or missing-anchor grants return false;
// NeedsWeeklyResetAt owns elapsed-duration and expiry-fit rules for this
// request.
func (s *UserSubscription) NeedsWeeklyReset() bool {
	return s.NeedsWeeklyResetAt(time.Now())
}

// NeedsWeeklyResetAt reports whether an existing weekly anchor can advance at
// the supplied instant. Nil, missing-anchor or expired grants fail, and seven
// elapsed days must have passed since the stored start. The replacement
// candidate is midnight in now's own zone.
//
// A full weekly window must fit before expiry unless a positive monthly limit
// supplies outer protection. This method checks the anchor and expiry fields,
// leaving status admission and persisted usage clearing to the service.
// Calendar-date changes alone do not exhaust the elapsed weekly duration.
func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
	if s == nil || s.WeeklyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Before(s.WeeklyWindowStart.Add(SubscriptionWeeklyWindow)) {
		return false
	}
	return s.CanStartQuotaWindow(startOfDay(now), SubscriptionWeeklyWindow)
}

// NeedsMonthlyReset evaluates monthly maintenance at the current wall clock
// without mutating the grant. Nil or missing-anchor grants return false;
// NeedsMonthlyResetAt checks the elapsed thirty-day period and remaining
// lifetime fit.
func (s *UserSubscription) NeedsMonthlyReset() bool {
	return s.NeedsMonthlyResetAt(time.Now())
}

// NeedsMonthlyResetAt reports whether an existing monthly anchor can advance
// at the supplied instant. Nil, missing-anchor or expired grants fail, and
// thirty elapsed days must have passed since the stored start. The replacement
// candidate is midnight in now's own zone.
//
// Monthly has no outer protection, so its full thirty-day period must fit
// before expiry. This helper leaves active-status admission and usage
// persistence to the service; a calendar-month change by itself does not
// finish this elapsed monthly window.
func (s *UserSubscription) NeedsMonthlyResetAt(now time.Time) bool {
	if s == nil || s.MonthlyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Before(s.MonthlyWindowStart.Add(SubscriptionMonthlyWindow)) {
		return false
	}
	return s.CanStartQuotaWindow(startOfDay(now), SubscriptionMonthlyWindow)
}

// CanStartFullQuotaWindow reports whether a positive elapsed duration starting
// before a nonzero expiry fits completely. Nil grants or invalid inputs fail;
// a candidate ending exactly at expiry is accepted.
func (s *UserSubscription) CanStartFullQuotaWindow(windowStart time.Time, duration time.Duration) bool {
	if s == nil || s.ExpiresAt.IsZero() || duration <= 0 || !windowStart.Before(s.ExpiresAt) {
		return false
	}
	return !windowStart.Add(duration).After(s.ExpiresAt)
}

// CanStartQuotaWindow accepts a candidate before nonzero expiry when its full
// duration fits or a positive outer quota protects a shorter tail. Nil grants
// fail, and the helper does not require the candidate to start after StartsAt
// or verify active status.
//
// Daily windows can use weekly/monthly protection, weekly windows can use
// monthly protection, and monthly or other durations have no such exception.
// Missing or nonpositive outer limits supply none. The caller determines the
// candidate anchor and performs grant admission before acting on this
// calculated fit.
// @project-doc docs/domains/payments_and_entitlements.md#subscription_quota_windows
func (s *UserSubscription) CanStartQuotaWindow(windowStart time.Time, duration time.Duration) bool {
	if s == nil || s.ExpiresAt.IsZero() || !windowStart.Before(s.ExpiresAt) {
		return false
	}
	return s.CanStartFullQuotaWindow(windowStart, duration) || s.HasFiniteOuterQuotaLimit(duration)
}

// HasFiniteOuterQuotaLimit reports positive outer protection for the requested
// elapsed window duration. Daily uses either the weekly or monthly limit,
// weekly uses monthly, and all other durations return false. Nil grants return
// false.
//
// Despite this method's name, the test uses ordinary positivity and therefore
// accepts positive infinity; it does not apply
// FinitePositiveSubscriptionLimit. The distinction matters when
// self-revocation evaluates a highest finite quota, while expiry-tail window
// selection uses these ordinary positive outer limits.
func (s *UserSubscription) HasFiniteOuterQuotaLimit(duration time.Duration) bool {
	if s == nil {
		return false
	}
	switch duration {
	case SubscriptionDailyWindow:
		return PositiveSubscriptionLimit(s.WeeklyLimitUSD) || PositiveSubscriptionLimit(s.MonthlyLimitUSD)
	case SubscriptionWeeklyWindow:
		return PositiveSubscriptionLimit(s.MonthlyLimitUSD)
	default:
		return false
	}
}

// DailyResetTime returns a fresh display deadline for a nonnil grant's stored
// daily anchor. Missing anchors return nil, and a one-time daily lifetime
// returns expiry immediately. Otherwise the candidate is the next calendar
// midnight after the anchor's date, including legacy non-midnight starts.
//
// Calendar addition follows offset changes rather than assuming twenty-four
// elapsed hours. If CanStartQuotaWindow rejects another daily period, the
// display deadline becomes subscription expiry; positive outer protection can
// keep an earlier tail reset. The calculation leaves the stored anchor
// unchanged and performs no persistence.
func (s *UserSubscription) DailyResetTime(calendar timezone.Calendar) *time.Time {
	if s.DailyWindowStart == nil {
		return nil
	}
	if s.HasOneTimeDailyQuota() {
		t := s.ExpiresAt
		return &t
	}
	// Calendar addition reaches the next local midnight even when the intervening
	// day lasts twenty-three or twenty-five hours.
	t := calendar.StartOfDay(*s.DailyWindowStart).AddDate(0, 0, 1)
	// When another period cannot begin, show expiry as the final quota end; this
	// substitution does not reset usage or issue another allowance.
	if !s.CanStartQuotaWindow(t, SubscriptionDailyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

// WeeklyResetTime returns a fresh display deadline seven elapsed days after a
// nonnil grant's stored weekly anchor. A missing anchor returns nil, and a
// local offset change can shift the displayed hour because addition uses
// one-hundred-sixty-eight hours. It does not align to a calendar-week
// boundary.
//
// If another weekly period cannot fit and no positive monthly protection
// applies, the deadline becomes subscription expiry. With monthly protection a
// lower tail reset remains available. The caller supplies a grant for display;
// this method changes neither its anchor nor persisted quota usage.
func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.WeeklyWindowStart.Add(SubscriptionWeeklyWindow)
	// When another period cannot begin, show expiry as the final quota end; this
	// substitution does not reset usage or issue another allowance.
	if !s.CanStartQuotaWindow(t, SubscriptionWeeklyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

// MonthlyResetTime returns a fresh display deadline thirty elapsed days after
// a nonnil grant's stored monthly anchor. A missing anchor returns nil, and
// calendar month lengths do not alter that elapsed duration. The method leaves
// the stored anchor unchanged.
//
// Monthly has no outer quota window, so a candidate unable to start another
// complete thirty-day period is replaced by subscription expiry. That
// displayed end marks the grant's remaining lifetime, rather than granting
// another monthly quota. The caller owns status admission and any persisted
// maintenance.
func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.MonthlyWindowStart.Add(SubscriptionMonthlyWindow)
	// When another period cannot begin, show expiry as the final quota end; this
	// substitution does not reset usage or issue another allowance.
	if !s.CanStartQuotaWindow(t, SubscriptionMonthlyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

// RemainingDailyUSD returns the daily limit minus recorded usage, clamped at
// zero for overuse. A nonnil grant with absent or nonpositive daily limit
// returns nil, indicating an unlimited window to quota selection.
func (s *UserSubscription) RemainingDailyUSD() *float64 {
	return RemainingWindowAmount(s.DailyLimitUSD, s.DailyUsageUSD)
}

// RemainingWeeklyUSD returns the weekly limit minus recorded usage, clamped at
// zero for overuse. A nonnil grant with absent or nonpositive weekly limit
// returns nil, so weekly data does not constrain quota selection.
func (s *UserSubscription) RemainingWeeklyUSD() *float64 {
	return RemainingWindowAmount(s.WeeklyLimitUSD, s.WeeklyUsageUSD)
}

// RemainingMonthlyUSD returns the monthly limit minus recorded usage, clamped
// at zero for overuse. A nonnil grant with absent or nonpositive monthly limit
// returns nil, indicating that this period is unlimited.
func (s *UserSubscription) RemainingMonthlyUSD() *float64 {
	return RemainingWindowAmount(s.MonthlyLimitUSD, s.MonthlyUsageUSD)
}

// AvailableQuotaUSD returns the smallest enabled day/week/month remainder for
// a nonnil grant. Absent and nonpositive windows contribute nil, and an
// entirely unlimited grant returns zero from MinRemainingWindowAmount. Display
// callers use SubscriptionRemainingForDisplay to distinguish unlimited from
// exhausted.
//
// Each enabled counter can include the same charge, so the strictest remainder
// limits one request and the amounts are not added. This method does not
// normalize stale windows or check effective status; callers supply their
// current eligible or display view before interpreting the result.
func (s *UserSubscription) AvailableQuotaUSD() float64 {
	return MinRemainingWindowAmount(
		s.RemainingDailyUSD(),
		s.RemainingWeeklyUSD(),
		s.RemainingMonthlyUSD(),
	)
}

// HighestQuotaExhausted reports consumption of the highest configured positive
// finite quota for self-revocation. Monthly takes precedence over weekly and
// then daily; the first qualifying limit decides, so an available outer limit
// blocks lower exhausted windows. Nil grants or no qualifying limit return
// false.
//
// Zero, negative, NaN and infinite limits do not qualify here. Usage at the
// selected limit is already exhausted, while status, ownership and window
// maintenance remain service checks before revocation. Lower windows cannot
// authorize revocation while the selected outer quota still has remaining
// amount.
// @project-doc docs/domains/payments_and_entitlements.md#subscription_self_revoke
func (s *UserSubscription) HighestQuotaExhausted() bool {
	if s == nil {
		return false
	}
	for _, quota := range []struct {
		// limit, used and ok keep each period's amount pair with its finite-positive
		// eligibility. HighestQuotaExhausted visits these records in monthly,
		// weekly, daily precedence order.
		limit float64 // candidate finite quota amount
		used  float64 // corresponding period consumption amount
		ok    bool    // positive finite limit eligibility
	}{
		{limit: ValueOrZeroLimit(s.MonthlyLimitUSD), used: s.MonthlyUsageUSD, ok: FinitePositiveSubscriptionLimit(s.MonthlyLimitUSD)},
		{limit: ValueOrZeroLimit(s.WeeklyLimitUSD), used: s.WeeklyUsageUSD, ok: FinitePositiveSubscriptionLimit(s.WeeklyLimitUSD)},
		{limit: ValueOrZeroLimit(s.DailyLimitUSD), used: s.DailyUsageUSD, ok: FinitePositiveSubscriptionLimit(s.DailyLimitUSD)},
	} {
		if quota.ok {
			return quota.used >= quota.limit
		}
	}
	return false
}

// ValueOrZeroLimit returns a pointed-to limit unchanged or zero when absent.
// It performs no positivity or finite-value validation; HighestQuotaExhausted
// separately decides which configured windows qualify for its ordered test.
func ValueOrZeroLimit(value *float64) float64 {
	if value == nil {
		return 0
	}
	return *value
}

// FinitePositiveSubscriptionLimit reports a present positive limit that is
// neither NaN nor infinite. HighestQuotaExhausted uses this stricter predicate
// for self-revocation; ordinary outer-window protection uses
// PositiveSubscriptionLimit and can accept positive infinity.
func FinitePositiveSubscriptionLimit(limit *float64) bool {
	return PositiveSubscriptionLimit(limit) && !math.IsNaN(*limit) && !math.IsInf(*limit, 0)
}

// RemainingWindowAmount returns limit minus usage in new scalar storage,
// clamping a negative remainder to zero. Nil or nonpositive limits return nil;
// supplied nonfinite values receive ordinary floating-point operations rather
// than validation.
func RemainingWindowAmount(limit *float64, used float64) *float64 {
	if limit == nil || *limit <= 0 {
		return nil
	}
	remaining := *limit - used
	// Excess usage leaves no spendable amount, so expose zero to quota selection.
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

// MinRemainingWindowAmount chooses the smallest nonnil supplied remainder and
// returns zero when every value is absent. It skips unlimited-window nil
// pointers and copies the chosen numeric value rather than returning caller
// storage. Input order determines which value initializes the comparison.
//
// The helper performs ordinary floating-point comparisons, so callers validate
// nonfinite inputs when their policy requires finite amounts.
// AvailableQuotaUSD uses the zero empty result, while its display wrapper
// separately returns negative one for an entirely unlimited grant. No quota or
// status state is mutated by this selection.
func MinRemainingWindowAmount(values ...*float64) float64 {
	var (
		min   float64
		found bool
	)
	for _, value := range values {
		if value == nil {
			continue
		}
		if !found || *value < min {
			min = *value
			found = true
		}
	}
	// With no enabled window, return the arithmetic helper's zero result; the
	// display wrapper owns its distinct negative-one unlimited representation.
	if !found {
		return 0
	}
	return min
}

// PositiveSubscriptionLimit reports a present number greater than zero,
// including positive infinity. Nil, zero, negative and NaN values fail; use
// FinitePositiveSubscriptionLimit when the consuming policy also excludes
// infinite limits.
func PositiveSubscriptionLimit(limit *float64) bool {
	return limit != nil && *limit > 0
}

// startOfDay constructs midnight on the input's calendar date in that time's
// own location. Callers supply a time in their intended window zone; the
// helper does not substitute the application's configured calendar.
func startOfDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// subscriptionPlanIncludesGroup reports exact group membership, treating an
// empty loaded plan list as unrestricted. Nil plans or nonpositive group IDs
// fail; callers provide the current plan view before interpreting coverage.
func subscriptionPlanIncludesGroup(plan *SubscriptionPlan, groupID int64) bool {
	if plan == nil || groupID <= 0 {
		return false
	}
	if len(plan.GroupIDs) == 0 {
		return true
	}
	return slices.Contains(plan.GroupIDs, groupID)
}

// SubscriptionAllowsGroup reports whether a loaded subscription plan covers a
// positive group ID. Nil subscription/plan or nonpositive IDs fail, while an
// empty plan group list is unrestricted; quota and time eligibility are
// separate checks.
func SubscriptionAllowsGroup(subscription *UserSubscription, groupID int64) bool {
	if subscription == nil || subscription.Plan == nil || groupID <= 0 {
		return false
	}
	return subscriptionPlanIncludesGroup(subscription.Plan, groupID)
}
