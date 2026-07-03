package service

import "time"

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
	if s.IsExpired() {
		return 0
	}
	return int(time.Until(s.ExpiresAt).Hours() / 24)
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

// WindowActivationAt 返回当前可首次激活的额度窗口；到期尾段不足完整周期的窗口保持未激活。
func (s *UserSubscription) WindowActivationAt(now time.Time) SubscriptionWindowActivation {
	var activation SubscriptionWindowActivation
	if s == nil || !s.HasQuotaLimit() {
		return activation
	}

	windowStart := startOfDay(now)
	if positiveSubscriptionLimit(s.DailyLimitUSD) && s.DailyWindowStart == nil {
		activation.Daily = s.HasOneTimeDailyQuota() ||
			s.CanStartFullQuotaWindow(windowStart, subscriptionDailyWindow)
	}
	if positiveSubscriptionLimit(s.WeeklyLimitUSD) && s.WeeklyWindowStart == nil {
		activation.Weekly = s.CanStartFullQuotaWindow(windowStart, subscriptionWeeklyWindow)
	}
	if positiveSubscriptionLimit(s.MonthlyLimitUSD) && s.MonthlyWindowStart == nil {
		activation.Monthly = s.CanStartFullQuotaWindow(windowStart, subscriptionMonthlyWindow)
	}
	return activation
}

func (s *UserSubscription) NeedsDailyReset() bool {
	return s.NeedsDailyResetAt(time.Now())
}

func (s *UserSubscription) NeedsDailyResetAt(now time.Time) bool {
	if s.DailyWindowStart == nil {
		return false
	}
	if s.HasOneTimeDailyQuota() {
		return false
	}
	if now.Before(s.DailyWindowStart.Add(subscriptionDailyWindow)) {
		return false
	}
	// 到期尾段不足一个完整日窗口时不再重置，避免套餐结束前额外刷新一次额度。
	return s.CanStartFullQuotaWindow(startOfDay(now), subscriptionDailyWindow)
}

func (s *UserSubscription) NeedsWeeklyReset() bool {
	return s.NeedsWeeklyResetAt(time.Now())
}

func (s *UserSubscription) NeedsWeeklyResetAt(now time.Time) bool {
	if s.WeeklyWindowStart == nil {
		return false
	}
	if now.Before(s.WeeklyWindowStart.Add(subscriptionWeeklyWindow)) {
		return false
	}
	// 到期尾段不足一个完整周窗口时不再重置，避免最终几天额外刷新周额度。
	return s.CanStartFullQuotaWindow(startOfDay(now), subscriptionWeeklyWindow)
}

func (s *UserSubscription) NeedsMonthlyReset() bool {
	return s.NeedsMonthlyResetAt(time.Now())
}

func (s *UserSubscription) NeedsMonthlyResetAt(now time.Time) bool {
	if s.MonthlyWindowStart == nil {
		return false
	}
	if now.Before(s.MonthlyWindowStart.Add(subscriptionMonthlyWindow)) {
		return false
	}
	// 到期尾段不足一个完整月窗口时不再重置，避免 30 天套餐到期瞬间刷新月额度。
	return s.CanStartFullQuotaWindow(startOfDay(now), subscriptionMonthlyWindow)
}

// CanStartFullQuotaWindow 判断从指定起点开始是否还能覆盖一个完整额度窗口。
func (s *UserSubscription) CanStartFullQuotaWindow(windowStart time.Time, duration time.Duration) bool {
	if s == nil || s.ExpiresAt.IsZero() {
		return false
	}
	return !windowStart.Add(duration).After(s.ExpiresAt)
}

func (s *UserSubscription) DailyResetTime() *time.Time {
	if s.DailyWindowStart == nil {
		return nil
	}
	if s.HasOneTimeDailyQuota() {
		t := s.ExpiresAt
		return &t
	}
	t := s.DailyWindowStart.Add(subscriptionDailyWindow)
	if !s.CanStartFullQuotaWindow(t, subscriptionDailyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.WeeklyWindowStart.Add(subscriptionWeeklyWindow)
	if !s.CanStartFullQuotaWindow(t, subscriptionWeeklyWindow) {
		t = s.ExpiresAt
	}
	return &t
}

func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.MonthlyWindowStart.Add(subscriptionMonthlyWindow)
	if !s.CanStartFullQuotaWindow(t, subscriptionMonthlyWindow) {
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
