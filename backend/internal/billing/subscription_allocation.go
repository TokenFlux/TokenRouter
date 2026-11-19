package billing

import (
	"math"
	"time"
)

// subscription_allocation.go allocates charges across ordered subscriptions
// and computes quota updates.
// A charge can use multiple ordered subscriptions whose quota amounts overlap
// across daily, weekly and monthly periods. Allocation consumes the smallest
// enabled remainder on each supplied row and advances through that row order.
// Storage supplies already eligible, appropriately selected locked rows; these
// calculations do not perform ownership, status or group eligibility queries.
//
// Commands with positive base amount convert coverage through each
// subscription's rate, so uncovered base dollars differ from accumulated
// billed subscription dollars. Window normalization handles first activation,
// elapsed resets and expiry-tail protection before calculating capacity. A
// one-day lifetime retains its activated daily use once, while a zero selected
// rate finishes base coverage without issuing a quota update. The result
// describes writes for the caller's transaction.

// SettlementSubscription carries one locked grant's quota snapshot and current
// plan rate overrides.
type SettlementSubscription struct {
	ID     int64 // selected grant record identifier
	PlanID int64 // selected source plan identifier
	// StartsAt and ExpiresAt come from the selected grant lifetime. The storage
	// caller filters effective rows before allocation; these calculations use the
	// timestamps to control quota activation and reset fit.
	StartsAt  time.Time // inclusive grant start time
	ExpiresAt time.Time // exclusive grant expiry time
	// DailyWindowStart, WeeklyWindowStart and MonthlyWindowStart are normalized
	// independently. Returned nonnil anchors use new time storage, while nil or
	// zero starts can request activation when that period is permitted.
	DailyWindowStart   *time.Time // stored daily quota anchor
	WeeklyWindowStart  *time.Time // stored weekly quota anchor
	MonthlyWindowStart *time.Time // stored monthly quota anchor
	// DailyLimitUSD, WeeklyLimitUSD, MonthlyLimitUSD and DailyUsageUSD,
	// WeeklyUsageUSD, MonthlyUsageUSD describe overlapping quotas. Allocation
	// adds the same billed amount to every positive-limit period, and the
	// smallest remaining period caps coverage.
	DailyLimitUSD   *float64 // USD
	WeeklyLimitUSD  *float64 // USD
	MonthlyLimitUSD *float64 // USD
	DailyUsageUSD   float64  // USD
	WeeklyUsageUSD  float64  // USD
	MonthlyUsageUSD float64  // USD
	// PlanGroupRateMultipliers remains shared when the row value is copied.
	// Allocation reads a positive entry for the command's group and leaves this
	// map unchanged while returning quota updates.
	PlanGroupRateMultipliers map[int64]float64 // current plan group rate overrides
}

// SubscriptionAllocationPlan carries calculated grant updates and ordered
// funding records for transaction persistence.
type SubscriptionAllocationPlan struct {
	// Remaining and SubscriptionAmount use different bases when UsesBaseAmount is
	// true: Remaining is uncovered base USD, while SubscriptionAmount adds billed
	// subscription USD after each selected multiplier. Otherwise both use
	// billable USD.
	// Remaining: uncovered request amount USD.
	// SubscriptionAmount: accumulated billed subscription USD.
	Remaining, SubscriptionAmount float64
	// Allocations contains one funding record per positive allocation in supplied
	// row order. IncludeAllocationPricing additionally retains covered base
	// amount and the selected multiplier in each record.
	Allocations []BillingAllocation // ordered selected grant funding
	// Updates contains only rows that received positive allocations, with
	// normalized anchors and incremented positive-limit counters. Normalized rows
	// skipped for empty capacity or zero-rate completion are absent from this
	// result.
	Updates []SettlementSubscription // selected grant window replacements
}

// UsesBaseAmount reports a command with a positive unmultiplied base amount
// for rate-dependent allocation. Nil or nonpositive base input uses billable
// amount instead; callers validate finite financial inputs before calculation.
func UsesBaseAmount(cmd *UsageBillingCommand) bool {
	return cmd != nil && cmd.BaseAmountUSD > 0
}

// NonNegativeRate clamps a negative multiplier to zero and otherwise returns
// it unchanged. It performs no finite-value validation, so NaN and positive
// infinity retain their supplied floating-point values.
func NonNegativeRate(rate float64) float64 {
	// Negative rates contribute no billable amount to this funding calculation.
	if rate < 0 {
		return 0
	}
	return rate
}

// SettlementSubscriptionRateMultiplier selects the row's positive rate
// override for a positive supplied group ID, or the supplied default. An
// override is multiplied by scale, whose nonpositive value becomes one only on
// that selected override path. Missing, unselected or nonpositive entries
// leave the default unscaled.
//
// The chosen result passes through NonNegativeRate, allowing a negative
// default to become zero. The helper reads current plan entries without
// mutating their map, and it validates neither finite values nor grant/group
// eligibility. Storage and command construction supply those eligible inputs
// before allocation uses the selected multiplier.
func SettlementSubscriptionRateMultiplier(row SettlementSubscription, groupID *int64, defaultRate, scale float64) float64 {
	rate := defaultRate
	// A plan override belongs to the final positive group; absent or unconfigured
	// groups retain the command's default rate and do not apply its override
	// scale.
	if groupID != nil && *groupID > 0 {
		if value, ok := row.PlanGroupRateMultipliers[*groupID]; ok && value > 0 {
			if scale <= 0 {
				scale = 1
			}
			rate = value * scale
		}
	}
	return NonNegativeRate(rate)
}

// NormalizeSettlementSubscription returns a row copy whose quota windows are
// ready for allocation at now. Replacement anchors use midnight in now's own
// zone, daily resets by calendar day in that zone, weekly uses seven elapsed
// days and monthly uses thirty elapsed days. Limits and plan-rate maps remain
// shared read-only values.
//
// Positive weekly/monthly limits protect daily expiry tails, and positive
// monthly limits protect weekly tails; monthly has no outer protection.
// NormalizeSettlementWindow applies each period's activation/reset decision
// and retains one-time daily usage. This helper changes local fields only; its
// caller persists the rows that receive an allocation.
// @project-doc docs/domains/payments_and_entitlements.md#subscription_quota_windows
func NormalizeSettlementSubscription(row SettlementSubscription, now time.Time) SettlementSubscription {
	windowStart := startOfDay(now)
	dailyHasFiniteOuterLimit := PositiveSubscriptionLimit(row.WeeklyLimitUSD) || PositiveSubscriptionLimit(row.MonthlyLimitUSD)
	weeklyHasFiniteOuterLimit := PositiveSubscriptionLimit(row.MonthlyLimitUSD)

	dailyStart, dailyUsage := NormalizeSettlementWindow(
		row.DailyWindowStart, row.DailyLimitUSD, row.DailyUsageUSD,
		windowStart, 24*time.Hour, now, row.StartsAt, row.ExpiresAt, dailyHasFiniteOuterLimit,
	)
	weeklyStart, weeklyUsage := NormalizeSettlementWindow(
		row.WeeklyWindowStart, row.WeeklyLimitUSD, row.WeeklyUsageUSD,
		windowStart, 7*24*time.Hour, now, row.StartsAt, row.ExpiresAt, weeklyHasFiniteOuterLimit,
	)
	monthlyStart, monthlyUsage := NormalizeSettlementWindow(
		row.MonthlyWindowStart, row.MonthlyLimitUSD, row.MonthlyUsageUSD,
		windowStart, 30*24*time.Hour, now, row.StartsAt, row.ExpiresAt, false,
	)

	row.DailyWindowStart = dailyStart
	row.WeeklyWindowStart = weeklyStart
	row.MonthlyWindowStart = monthlyStart
	row.DailyUsageUSD = dailyUsage
	row.WeeklyUsageUSD = weeklyUsage
	row.MonthlyUsageUSD = monthlyUsage
	return row
}

// NormalizeSettlementWindow returns a copied or replacement anchor and its
// effective usage for one supplied period. Absent or nonpositive limits retain
// usage and copy any existing anchor. For a twenty-four-hour period whose
// expiry is no later than the day after start, a missing/zero anchor activates
// once and an established anchor keeps its usage.
//
// Other periods need the proposed resetStart to fit before expiry or have the
// supplied outer protection. A denied replacement retains established usage,
// turning missing/zero anchors into nil. An allowed missing, zero or elapsed
// anchor becomes resetStart with zero usage; twenty-four-hour daily windows
// advance on calendar-date rollover in resetStart's location, while longer
// periods compare stored start plus duration to now. Callers provide eligible
// lifetimes and positive durations, since this normalization itself does not
// admit the grant.
func NormalizeSettlementWindow(
	windowStart *time.Time,
	limit *float64,
	used float64,
	resetStart time.Time,
	duration time.Duration,
	now, startsAt, expiresAt time.Time,
	hasFiniteOuterLimit bool,
) (*time.Time, float64) {
	// An unlimited period contributes no allocation constraint. Retain its
	// observed usage and copy any anchor so normalization does not erase that
	// recorded observation.
	if limit == nil || *limit <= 0 {
		if windowStart == nil {
			return nil, used
		}
		start := *windowStart
		return &start, used
	}

	// A one-day lifetime has one daily allowance; an existing anchor keeps
	// consumption even after later day boundaries, preventing another issuance of
	// that quota.
	if duration == SubscriptionDailyWindow && !expiresAt.After(startsAt.In(resetStart.Location()).AddDate(0, 0, 1)) {
		if windowStart == nil || windowStart.IsZero() {
			start := resetStart
			return &start, 0
		}
		start := *windowStart
		return &start, used
	}

	// Keep established usage when a replacement period cannot start. Positive
	// outer protection can permit a lower tail, but an unprotected top period
	// needs its complete duration.
	if !CanStartSettlementWindow(resetStart, duration, expiresAt, hasFiniteOuterLimit) {
		if windowStart == nil || windowStart.IsZero() {
			return nil, used
		}
		start := *windowStart
		return &start, used
	}

	// Daily quotas align with calendar midnights in resetStart's zone so legacy
	// non-midnight anchors and 23-hour DST spring-forward days reset with precheck;
	// weekly and monthly windows continue to require full elapsed durations.
	windowElapsed := windowStart == nil || windowStart.IsZero()
	if !windowElapsed {
		if duration == SubscriptionDailyWindow {
			windowElapsed = startOfDay(resetStart).After(startOfDay(windowStart.In(resetStart.Location())))
		} else {
			windowElapsed = !windowStart.Add(duration).After(now)
		}
	}
	if windowElapsed {
		start := resetStart
		return &start, 0
	}
	start := *windowStart
	return &start, used
}

// CanStartSettlementWindow reports whether a positive duration beginning
// before a nonzero expiry can start under the supplied protection flag. A
// complete period ending exactly at expiry fits. With outer protection true,
// the candidate may end after expiry while its start still precedes expiry.
//
// The caller computes that flag from the appropriate positive outer limits and
// owns grant lifetime/status admission. This helper trusts the flag and does
// not inspect quota amounts or StartsAt, so it can evaluate the same fit for
// each normalized period without issuing any persisted allowance.
func CanStartSettlementWindow(windowStart time.Time, duration time.Duration, expiresAt time.Time, hasFiniteOuterLimit bool) bool {
	if expiresAt.IsZero() || duration <= 0 || !windowStart.Before(expiresAt) {
		return false
	}
	return hasFiniteOuterLimit || !windowStart.Add(duration).After(expiresAt)
}

// SubscriptionAvailableAmount selects the smallest nonnil remainder, using
// unlimitedAmount when every period is absent. Nil represents a disabled or
// unlimited period, so it contributes no capacity constraint. Allocation
// supplies its remaining billed request amount as unlimitedAmount, allowing an
// entirely unlimited subscription to cover that request.
//
// The helper compares supplied floating-point values directly and does not
// clamp negative inputs or validate finiteness. For finite limits and usage,
// RemainingWindowAmount supplies clamped remainders before selection. The
// selected scalar is independent of caller pointer storage, and no quota usage
// is changed here.
func SubscriptionAvailableAmount(unlimitedAmount float64, values ...*float64) float64 {
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
	if !found {
		// No configured remainder constrained this row, so the allocation caller's
		// remaining billed amount supplies its capacity instead of the display
		// helper's zero sentinel.
		return unlimitedAmount
	}
	return min
}

// SubscriptionAllocationRequest returns the amount basis and whether ordered
// subscription lookup is needed. Nil or nonpositive amounts return zero with
// no lookup; positive base amount wins over billable amount, and balance mode
// returns that amount without subscription selection.
//
// A positive subscription-mode request requires a positive preferred ID before
// lookup. That validation follows the empty-amount return, so empty commands
// do not fail for a missing preferred ID. Other modes request lookup, leaving
// eligible row selection to storage; the result itself applies no debit or
// quota update.
func SubscriptionAllocationRequest(cmd *UsageBillingCommand) (amountUSD float64, query bool, err error) {
	if cmd == nil {
		return 0, false, nil
	}
	amountUSD = cmd.BillableAmountUSD
	if UsesBaseAmount(cmd) {
		amountUSD = cmd.BaseAmountUSD
	}
	if amountUSD <= 0 {
		return 0, false, nil
	}
	if cmd.APIKeyBillingMode == APIKeyBillingModeBalance {
		return amountUSD, false, nil
	}
	if cmd.APIKeyBillingMode == APIKeyBillingModeSubscription && (cmd.PreferredSubscriptionID == nil || *cmd.PreferredSubscriptionID <= 0) {
		return 0, false, ErrPreferredSubscriptionInvalid
	}

	return amountUSD, true, nil
}

// AllocateSubscriptions calculates funding and quota updates in the supplied
// eligible row order. The storage caller provides locked rows with ownership,
// time, status, group and preferred-ID selection already applied; this
// function neither filters nor sorts those rows. Callers supply finite amounts
// and rates; this helper uses ordinary floating-point comparisons. A positive
// subscription-mode
// request with no rows returns preferred-insufficient, while balance or empty
// requests return their uncovered amount directly.
//
// Each row is normalized before its smallest enabled remainder caps
// allocation. In base mode, the selected rate converts remaining base USD into
// billed quota USD, then divides the allocated billed amount back into covered
// base USD. Every positive-limit counter receives that same billed amount,
// because period quotas overlap rather than represent independent spending. A
// zero selected rate clears the remaining base request and ends without an
// allocation or update for that row.
//
// Only positively allocated rows enter Updates and Allocations, preserving
// their order and IDs. IncludeAllocationPricing retains each coverage basis
// and multiplier for later task settlement. The result can retain an uncovered
// amount even in subscription mode; ordinary completion or strict reservation
// policy is enforced by the transaction caller after this calculation.
func AllocateSubscriptions(cmd *UsageBillingCommand, subscriptions []SettlementSubscription, now time.Time) (SubscriptionAllocationPlan, error) {
	amountUSD, query, err := SubscriptionAllocationRequest(cmd)
	if err != nil {
		return SubscriptionAllocationPlan{}, err
	}
	if !query {
		return SubscriptionAllocationPlan{Remaining: amountUSD}, nil
	}
	updates := make([]SettlementSubscription, 0, len(subscriptions))
	// An empty preferred selection cannot fund the positive request. Return
	// insufficiency here so the caller does not reinterpret a missing preferred
	// grant as automatic balance funding.
	if cmd.APIKeyBillingMode == APIKeyBillingModeSubscription && len(subscriptions) == 0 {
		return SubscriptionAllocationPlan{}, ErrPreferredSubscriptionInsufficient
	}

	remaining := amountUSD
	subscriptionAmount := 0.0
	allocations := make([]BillingAllocation, 0, len(subscriptions))

	for _, row := range subscriptions {
		if remaining <= 0 {
			break
		}

		row = NormalizeSettlementSubscription(row, now)

		rateMultiplier := 1.0
		if UsesBaseAmount(cmd) {
			if cmd.DisablePlanGroupRateMultiplier {
				rateMultiplier = NonNegativeRate(cmd.SubscriptionRateMultiplier)
			} else {
				rateMultiplier = SettlementSubscriptionRateMultiplier(row, cmd.GroupID, cmd.SubscriptionRateMultiplier, cmd.SubscriptionRateMultiplierScale)
			}
			// A zero rate consumes this row's remaining base coverage as free usage.
			// Finish selection before appending counters or funding records for that
			// row.
			if rateMultiplier <= 0 {
				remaining = 0
				break
			}
		}
		remainingBillable := remaining
		if UsesBaseAmount(cmd) {
			// Translate uncovered base USD into this grant's billed quota basis before
			// comparing it with window capacity.
			remainingBillable = remaining * rateMultiplier
		}

		available := SubscriptionAvailableAmount(
			remainingBillable,
			RemainingWindowAmount(row.DailyLimitUSD, row.DailyUsageUSD),
			RemainingWindowAmount(row.WeeklyLimitUSD, row.WeeklyUsageUSD),
			RemainingWindowAmount(row.MonthlyLimitUSD, row.MonthlyUsageUSD),
		)
		if available <= 0 {
			continue
		}

		allocated := math.Min(remainingBillable, available)
		if allocated <= 0 {
			continue
		}
		coveredBaseAmount := allocated
		if UsesBaseAmount(cmd) {
			// Convert allocated quota dollars back to covered base dollars before
			// reducing the request remainder, keeping later grants on the same base
			// basis.
			coveredBaseAmount = allocated / rateMultiplier
		}

		// Day, week and month quotas overlap for this charge. Increment every
		// positive-limit counter by the same allocation while leaving
		// unlimited-period counters as observed.
		if row.DailyLimitUSD != nil && *row.DailyLimitUSD > 0 {
			row.DailyUsageUSD += allocated
		}
		if row.WeeklyLimitUSD != nil && *row.WeeklyLimitUSD > 0 {
			row.WeeklyUsageUSD += allocated
		}
		if row.MonthlyLimitUSD != nil && *row.MonthlyLimitUSD > 0 {
			row.MonthlyUsageUSD += allocated
		}

		updates = append(updates, row)

		// Capture this row's grant and plan IDs in per-allocation storage so each
		// returned funding record retains its own selected identity after iteration
		// advances.
		subscriptionID := row.ID
		planID := row.PlanID
		allocation := BillingAllocation{
			Type:           BillingAllocationTypeSubscription,
			AmountUSD:      allocated,
			SubscriptionID: &subscriptionID,
			PlanID:         &planID,
		}
		if cmd.IncludeAllocationPricing {
			allocation.BaseAmountUSD = coveredBaseAmount
			allocation.RateMultiplier = rateMultiplier
		}
		allocations = append(allocations, allocation)
		subscriptionAmount += allocated
		remaining -= coveredBaseAmount
	}

	return SubscriptionAllocationPlan{Remaining: remaining, SubscriptionAmount: subscriptionAmount, Allocations: allocations, Updates: updates}, nil
}
