package billing

import (
	"math"
	"time"
)

type SettlementSubscription struct {
	ID                       int64
	PlanID                   int64
	StartsAt                 time.Time
	ExpiresAt                time.Time
	DailyWindowStart         *time.Time
	WeeklyWindowStart        *time.Time
	MonthlyWindowStart       *time.Time
	DailyLimitUSD            *float64
	WeeklyLimitUSD           *float64
	MonthlyLimitUSD          *float64
	DailyUsageUSD            float64
	WeeklyUsageUSD           float64
	MonthlyUsageUSD          float64
	PlanGroupRateMultipliers map[int64]float64
}

func UsesBaseAmount(cmd *UsageBillingCommand) bool {
	return cmd != nil && cmd.BaseAmountUSD > 0
}

func NonNegativeRate(rate float64) float64 {
	if rate < 0 {
		return 0
	}
	return rate
}

func SettlementSubscriptionRateMultiplier(row SettlementSubscription, groupID *int64, defaultRate, scale float64) float64 {
	rate := defaultRate
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

// @project-doc docs/domains/payments_and_entitlements.md#subscription_quota_windows
// NormalizeSettlementSubscription 统一解析倍率与事务扣费看到的额度窗口状态。
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

func NormalizeSettlementWindow(
	windowStart *time.Time,
	limit *float64,
	used float64,
	resetStart time.Time,
	duration time.Duration,
	now, startsAt, expiresAt time.Time,
	hasFiniteOuterLimit bool,
) (*time.Time, float64) {
	if limit == nil || *limit <= 0 {
		if windowStart == nil {
			return nil, used
		}
		start := *windowStart
		return &start, used
	}

	// 1 日卡是一次性日额度：首次扣费要记录窗口，但跨过 24 小时边界后不能清零。
	if duration == 24*time.Hour && !expiresAt.After(startsAt.AddDate(0, 0, 1)) {
		if windowStart == nil || windowStart.IsZero() {
			start := resetStart
			return &start, 0
		}
		start := *windowStart
		return &start, used
	}

	// 没有有限外层额度保护时，尾段仍须容纳完整窗口，避免最高层额度重复发放。
	if !CanStartSettlementWindow(resetStart, duration, expiresAt, hasFiniteOuterLimit) {
		if windowStart == nil || windowStart.IsZero() {
			return nil, used
		}
		start := *windowStart
		return &start, used
	}

	if windowStart == nil || windowStart.IsZero() || !windowStart.Add(duration).After(now) {
		start := resetStart
		return &start, 0
	}
	start := *windowStart
	return &start, used
}

func CanStartSettlementWindow(windowStart time.Time, duration time.Duration, expiresAt time.Time, hasFiniteOuterLimit bool) bool {
	if expiresAt.IsZero() || duration <= 0 || !windowStart.Before(expiresAt) {
		return false
	}
	return hasFiniteOuterLimit || !windowStart.Add(duration).After(expiresAt)
}

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
		// nil 表示该窗口无限额；所有窗口都无限时，本次剩余费用都由订阅覆盖。
		return unlimitedAmount
	}
	return min
}

// SubscriptionAllocationRequest 保持原校验顺序，允许 Adapter 跳过不需要的订阅查询。
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

// SubscriptionAllocationPlan 按锁定后的稳定顺序给出更新和资金分配，不执行 I/O。
type SubscriptionAllocationPlan struct {
	Remaining, SubscriptionAmount float64
	Allocations                   []BillingAllocation
	Updates                       []SettlementSubscription
}

func AllocateSubscriptions(cmd *UsageBillingCommand, subscriptions []SettlementSubscription, now time.Time) (SubscriptionAllocationPlan, error) {
	amountUSD, query, err := SubscriptionAllocationRequest(cmd)
	if err != nil {
		return SubscriptionAllocationPlan{}, err
	}
	if !query {
		return SubscriptionAllocationPlan{Remaining: amountUSD}, nil
	}
	updates := make([]SettlementSubscription, 0, len(subscriptions))
	// 指定订阅不存在、失效或不覆盖最终分组时不能把整笔请求伪装成余额回退。
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
			if rateMultiplier <= 0 {
				remaining = 0
				break
			}
		}
		remainingBillable := remaining
		if UsesBaseAmount(cmd) {
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
			coveredBaseAmount = allocated / rateMultiplier
		}

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
