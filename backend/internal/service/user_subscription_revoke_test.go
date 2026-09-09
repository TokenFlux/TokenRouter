//go:build unit

package service

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type revokeSubscriptionRepoStub struct {
	*subscriptionUserSubRepoStub
}

type resettingRevokeSubscriptionRepoStub struct {
	*revokeSubscriptionRepoStub
}

func (r *revokeSubscriptionRepoStub) Delete(_ context.Context, id int64) error {
	if _, ok := r.byID[id]; !ok {
		return ErrSubscriptionNotFound
	}
	delete(r.byID, id)
	r.rebuildIndex()
	return nil
}

func (r *resettingRevokeSubscriptionRepoStub) ResetMonthlyUsage(_ context.Context, id int64, _ *time.Time, newWindowStart time.Time) error {
	sub := r.byID[id]
	if sub == nil {
		return ErrSubscriptionNotFound
	}
	sub.MonthlyUsageUSD = 0
	sub.MonthlyWindowStart = &newWindowStart
	return nil
}

func revokeSubscriptionFixture() *UserSubscription {
	now := time.Now().UTC()
	// 默认夹具使用当前日窗口，避免零点后一小时内意外触发额度重置。
	windowStart := startOfDay(now)
	return &UserSubscription{
		ID:                 1,
		UserID:             7,
		PlanID:             10,
		StartsAt:           now.Add(-2 * time.Hour),
		ExpiresAt:          now.Add(24 * time.Hour),
		Status:             SubscriptionStatusActive,
		DailyWindowStart:   &windowStart,
		WeeklyWindowStart:  &windowStart,
		MonthlyWindowStart: &windowStart,
	}
}

func quotaPointer(value float64) *float64 {
	return &value
}

func TestUserSubscriptionHighestQuotaExhaustedUsesHighestConfiguredWindow(t *testing.T) {
	tests := []struct {
		name       string
		monthly    *float64
		monthlyUse float64
		weekly     *float64
		weeklyUse  float64
		daily      *float64
		dailyUse   float64
		want       bool
	}{
		{name: "monthly exhausted", monthly: quotaPointer(100), monthlyUse: 100, weekly: quotaPointer(10), weeklyUse: 10, daily: quotaPointer(1), dailyUse: 1, want: true},
		{name: "monthly available blocks lower exhausted windows", monthly: quotaPointer(100), monthlyUse: 99, weekly: quotaPointer(10), weeklyUse: 10, daily: quotaPointer(1), dailyUse: 1, want: false},
		{name: "weekly exhausted when monthly absent", weekly: quotaPointer(10), weeklyUse: 10, daily: quotaPointer(1), dailyUse: 1, want: true},
		{name: "weekly available blocks daily exhausted", weekly: quotaPointer(10), weeklyUse: 9, daily: quotaPointer(1), dailyUse: 1, want: false},
		{name: "daily exhausted when it is the only limit", daily: quotaPointer(1), dailyUse: 1, want: true},
		{name: "unlimited has no revocable quota", daily: nil, want: false},
		{name: "non-finite quota is not revocable", monthly: quotaPointer(math.Inf(1)), monthlyUse: math.Inf(1), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub := revokeSubscriptionFixture()
			sub.MonthlyLimitUSD, sub.MonthlyUsageUSD = tt.monthly, tt.monthlyUse
			sub.WeeklyLimitUSD, sub.WeeklyUsageUSD = tt.weekly, tt.weeklyUse
			sub.DailyLimitUSD, sub.DailyUsageUSD = tt.daily, tt.dailyUse
			require.Equal(t, tt.want, sub.HighestQuotaExhausted())
		})
	}
}

func TestRevokeOwnExhaustedSubscriptionRejectsNonEligibleSubscriptions(t *testing.T) {
	for _, tt := range []struct {
		name   string
		user   int64
		mutate func(*UserSubscription)
		want   error
	}{
		{name: "foreign subscription", user: 99, want: ErrSubscriptionNotFound},
		{name: "inactive subscription", user: 7, mutate: func(sub *UserSubscription) {
			sub.Status = SubscriptionStatusPending
			sub.StartsAt = time.Now().Add(time.Hour)
		}, want: ErrSubscriptionNotActive},
		{name: "already revoked subscription", user: 7, mutate: func(sub *UserSubscription) {
			revokedAt := time.Now().Add(-time.Minute)
			sub.DeletedAt = &revokedAt
		}, want: ErrSubscriptionNotActive},
		{name: "quota still available", user: 7, mutate: func(sub *UserSubscription) { sub.MonthlyLimitUSD = quotaPointer(100); sub.MonthlyUsageUSD = 99 }, want: ErrSubscriptionQuotaAvailable},
		{name: "unlimited subscription", user: 7, want: ErrSubscriptionQuotaAvailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &revokeSubscriptionRepoStub{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
			sub := revokeSubscriptionFixture()
			if tt.mutate != nil {
				tt.mutate(sub)
			}
			repo.seed(sub)
			svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

			_, err := svc.RevokeOwnExhaustedSubscription(context.Background(), tt.user, sub.ID)
			require.ErrorIs(t, err, tt.want)
			require.Contains(t, repo.byID, sub.ID)
		})
	}
}

func TestRevokeOwnExhaustedSubscriptionAdvancesQueuedPack(t *testing.T) {
	repo := &revokeSubscriptionRepoStub{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()}
	active := revokeSubscriptionFixture()
	active.MonthlyLimitUSD = quotaPointer(10)
	active.MonthlyUsageUSD = 10
	pending := revokeSubscriptionFixture()
	pending.ID = 2
	pending.StartsAt = active.ExpiresAt
	pending.ExpiresAt = active.ExpiresAt.Add(24 * time.Hour)
	pending.Status = SubscriptionStatusPending
	pending.MonthlyLimitUSD = active.MonthlyLimitUSD
	later := revokeSubscriptionFixture()
	later.ID = 3
	later.StartsAt = pending.ExpiresAt
	later.ExpiresAt = pending.ExpiresAt.Add(24 * time.Hour)
	later.Status = SubscriptionStatusPending
	later.MonthlyLimitUSD = active.MonthlyLimitUSD
	repo.seed(active)
	repo.seed(pending)
	repo.seed(later)
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

	result, err := svc.RevokeOwnExhaustedSubscription(context.Background(), active.UserID, active.ID)
	require.NoError(t, err)
	require.Equal(t, active.ID, result.RevokedSubscriptionID)
	require.NotNil(t, result.ReplacementSubscriptionID)
	require.Equal(t, pending.ID, *result.ReplacementSubscriptionID)
	require.NotContains(t, repo.byID, active.ID)

	advanced := repo.byID[pending.ID]
	require.NotNil(t, advanced)
	require.Equal(t, SubscriptionStatusActive, advanced.Status)
	require.WithinDuration(t, time.Now(), advanced.StartsAt, 2*time.Second)
	advancedLater := repo.byID[later.ID]
	require.NotNil(t, advancedLater)
	require.Equal(t, SubscriptionStatusPending, advancedLater.Status)
	require.Equal(t, advanced.ExpiresAt, advancedLater.StartsAt)
}

func TestRevokeOwnExhaustedSubscriptionRechecksResetQuota(t *testing.T) {
	repo := &resettingRevokeSubscriptionRepoStub{
		revokeSubscriptionRepoStub: &revokeSubscriptionRepoStub{subscriptionUserSubRepoStub: newSubscriptionUserSubRepoStub()},
	}
	sub := revokeSubscriptionFixture()
	sub.ExpiresAt = time.Now().Add(60 * 24 * time.Hour)
	sub.MonthlyLimitUSD = quotaPointer(10)
	sub.MonthlyUsageUSD = 10
	windowStart := time.Now().Add(-31 * 24 * time.Hour)
	sub.MonthlyWindowStart = &windowStart
	repo.seed(sub)
	svc := NewSubscriptionService(groupRepoNoop{}, repo, nil, nil, nil)

	_, err := svc.RevokeOwnExhaustedSubscription(context.Background(), sub.UserID, sub.ID)
	require.ErrorIs(t, err, ErrSubscriptionQuotaAvailable)
	require.Contains(t, repo.byID, sub.ID)
	require.Zero(t, repo.byID[sub.ID].MonthlyUsageUSD)
}
