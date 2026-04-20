package service

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type groupRepoNoop struct{}

func (groupRepoNoop) Create(context.Context, *Group) error { panic("unexpected Create call") }
func (groupRepoNoop) GetByID(context.Context, int64) (*Group, error) {
	panic("unexpected GetByID call")
}
func (groupRepoNoop) GetByIDLite(context.Context, int64) (*Group, error) {
	panic("unexpected GetByIDLite call")
}
func (groupRepoNoop) Update(context.Context, *Group) error { panic("unexpected Update call") }
func (groupRepoNoop) Delete(context.Context, int64) error  { panic("unexpected Delete call") }
func (groupRepoNoop) DeleteCascade(context.Context, int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}
func (groupRepoNoop) List(context.Context, pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (groupRepoNoop) ListWithFilters(context.Context, pagination.PaginationParams, string, string, string, *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}
func (groupRepoNoop) ListActive(context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}
func (groupRepoNoop) ListActiveByPlatform(context.Context, string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (groupRepoNoop) ExistsByName(context.Context, string) (bool, error) {
	panic("unexpected ExistsByName call")
}
func (groupRepoNoop) GetAccountCount(context.Context, int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}
func (groupRepoNoop) DeleteAccountGroupsByGroupID(context.Context, int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}
func (groupRepoNoop) GetAccountIDsByGroupIDs(context.Context, []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}
func (groupRepoNoop) BindAccountsToGroup(context.Context, int64, []int64) error {
	panic("unexpected BindAccountsToGroup call")
}
func (groupRepoNoop) UpdateSortOrders(context.Context, []GroupSortOrderUpdate) error {
	panic("unexpected UpdateSortOrders call")
}

type userSubRepoNoop struct{}

func (userSubRepoNoop) Create(context.Context, *UserSubscription) error {
	panic("unexpected Create call")
}
func (userSubRepoNoop) GetByID(context.Context, int64) (*UserSubscription, error) {
	panic("unexpected GetByID call")
}
func (userSubRepoNoop) GetByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetByUserIDAndGroupID call")
}
func (userSubRepoNoop) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetActiveByUserIDAndGroupID call")
}
func (userSubRepoNoop) GetLatestByUserIDAndPlanID(context.Context, int64, int64) (*UserSubscription, error) {
	panic("unexpected GetLatestByUserIDAndPlanID call")
}
func (userSubRepoNoop) Update(context.Context, *UserSubscription) error {
	panic("unexpected Update call")
}
func (userSubRepoNoop) Delete(context.Context, int64) error { panic("unexpected Delete call") }
func (userSubRepoNoop) ListByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListByUserID call")
}
func (userSubRepoNoop) ListByUserIDAndPlanID(context.Context, int64, int64) ([]UserSubscription, error) {
	panic("unexpected ListByUserIDAndPlanID call")
}
func (userSubRepoNoop) ListActiveByUserID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListActiveByUserID call")
}
func (userSubRepoNoop) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}
func (userSubRepoNoop) ListByPlanID(context.Context, int64, pagination.PaginationParams) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByPlanID call")
}
func (userSubRepoNoop) List(context.Context, pagination.PaginationParams, *int64, *int64, string, string, string, string) ([]UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (userSubRepoNoop) ListBySourceOrderID(context.Context, int64) ([]UserSubscription, error) {
	panic("unexpected ListBySourceOrderID call")
}
func (userSubRepoNoop) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsByUserIDAndGroupID call")
}
func (userSubRepoNoop) ExtendExpiry(context.Context, int64, time.Time) error {
	panic("unexpected ExtendExpiry call")
}
func (userSubRepoNoop) UpdateStatus(context.Context, int64, string) error {
	panic("unexpected UpdateStatus call")
}
func (userSubRepoNoop) UpdateNotes(context.Context, int64, string) error {
	panic("unexpected UpdateNotes call")
}
func (userSubRepoNoop) ActivateWindows(context.Context, int64, time.Time) error {
	panic("unexpected ActivateWindows call")
}
func (userSubRepoNoop) ResetDailyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetDailyUsage call")
}
func (userSubRepoNoop) ResetWeeklyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetWeeklyUsage call")
}
func (userSubRepoNoop) ResetMonthlyUsage(context.Context, int64, time.Time) error {
	panic("unexpected ResetMonthlyUsage call")
}
func (userSubRepoNoop) IncrementUsage(context.Context, int64, float64) error {
	panic("unexpected IncrementUsage call")
}
func (userSubRepoNoop) BatchUpdateExpiredStatus(context.Context) (int64, error) {
	panic("unexpected BatchUpdateExpiredStatus call")
}

type subscriptionUserSubRepoStub struct {
	userSubRepoNoop

	nextID      int64
	byID        map[int64]*UserSubscription
	byUserPlan  map[string][]int64
	createCalls int
}

func newSubscriptionUserSubRepoStub() *subscriptionUserSubRepoStub {
	return &subscriptionUserSubRepoStub{
		nextID:     1,
		byID:       make(map[int64]*UserSubscription),
		byUserPlan: make(map[string][]int64),
	}
}

func (s *subscriptionUserSubRepoStub) key(userID, planID int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(planID, 10)
}

func (s *subscriptionUserSubRepoStub) rebuildIndex() {
	s.byUserPlan = make(map[string][]int64)
	for id, sub := range s.byID {
		key := s.key(sub.UserID, sub.PlanID)
		s.byUserPlan[key] = append(s.byUserPlan[key], id)
	}
	for key := range s.byUserPlan {
		ids := s.byUserPlan[key]
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				left := s.byID[ids[i]]
				right := s.byID[ids[j]]
				if right.StartsAt.Before(left.StartsAt) ||
					(right.StartsAt.Equal(left.StartsAt) && right.CreatedAt.Before(left.CreatedAt)) {
					ids[i], ids[j] = ids[j], ids[i]
				}
			}
		}
		s.byUserPlan[key] = ids
	}
}

func (s *subscriptionUserSubRepoStub) seed(sub *UserSubscription) {
	if sub == nil {
		return
	}
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	s.byID[cp.ID] = &cp
	s.rebuildIndex()
}

func (s *subscriptionUserSubRepoStub) Create(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return nil
	}
	s.createCalls++
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	sub.ID = cp.ID
	s.byID[cp.ID] = &cp
	s.rebuildIndex()
	return nil
}

func (s *subscriptionUserSubRepoStub) GetByID(_ context.Context, id int64) (*UserSubscription, error) {
	sub := s.byID[id]
	if sub == nil {
		return nil, ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) GetLatestByUserIDAndPlanID(_ context.Context, userID, planID int64) (*UserSubscription, error) {
	ids := s.byUserPlan[s.key(userID, planID)]
	if len(ids) == 0 {
		return nil, ErrSubscriptionNotFound
	}
	latest := s.byID[ids[0]]
	for _, id := range ids[1:] {
		candidate := s.byID[id]
		if candidate.ExpiresAt.After(latest.ExpiresAt) ||
			(candidate.ExpiresAt.Equal(latest.ExpiresAt) && candidate.CreatedAt.After(latest.CreatedAt)) {
			latest = candidate
		}
	}
	cp := *latest
	return &cp, nil
}

func (s *subscriptionUserSubRepoStub) Update(_ context.Context, sub *UserSubscription) error {
	if sub == nil {
		return nil
	}
	cp := *sub
	s.byID[cp.ID] = &cp
	s.rebuildIndex()
	return nil
}

func (s *subscriptionUserSubRepoStub) ListByUserID(_ context.Context, userID int64) ([]UserSubscription, error) {
	out := make([]UserSubscription, 0)
	for _, sub := range s.byID {
		if sub.UserID == userID {
			out = append(out, *sub)
		}
	}
	return out, nil
}

func (s *subscriptionUserSubRepoStub) ListByUserIDAndPlanID(_ context.Context, userID, planID int64) ([]UserSubscription, error) {
	ids := s.byUserPlan[s.key(userID, planID)]
	out := make([]UserSubscription, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.byID[id])
	}
	return out, nil
}

func (s *subscriptionUserSubRepoStub) ListActiveByUserID(_ context.Context, userID int64) ([]UserSubscription, error) {
	now := time.Now()
	out := make([]UserSubscription, 0)
	for _, sub := range s.byID {
		if sub.UserID == userID && sub.EffectiveStatus(now) == SubscriptionStatusActive {
			out = append(out, *sub)
		}
	}
	return out, nil
}

func (s *subscriptionUserSubRepoStub) ListBySourceOrderID(_ context.Context, sourceOrderID int64) ([]UserSubscription, error) {
	out := make([]UserSubscription, 0)
	for _, sub := range s.byID {
		if sub.SourceOrderID != nil && *sub.SourceOrderID == sourceOrderID {
			out = append(out, *sub)
		}
	}
	return out, nil
}

func TestAssignSubscription_SamePlanCreatesPendingChain(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	limit := 10.0
	existing := &UserSubscription{
		ID:        10,
		UserID:    1001,
		PlanID:    1,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(29 * 24 * time.Hour),
		Status:    SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	}
	subRepo.seed(existing)

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:        1001,
		PlanID:        1,
		ValidityDays:  30,
		DailyLimitUSD: &limit,
		Notes:         "renew",
	})
	require.NoError(t, err)
	require.True(t, queued)
	require.Equal(t, SubscriptionStatusPending, created.Status)
	require.Equal(t, existing.ExpiresAt, created.StartsAt)
	require.Equal(t, existing.ExpiresAt.AddDate(0, 0, 30), created.ExpiresAt)
	require.Equal(t, 1, subRepo.createCalls)
}

func TestAssignSubscription_DifferentPlanStartsImmediately(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	limit := 10.0
	subRepo.seed(&UserSubscription{
		ID:        11,
		UserID:    1001,
		PlanID:    1,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(29 * 24 * time.Hour),
		Status:    SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	})

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
		UserID:        1001,
		PlanID:        2,
		ValidityDays:  7,
		DailyLimitUSD: &limit,
	})
	require.NoError(t, err)
	require.False(t, queued)
	require.Equal(t, SubscriptionStatusActive, created.Status)
	require.WithinDuration(t, time.Now(), created.StartsAt, 2*time.Second)
	require.Equal(t, created.StartsAt.AddDate(0, 0, 7), created.ExpiresAt)
}

func TestAssignSubscription_ReusesExistingSourceOrderSubscription(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	sourceOrderID := int64(7788)
	limit := 18.0
	existing := &UserSubscription{
		ID:            21,
		UserID:        42,
		PlanID:        7,
		StartsAt:      now.Add(-2 * time.Hour),
		ExpiresAt:     now.Add(7 * 24 * time.Hour),
		Status:        SubscriptionStatusActive,
		SourceOrderID: &sourceOrderID,
		CreatedAt:     now.Add(-2 * time.Hour),
	}
	subRepo.seed(existing)

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	created, queued, err := svc.AssignOrExtendSubscription(context.Background(), &AssignSubscriptionInput{
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
	require.Equal(t, 0, subRepo.createCalls)
}

func TestBulkAssignSubscription_ReportsQueuedAndActive(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	limit := 10.0
	subRepo.seed(&UserSubscription{
		ID:        12,
		UserID:    1,
		PlanID:    9,
		StartsAt:  now.Add(-24 * time.Hour),
		ExpiresAt: now.Add(6 * 24 * time.Hour),
		Status:    SubscriptionStatusActive,
		CreatedAt: now.Add(-24 * time.Hour),
	})

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	result, err := svc.BulkAssignSubscription(context.Background(), &BulkAssignSubscriptionInput{
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
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	anchor := &UserSubscription{
		ID:        31,
		UserID:    5,
		PlanID:    9,
		StartsAt:  now,
		ExpiresAt: now.Add(7 * 24 * time.Hour),
		Status:    SubscriptionStatusActive,
		CreatedAt: now,
	}
	overlap := &UserSubscription{
		ID:        32,
		UserID:    5,
		PlanID:    9,
		StartsAt:  now.Add(24 * time.Hour),
		ExpiresAt: now.Add(8 * 24 * time.Hour),
		Status:    SubscriptionStatusPending,
		CreatedAt: now.Add(time.Minute),
	}
	later := &UserSubscription{
		ID:        33,
		UserID:    5,
		PlanID:    9,
		StartsAt:  anchor.ExpiresAt,
		ExpiresAt: anchor.ExpiresAt.Add(7 * 24 * time.Hour),
		Status:    SubscriptionStatusPending,
		CreatedAt: now.Add(2 * time.Minute),
	}
	subRepo.seed(anchor)
	subRepo.seed(overlap)
	subRepo.seed(later)

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	err := svc.shiftLaterChain(context.Background(), []UserSubscription{*anchor, *overlap, *later}, anchor, 48*time.Hour)
	require.NoError(t, err)

	unchangedOverlap, err := subRepo.GetByID(context.Background(), overlap.ID)
	require.NoError(t, err)
	require.Equal(t, overlap.StartsAt, unchangedOverlap.StartsAt)
	require.Equal(t, overlap.ExpiresAt, unchangedOverlap.ExpiresAt)

	shiftedLater, err := subRepo.GetByID(context.Background(), later.ID)
	require.NoError(t, err)
	require.Equal(t, later.StartsAt.Add(48*time.Hour), shiftedLater.StartsAt)
	require.Equal(t, later.ExpiresAt.Add(48*time.Hour), shiftedLater.ExpiresAt)
	require.Equal(t, SubscriptionStatusPending, shiftedLater.Status)
}

func TestGetActiveSubscription_FiltersByPlanID(t *testing.T) {
	subRepo := newSubscriptionUserSubRepoStub()
	now := time.Now().UTC()
	subRepo.seed(&UserSubscription{
		ID:        13,
		UserID:    8,
		PlanID:    100,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(24 * time.Hour),
		Status:    SubscriptionStatusActive,
	})
	subRepo.seed(&UserSubscription{
		ID:        14,
		UserID:    8,
		PlanID:    200,
		StartsAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(48 * time.Hour),
		Status:    SubscriptionStatusActive,
	})

	svc := NewSubscriptionService(groupRepoNoop{}, subRepo, nil, nil, nil)
	sub, err := svc.GetActiveSubscription(context.Background(), 8, 200)
	require.NoError(t, err)
	require.Equal(t, int64(14), sub.ID)
	require.Equal(t, int64(200), sub.PlanID)
}

func TestNormalizeAssignValidityDays(t *testing.T) {
	require.Equal(t, 30, normalizeAssignValidityDays(0))
	require.Equal(t, 30, normalizeAssignValidityDays(-5))
	require.Equal(t, MaxValidityDays, normalizeAssignValidityDays(MaxValidityDays+100))
	require.Equal(t, 7, normalizeAssignValidityDays(7))
}
