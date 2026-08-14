package service

import (
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/timezone"
)

const (
	subscriptionDailyWindow   = 24 * time.Hour
	subscriptionWeeklyWindow  = 7 * 24 * time.Hour
	subscriptionMonthlyWindow = 30 * 24 * time.Hour
)

type UserSubscription struct {
	ID     int64
	UserID int64
	PlanID int64

	StartsAt  time.Time
	ExpiresAt time.Time
	Status    string

	DailyWindowStart   *time.Time
	WeeklyWindowStart  *time.Time
	MonthlyWindowStart *time.Time

	DailyLimitUSD   *float64
	WeeklyLimitUSD  *float64
	MonthlyLimitUSD *float64

	DailyUsageUSD   float64
	WeeklyUsageUSD  float64
	MonthlyUsageUSD float64

	AssignedBy    *int64
	AssignedAt    time.Time
	SourceOrderID *int64
	Notes         string

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt *time.Time

	User           *User
	Plan           *SubscriptionPlan
	AssignedByUser *User
}

// SubscriptionWindowActivation 表示本次维护允许首次激活的订阅额度窗口。
type SubscriptionWindowActivation struct {
	Daily   bool
	Weekly  bool
	Monthly bool
}

func (a SubscriptionWindowActivation) Any() bool {
	return a.Daily || a.Weekly || a.Monthly
}

func (s *UserSubscription) IsActive() bool {
	now := time.Now()
	return s.DeletedAt == nil && s.Status == SubscriptionStatusActive && !now.Before(s.StartsAt) && now.Before(s.ExpiresAt)
}

func (s *UserSubscription) IsPending() bool {
	if s == nil {
		return false
	}
	return s.DeletedAt == nil && s.Status == SubscriptionStatusPending && time.Now().Before(s.StartsAt)
}

func (s *UserSubscription) IsEffective() bool {
	if s == nil {
		return false
	}
	now := time.Now()
	return !now.Before(s.StartsAt) && now.Before(s.ExpiresAt) && s.EffectiveStatus(now) == SubscriptionStatusActive
}

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

func (s *UserSubscription) IsExpired() bool {
	return !s.ExpiresAt.After(time.Now())
}

func (s *UserSubscription) DaysRemaining() int {
	return s.daysRemainingAt(time.Now())
}

func (s *UserSubscription) daysRemainingAt(now time.Time) int {
	remaining := s.ExpiresAt.Sub(now)
	if remaining <= 0 {
		return 0
	}

	days := int(remaining / subscriptionDailyWindow)
	if remaining%subscriptionDailyWindow != 0 {
		days++
	}
	return days
}

func (s *UserSubscription) IsWindowActivated() bool {
	return s.DailyWindowStart != nil || s.WeeklyWindowStart != nil || s.MonthlyWindowStart != nil
}

func (s *UserSubscription) HasQuotaLimit() bool {
	return positiveSubscriptionLimit(s.DailyLimitUSD) ||
		positiveSubscriptionLimit(s.WeeklyLimitUSD) ||
		positiveSubscriptionLimit(s.MonthlyLimitUSD)
}

func (s *UserSubscription) HasOneTimeDailyQuota() bool {
	if s == nil || s.StartsAt.IsZero() || s.ExpiresAt.IsZero() {
		return false
	}
	return !s.ExpiresAt.After(s.StartsAt.AddDate(0, 0, 1))
}

func (s *UserSubscription) NeedsWindowActivationAt(now time.Time) bool {
	return s.WindowActivationAt(now).Any()
}

// WindowActivationAt 返回当前可首次激活的额度窗口；有限外层额度允许低层窗口在到期尾段激活。
func (s *UserSubscription) WindowActivationAt(now time.Time) SubscriptionWindowActivation {
	var activation SubscriptionWindowActivation
	if s == nil || !s.HasQuotaLimit() || now.Before(s.StartsAt) || !now.Before(s.ExpiresAt) {
		return activation
	}

	windowStart := startOfDay(now)
	if positiveSubscriptionLimit(s.DailyLimitUSD) && s.DailyWindowStart == nil {
		activation.Daily = s.HasOneTimeDailyQuota() ||
			s.canStartQuotaWindow(windowStart, subscriptionDailyWindow)
	}
	if positiveSubscriptionLimit(s.WeeklyLimitUSD) && s.WeeklyWindowStart == nil {
		activation.Weekly = s.canStartQuotaWindow(windowStart, subscriptionWeeklyWindow)
	}
	if positiveSubscriptionLimit(s.MonthlyLimitUSD) && s.MonthlyWindowStart == nil {
		activation.Monthly = s.canStartQuotaWindow(windowStart, subscriptionMonthlyWindow)
	}
	return activation
}

func (s *UserSubscription) NeedsDailyReset() bool {
	return s.NeedsDailyResetAt(time.Now())
}

func (s *UserSubscription) NeedsDailyResetAt(now time.Time) bool {
	_, ok := s.automaticDailyWindowStartAt(now)
	return ok
}

// automaticDailyWindowStartAt 计算按项目时区日历日对齐的日窗口起点。
// 历史非零点锚点会在下一个零点自愈，1 日卡和到期尾段规则仍保持原语义。
func (s *UserSubscription) automaticDailyWindowStartAt(now time.Time) (time.Time, bool) {
	if s == nil || s.DailyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return time.Time{}, false
	}
	if s.HasOneTimeDailyQuota() {
		return time.Time{}, false
	}
	today := timezone.StartOfDay(now)
	if !today.After(timezone.StartOfDay(*s.DailyWindowStart)) {
		return time.Time{}, false
	}
	if !s.canStartQuotaWindow(today, subscriptionDailyWindow) {
		return time.Time{}, false
	}
	return today, true
}

func (s *UserSubscription) NeedsWeeklyReset() bool {
	return s.NeedsWeeklyResetAt(time.Now())
}

func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
	if s == nil || s.WeeklyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Before(s.WeeklyWindowStart.Add(subscriptionWeeklyWindow)) {
		return false
	}
	return s.canStartQuotaWindow(startOfDay(now), subscriptionWeeklyWindow)
}

func (s *UserSubscription) NeedsMonthlyReset() bool {
	return s.NeedsMonthlyResetAt(time.Now())
}

func (s *UserSubscription) NeedsMonthlyResetAt(now time.Time) bool {
	if s == nil || s.MonthlyWindowStart == nil || s.ExpiresAt.IsZero() || !now.Before(s.ExpiresAt) {
		return false
	}
	if now.Before(s.MonthlyWindowStart.Add(subscriptionMonthlyWindow)) {
		return false
	}
	return s.canStartQuotaWindow(startOfDay(now), subscriptionMonthlyWindow)
}

// CanStartFullQuotaWindow 判断从指定起点开始是否还能覆盖一个完整额度窗口。
func (s *UserSubscription) CanStartFullQuotaWindow(windowStart time.Time, duration time.Duration) bool {
	if s == nil || s.ExpiresAt.IsZero() || duration <= 0 || !windowStart.Before(s.ExpiresAt) {
		return false
	}
	return !windowStart.Add(duration).After(s.ExpiresAt)
}

// @project-doc docs/domains/payments_and_entitlements.md#subscription_quota_windows
// canStartQuotaWindow 判断窗口能否开始；更高层有限额度存在时，它负责约束低层尾段的总消耗。
func (s *UserSubscription) canStartQuotaWindow(windowStart time.Time, duration time.Duration) bool {
	if s == nil || s.ExpiresAt.IsZero() || !windowStart.Before(s.ExpiresAt) {
		return false
	}
	return s.CanStartFullQuotaWindow(windowStart, duration) || s.hasFiniteOuterQuotaLimit(duration)
}

// hasFiniteOuterQuotaLimit 按“日 < 周 < 月”层级判断是否存在正数外层额度；无限额度不能充当尾段保护层。
func (s *UserSubscription) hasFiniteOuterQuotaLimit(duration time.Duration) bool {
	if s == nil {
		return false
	}
	switch duration {
	case subscriptionDailyWindow:
		return positiveSubscriptionLimit(s.WeeklyLimitUSD) || positiveSubscriptionLimit(s.MonthlyLimitUSD)
	case subscriptionWeeklyWindow:
		return positiveSubscriptionLimit(s.MonthlyLimitUSD)
	default:
		return false
	}
}

func (s *UserSubscription) DailyResetTime() *time.Time {
	if s.DailyWindowStart == nil {
		return nil
	}
	if s.HasOneTimeDailyQuota() {
		t := s.ExpiresAt
		return &t
	}
	// 日额度按日历日刷新，旧的非零点锚点也应展示其所在日的下一个零点。
	t := timezone.StartOfDay(*s.DailyWindowStart).AddDate(0, 0, 1)
	if !s.canStartQuotaWindow(t, subscriptionDailyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.WeeklyWindowStart.Add(subscriptionWeeklyWindow)
	if !s.canStartQuotaWindow(t, subscriptionWeeklyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.MonthlyWindowStart.Add(subscriptionMonthlyWindow)
	if !s.canStartQuotaWindow(t, subscriptionMonthlyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

func (s *UserSubscription) CheckDailyLimit(additionalCost float64) bool {
	if s.DailyLimitUSD == nil || *s.DailyLimitUSD <= 0 {
		return true
	}
	return s.DailyUsageUSD+additionalCost <= *s.DailyLimitUSD
}

func (s *UserSubscription) CheckWeeklyLimit(additionalCost float64) bool {
	if s.WeeklyLimitUSD == nil || *s.WeeklyLimitUSD <= 0 {
		return true
	}
	return s.WeeklyUsageUSD+additionalCost <= *s.WeeklyLimitUSD
}

func (s *UserSubscription) CheckMonthlyLimit(additionalCost float64) bool {
	if s.MonthlyLimitUSD == nil || *s.MonthlyLimitUSD <= 0 {
		return true
	}
	return s.MonthlyUsageUSD+additionalCost <= *s.MonthlyLimitUSD
}

func (s *UserSubscription) CheckAllLimits(additionalCost float64) (daily, weekly, monthly bool) {
	daily = s.CheckDailyLimit(additionalCost)
	weekly = s.CheckWeeklyLimit(additionalCost)
	monthly = s.CheckMonthlyLimit(additionalCost)
	return
}

func (s *UserSubscription) RemainingDailyUSD() *float64 {
	return remainingWindowAmount(s.DailyLimitUSD, s.DailyUsageUSD)
}

func (s *UserSubscription) RemainingWeeklyUSD() *float64 {
	return remainingWindowAmount(s.WeeklyLimitUSD, s.WeeklyUsageUSD)
}

func (s *UserSubscription) RemainingMonthlyUSD() *float64 {
	return remainingWindowAmount(s.MonthlyLimitUSD, s.MonthlyUsageUSD)
}

func (s *UserSubscription) AvailableQuotaUSD() float64 {
	return minRemainingWindowAmount(
		s.RemainingDailyUSD(),
		s.RemainingWeeklyUSD(),
		s.RemainingMonthlyUSD(),
	)
}

func remainingWindowAmount(limit *float64, used float64) *float64 {
	if limit == nil || *limit <= 0 {
		return nil
	}
	remaining := *limit - used
	if remaining < 0 {
		remaining = 0
	}
	return &remaining
}

func minRemainingWindowAmount(values ...*float64) float64 {
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
		return 0
	}
	return min
}

func positiveSubscriptionLimit(limit *float64) bool {
	return limit != nil && *limit > 0
}
