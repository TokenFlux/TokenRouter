//go:build unit

package billing_test

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

type revokeSubscriptionRepoStub struct {
	*billingtestkit.SubscriptionRepository
}

type resettingRevokeSubscriptionRepoStub struct {
	*revokeSubscriptionRepoStub
}

func (r *revokeSubscriptionRepoStub) Delete(_ context.Context, id int64) error {
	if _, ok := r.ByID[id]; !ok {
		return billing.ErrSubscriptionNotFound
	}
	delete(r.ByID, id)
	r.RebuildIndex()
	return nil
}

func (r *resettingRevokeSubscriptionRepoStub) ResetMonthlyUsage(_ context.Context, id int64, _ *time.Time, newWindowStart time.Time) error {
	sub := r.ByID[id]
	if sub == nil {
		return billing.ErrSubscriptionNotFound
	}
	sub.MonthlyUsageUSD = 0
	sub.MonthlyWindowStart = &newWindowStart
	return nil
}

func revokeSubscriptionFixture() *billing.UserSubscription {
	now := time.Now().UTC()
	// 默认夹具使用当前日窗口，避免零点后一小时内意外触发额度重置。
	windowStart := timezone.NewCalendar(now.Location()).StartOfDay(now)
	return &billing.UserSubscription{
		ID:                 1,
		UserID:             7,
		PlanID:             10,
		StartsAt:           now.Add(-2 * time.Hour),
		ExpiresAt:          now.Add(24 * time.Hour),
		Status:             billing.SubscriptionStatusActive,
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
		mutate func(*billing.UserSubscription)
		want   error
	}{
		{name: "foreign subscription", user: 99, want: billing.ErrSubscriptionNotFound},
		{name: "inactive subscription", user: 7, mutate: func(sub *billing.UserSubscription) {
			sub.Status = billing.SubscriptionStatusPending
			sub.StartsAt = time.Now().UTC().Add(time.Hour)
		}, want: billing.ErrSubscriptionNotActive},
		{name: "already revoked subscription", user: 7, mutate: func(sub *billing.UserSubscription) {
			revokedAt := time.Now().UTC().Add(-time.Minute)
			sub.DeletedAt = &revokedAt
		}, want: billing.ErrSubscriptionNotActive},
		{name: "quota still available", user: 7, mutate: func(sub *billing.UserSubscription) {
			sub.MonthlyLimitUSD = quotaPointer(100)
			sub.MonthlyUsageUSD = 99
		}, want: billing.ErrSubscriptionQuotaAvailable},
		{name: "unlimited subscription", user: 7, want: billing.ErrSubscriptionQuotaAvailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &revokeSubscriptionRepoStub{SubscriptionRepository: billingtestkit.NewSubscriptionRepository()}
			sub := revokeSubscriptionFixture()
			if tt.mutate != nil {
				tt.mutate(sub)
			}
			repo.Seed(sub)
			svc := newOriginalSubscriptionService(repo)

			_, err := svc.RevokeOwnExhaustedSubscription(context.Background(), tt.user, sub.ID)
			require.ErrorIs(t, err, tt.want)
			require.Contains(t, repo.ByID, sub.ID)
		})
	}
}

func TestRevokeOwnExhaustedSubscriptionAdvancesQueuedPack(t *testing.T) {
	repo := &revokeSubscriptionRepoStub{SubscriptionRepository: billingtestkit.NewSubscriptionRepository()}
	active := revokeSubscriptionFixture()
	active.MonthlyLimitUSD = quotaPointer(10)
	active.MonthlyUsageUSD = 10
	pending := revokeSubscriptionFixture()
	pending.ID = 2
	pending.StartsAt = active.ExpiresAt
	pending.ExpiresAt = active.ExpiresAt.Add(24 * time.Hour)
	pending.Status = billing.SubscriptionStatusPending
	pending.MonthlyLimitUSD = active.MonthlyLimitUSD
	later := revokeSubscriptionFixture()
	later.ID = 3
	later.StartsAt = pending.ExpiresAt
	later.ExpiresAt = pending.ExpiresAt.Add(24 * time.Hour)
	later.Status = billing.SubscriptionStatusPending
	later.MonthlyLimitUSD = active.MonthlyLimitUSD
	repo.Seed(active)
	repo.Seed(pending)
	repo.Seed(later)
	svc := newOriginalSubscriptionService(repo)

	result, err := svc.RevokeOwnExhaustedSubscription(context.Background(), active.UserID, active.ID)
	require.NoError(t, err)
	require.Equal(t, active.ID, result.RevokedSubscriptionID)
	require.NotNil(t, result.ReplacementSubscriptionID)
	require.Equal(t, pending.ID, *result.ReplacementSubscriptionID)
	require.NotContains(t, repo.ByID, active.ID)

	advanced := repo.ByID[pending.ID]
	require.NotNil(t, advanced)
	require.Equal(t, billing.SubscriptionStatusActive, advanced.Status)
	require.WithinDuration(t, time.Now().UTC(), advanced.StartsAt, 2*time.Second)
	advancedLater := repo.ByID[later.ID]
	require.NotNil(t, advancedLater)
	require.Equal(t, billing.SubscriptionStatusPending, advancedLater.Status)
	require.Equal(t, advanced.ExpiresAt, advancedLater.StartsAt)
}

func TestRevokeOwnExhaustedSubscriptionRechecksResetQuota(t *testing.T) {
	repo := &resettingRevokeSubscriptionRepoStub{
		revokeSubscriptionRepoStub: &revokeSubscriptionRepoStub{SubscriptionRepository: billingtestkit.NewSubscriptionRepository()},
	}
	sub := revokeSubscriptionFixture()
	sub.ExpiresAt = time.Now().UTC().Add(60 * 24 * time.Hour)
	sub.MonthlyLimitUSD = quotaPointer(10)
	sub.MonthlyUsageUSD = 10
	windowStart := time.Now().UTC().Add(-31 * 24 * time.Hour)
	sub.MonthlyWindowStart = &windowStart
	repo.Seed(sub)
	svc := newOriginalSubscriptionService(repo)

	_, err := svc.RevokeOwnExhaustedSubscription(context.Background(), sub.UserID, sub.ID)
	require.ErrorIs(t, err, billing.ErrSubscriptionQuotaAvailable)
	require.Contains(t, repo.ByID, sub.ID)
	require.Zero(t, repo.ByID[sub.ID].MonthlyUsageUSD)
}
