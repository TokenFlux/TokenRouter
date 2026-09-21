package billing_test

import (
	"context"
	"testing"
	"time"

	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/stretchr/testify/require"
)

func TestAssignSubscription_SamePlanCreatesPendingChain(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	limit := 10.0
	existing := &billing.UserSubscription{
		ID:        10,
		UserID:    1001,
		PlanID:    1,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(29 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	}
	subRepo.Seed(existing)

	svc := newOriginalSubscriptionService(subRepo)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &billing.AssignSubscriptionInput{
		UserID:        1001,
		PlanID:        1,
		ValidityDays:  30,
		DailyLimitUSD: &limit,
		Notes:         "renew",
	})
	require.NoError(t, err)
	require.True(t, queued)
	require.Equal(t, billing.SubscriptionStatusPending, created.Status)
	require.Equal(t, existing.ExpiresAt, created.StartsAt)
	require.Equal(t, existing.ExpiresAt.AddDate(0, 0, 30), created.ExpiresAt)
	require.Equal(t, 1, subRepo.CreateCalls)
}

func TestAssignSubscription_DifferentPlanStartsImmediately(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	limit := 10.0
	subRepo.Seed(&billing.UserSubscription{
		ID:        11,
		UserID:    1001,
		PlanID:    1,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(29 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	})

	svc := newOriginalSubscriptionService(subRepo)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &billing.AssignSubscriptionInput{
		UserID:        1001,
		PlanID:        2,
		ValidityDays:  7,
		DailyLimitUSD: &limit,
	})
	require.NoError(t, err)
	require.False(t, queued)
	require.Equal(t, billing.SubscriptionStatusActive, created.Status)
	require.WithinDuration(t, time.Now().UTC(), created.StartsAt, 2*time.Second)
	require.Equal(t, created.StartsAt.AddDate(0, 0, 7), created.ExpiresAt)
}

func TestAssignSubscription_ReusesExistingSourceOrderSubscription(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	sourceOrderID := int64(7788)
	limit := 18.0
	existing := &billing.UserSubscription{
		ID:            21,
		UserID:        42,
		PlanID:        7,
		StartsAt:      now.Add(-2 * time.Hour),
		ExpiresAt:     now.Add(7 * 24 * time.Hour),
		Status:        billing.SubscriptionStatusActive,
		SourceOrderID: &sourceOrderID,
		CreatedAt:     now.Add(-2 * time.Hour),
	}
	subRepo.Seed(existing)

	svc := newOriginalSubscriptionService(subRepo)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &billing.AssignSubscriptionInput{
		UserID:              42,
		PlanID:              7,
		ValidityDays:        30,
		DailyLimitUSD:       &limit,
		SourceOrderID:       &sourceOrderID,
		UseProvidedTemplate: true,
		Notes:               "retry same order",
	})
	require.NoError(t, err)
	require.False(t, queued)
	require.Equal(t, existing.ID, created.ID)
	require.Equal(t, 0, subRepo.CreateCalls)
}

func TestBulkAssignSubscription_ReportsQueuedAndActive(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	limit := 10.0
	subRepo.Seed(&billing.UserSubscription{
		ID:        12,
		UserID:    1,
		PlanID:    9,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(6 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	})

	svc := newOriginalSubscriptionService(subRepo)
	result, err := svc.BulkAssignSubscription(context.Background(), &billing.BulkAssignSubscriptionInput{
		UserIDs:       []int64{1, 2},
		PlanID:        9,
		ValidityDays:  7,
		DailyLimitUSD: &limit,
	})
	require.NoError(t, err)
	require.Equal(t, 2, result.SuccessCount)
	require.Equal(t, 2, result.CreatedCount)
	require.Equal(t, 0, result.ReusedCount)
	require.Equal(t, 0, result.FailedCount)
	require.Equal(t, "queued", result.Statuses[1])
	require.Equal(t, "active", result.Statuses[2])
}

func TestShiftLaterChain_ShiftsOnlyLaterSubscriptions(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	anchor := &billing.UserSubscription{
		ID:        31,
		UserID:    5,
		PlanID:    9,
		StartsAt:  now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now,
	}
	overlap := &billing.UserSubscription{
		ID:        32,
		UserID:    5,
		PlanID:    9,
		StartsAt:  now.Add(24 * time.Hour),
		ExpiresAt: now.Add(8 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusPending,
		CreatedAt: now.Add(time.Minute),
	}
	later := &billing.UserSubscription{
		ID:        33,
		UserID:    5,
		PlanID:    9,
		StartsAt:  anchor.ExpiresAt,
		ExpiresAt: anchor.ExpiresAt.Add(7 * 24 * time.Hour),
		Status:    billing.SubscriptionStatusPending,
		CreatedAt: now.Add(2 * time.Minute),
	}
	subRepo.Seed(anchor)
	subRepo.Seed(overlap)
	subRepo.Seed(later)

	svc := newOriginalSubscriptionService(subRepo)
	err := svc.ShiftLaterChain(context.Background(), []billing.UserSubscription{*anchor, *overlap, *later}, anchor, 48*time.Hour)
	require.NoError(t, err)

	unchangedOverlap, err := subRepo.GetByID(context.Background(), overlap.ID)
	require.NoError(t, err)
	require.Equal(t, overlap.StartsAt, unchangedOverlap.StartsAt)
	require.Equal(t, overlap.ExpiresAt, unchangedOverlap.ExpiresAt)

	shiftedLater, err := subRepo.GetByID(context.Background(), later.ID)
	require.NoError(t, err)
	require.Equal(t, later.StartsAt.Add(48*time.Hour), shiftedLater.StartsAt)
	require.Equal(t, later.ExpiresAt.Add(48*time.Hour), shiftedLater.ExpiresAt)
	require.Equal(t, billing.SubscriptionStatusPending, shiftedLater.Status)
}

func TestRevokeChainDelta_ActiveSubscriptionOnlyReleasesRemainingWindow(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	active := &billing.UserSubscription{
		StartsAt:  now.AddDate(0, 0, -10),
		ExpiresAt: now.AddDate(0, 0, 20),
		Status:    billing.SubscriptionStatusActive,
	}
	pending := &billing.UserSubscription{
		StartsAt:  now.AddDate(0, 0, 5),
		ExpiresAt: now.AddDate(0, 0, 35),
		Status:    billing.SubscriptionStatusPending,
	}
	expired := &billing.UserSubscription{
		StartsAt:  now.AddDate(0, 0, -40),
		ExpiresAt: now.AddDate(0, 0, -10),
		Status:    billing.SubscriptionStatusExpired,
	}

	require.Equal(t, now.Sub(active.ExpiresAt), billing.RevokeChainDelta(active, now))
	require.Equal(t, pending.StartsAt.Sub(pending.ExpiresAt), billing.RevokeChainDelta(pending, now))
	require.Equal(t, time.Duration(0), billing.RevokeChainDelta(expired, now))
}

func TestTargetSubscriptionExpiresAt_ActiveSubscriptionCountsFromNow(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	active := &billing.UserSubscription{
		StartsAt:  now.AddDate(0, 0, -11),
		ExpiresAt: now.AddDate(0, 0, 19),
		Status:    billing.SubscriptionStatusActive,
	}

	require.Equal(t, now.AddDate(0, 0, 30), billing.TargetSubscriptionExpiresAt(active, now, 30))
}

func TestTargetSubscriptionExpiresAt_PendingSubscriptionCountsFromStartsAt(t *testing.T) {
	now := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	pending := &billing.UserSubscription{
		StartsAt:  now.AddDate(0, 0, 7),
		ExpiresAt: now.AddDate(0, 0, 37),
		Status:    billing.SubscriptionStatusPending,
	}

	require.Equal(t, pending.StartsAt.AddDate(0, 0, 15), billing.TargetSubscriptionExpiresAt(pending, now, 15))
}

func TestGetActiveSubscription_FiltersByPlanID(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	subRepo.Seed(&billing.UserSubscription{
		ID:        13,
		UserID:    8,
		PlanID:    100,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
	})
	subRepo.Seed(&billing.UserSubscription{
		ID:        14,
		UserID:    8,
		PlanID:    200,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(48 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
	})

	svc := newOriginalSubscriptionService(subRepo)
	sub, err := svc.GetActiveSubscription(context.Background(), 8, 200)
	require.NoError(t, err)
	require.Equal(t, int64(14), sub.ID)
	require.Equal(t, int64(200), sub.PlanID)
}

func TestRestoreSubscription_ExpiredActiveRestoresAsExpired(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	deletedAt := now.Add(-time.Hour)
	subRepo.Seed(&billing.UserSubscription{
		ID:        101,
		UserID:    31,
		PlanID:    41,
		StartsAt:  now.Add(-48 * time.Hour),
		ExpiresAt: now.Add(-time.Minute),
		Status:    billing.SubscriptionStatusActive,
		DeletedAt: &deletedAt,
		CreatedAt: now.Add(-48 * time.Hour),
	})

	svc := newOriginalSubscriptionService(subRepo)
	restored, err := svc.RestoreSubscription(context.Background(), 101)
	require.NoError(t, err)
	require.Equal(t, billing.SubscriptionStatusExpired, restored.Status)
	require.Nil(t, restored.DeletedAt)

	got, err := subRepo.GetByID(context.Background(), 101)
	require.NoError(t, err)
	require.Equal(t, billing.SubscriptionStatusExpired, got.Status)
	require.Nil(t, got.DeletedAt)
}

func TestRestoreSubscription_NotRevokedReturnsConflict(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	subRepo.Seed(&billing.UserSubscription{
		ID:        102,
		UserID:    31,
		PlanID:    42,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now.Add(-time.Hour),
	})

	svc := newOriginalSubscriptionService(subRepo)
	_, err := svc.RestoreSubscription(context.Background(), 102)
	require.ErrorIs(t, err, billing.ErrSubscriptionNotRevoked)
}

func TestRestoreSubscription_LiveSubscriptionConflict(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	deletedAt := now.Add(-30 * time.Minute)
	subRepo.Seed(&billing.UserSubscription{
		ID:        103,
		UserID:    31,
		PlanID:    43,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(24 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		DeletedAt: &deletedAt,
		CreatedAt: now.Add(-time.Hour),
	})
	subRepo.Seed(&billing.UserSubscription{
		ID:        104,
		UserID:    31,
		PlanID:    43,
		StartsAt:  now.Add(-30 * time.Minute),
		ExpiresAt: now.Add(48 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		CreatedAt: now.Add(-30 * time.Minute),
	})

	svc := newOriginalSubscriptionService(subRepo)
	_, err := svc.RestoreSubscription(context.Background(), 103)
	require.ErrorIs(t, err, billing.ErrSubscriptionRestoreConflict)

	got, getErr := subRepo.GetByIDIncludeDeleted(context.Background(), 103)
	require.NoError(t, getErr)
	require.NotNil(t, got.DeletedAt)
}

func TestRestoreSubscription_FutureWindowRestoresAsPending(t *testing.T) {
	subRepo := billingtestkit.NewSubscriptionRepository()
	now := time.Now().UTC()
	deletedAt := now.Add(-time.Hour)
	subRepo.Seed(&billing.UserSubscription{
		ID:        105,
		UserID:    31,
		PlanID:    44,
		StartsAt:  now.Add(24 * time.Hour),
		ExpiresAt: now.Add(48 * time.Hour),
		Status:    billing.SubscriptionStatusActive,
		DeletedAt: &deletedAt,
		CreatedAt: now.Add(-time.Hour),
	})

	svc := newOriginalSubscriptionService(subRepo)
	restored, err := svc.RestoreSubscription(context.Background(), 105)
	require.NoError(t, err)
	require.Equal(t, billing.SubscriptionStatusPending, restored.Status)
	require.Nil(t, restored.DeletedAt)
}

func TestNormalizeAssignValidityDays(t *testing.T) {
	require.Equal(t, 30, billing.NormalizeAssignValidityDays(0))
	require.Equal(t, 30, billing.NormalizeAssignValidityDays(-5))
	require.Equal(t, billing.MaxValidityDays, billing.NormalizeAssignValidityDays(billing.MaxValidityDays+100))
	require.Equal(t, 7, billing.NormalizeAssignValidityDays(7))
}
