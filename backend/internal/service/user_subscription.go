package service

import "time"

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

	User           *User
	Plan           *SubscriptionPlan
	AssignedByUser *User
}

func (s *UserSubscription) IsActive() bool {
	now := time.Now()
	return s.Status == SubscriptionStatusActive && !now.Before(s.StartsAt) && now.Before(s.ExpiresAt)
}

func (s *UserSubscription) IsPending() bool {
	if s == nil {
		return false
	}
	return s.Status == SubscriptionStatusPending && time.Now().Before(s.StartsAt)
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

func (s *UserSubscription) NeedsDailyReset() bool {
	if s.DailyWindowStart == nil {
		return false
	}
	return time.Since(*s.DailyWindowStart) >= 24*time.Hour
}

func (s *UserSubscription) NeedsWeeklyReset() bool {
	if s.WeeklyWindowStart == nil {
		return false
	}
	return time.Since(*s.WeeklyWindowStart) >= 7*24*time.Hour
}

func (s *UserSubscription) NeedsMonthlyReset() bool {
	if s.MonthlyWindowStart == nil {
		return false
	}
	return time.Since(*s.MonthlyWindowStart) >= 30*24*time.Hour
}

func (s *UserSubscription) DailyResetTime() *time.Time {
	if s.DailyWindowStart == nil {
		return nil
	}
	t := s.DailyWindowStart.Add(24 * time.Hour)
	return &t
}

func (s *UserSubscription) WeeklyResetTime() *time.Time {
	if s.WeeklyWindowStart == nil {
		return nil
	}
	t := s.WeeklyWindowStart.Add(7 * 24 * time.Hour)
	return &t
}

func (s *UserSubscription) MonthlyResetTime() *time.Time {
	if s.MonthlyWindowStart == nil {
		return nil
	}
	t := s.MonthlyWindowStart.Add(30 * 24 * time.Hour)
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
