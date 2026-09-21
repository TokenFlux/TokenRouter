// 订阅内存替身保留原测试的排序、复制与调用记录，不替代存储事务证据。
package testkit

import (
	"context"
	"strconv"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
)

type SubscriptionRepositoryNoop struct{}

func (SubscriptionRepositoryNoop) Create(context.Context, *billing.UserSubscription) error {
	panic("unexpected Create call")
}
func (SubscriptionRepositoryNoop) GetByID(context.Context, int64) (*billing.UserSubscription, error) {
	panic("unexpected GetByID call")
}
func (SubscriptionRepositoryNoop) GetByIDIncludeDeleted(context.Context, int64) (*billing.UserSubscription, error) {
	panic("unexpected GetByIDIncludeDeleted call")
}
func (SubscriptionRepositoryNoop) GetByUserIDAndGroupID(context.Context, int64, int64) (*billing.UserSubscription, error) {
	panic("unexpected GetByUserIDAndGroupID call")
}
func (SubscriptionRepositoryNoop) GetActiveByUserIDAndGroupID(context.Context, int64, int64) (*billing.UserSubscription, error) {
	panic("unexpected GetActiveByUserIDAndGroupID call")
}
func (SubscriptionRepositoryNoop) GetLatestByUserIDAndPlanID(context.Context, int64, int64) (*billing.UserSubscription, error) {
	panic("unexpected GetLatestByUserIDAndPlanID call")
}
func (SubscriptionRepositoryNoop) Update(context.Context, *billing.UserSubscription) error {
	panic("unexpected Update call")
}
func (SubscriptionRepositoryNoop) Delete(context.Context, int64) error {
	panic("unexpected Delete call")
}
func (SubscriptionRepositoryNoop) Restore(context.Context, int64, string) (*billing.UserSubscription, error) {
	panic("unexpected Restore call")
}
func (SubscriptionRepositoryNoop) ListByUserID(context.Context, int64) ([]billing.UserSubscription, error) {
	panic("unexpected ListByUserID call")
}
func (SubscriptionRepositoryNoop) ListByUserIDAndPlanID(context.Context, int64, int64) ([]billing.UserSubscription, error) {
	panic("unexpected ListByUserIDAndPlanID call")
}
func (SubscriptionRepositoryNoop) ListActiveByUserID(context.Context, int64) ([]billing.UserSubscription, error) {
	panic("unexpected ListActiveByUserID call")
}
func (SubscriptionRepositoryNoop) ListByGroupID(context.Context, int64, pagination.PaginationParams) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByGroupID call")
}
func (SubscriptionRepositoryNoop) ListByPlanID(context.Context, int64, pagination.PaginationParams) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected ListByPlanID call")
}
func (SubscriptionRepositoryNoop) List(context.Context, pagination.PaginationParams, *int64, *int64, string, string, string, string) ([]billing.UserSubscription, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}
func (SubscriptionRepositoryNoop) ListBySourceOrderID(context.Context, int64) ([]billing.UserSubscription, error) {
	panic("unexpected ListBySourceOrderID call")
}
func (SubscriptionRepositoryNoop) ExistsByUserIDAndGroupID(context.Context, int64, int64) (bool, error) {
	panic("unexpected ExistsByUserIDAndGroupID call")
}
func (SubscriptionRepositoryNoop) ExtendExpiry(context.Context, int64, time.Time) error {
	panic("unexpected ExtendExpiry call")
}
func (SubscriptionRepositoryNoop) UpdateStatus(context.Context, int64, string) error {
	panic("unexpected UpdateStatus call")
}
func (SubscriptionRepositoryNoop) UpdateNotes(context.Context, int64, string) error {
	panic("unexpected UpdateNotes call")
}
func (SubscriptionRepositoryNoop) ActivateWindows(context.Context, int64, time.Time, billing.SubscriptionWindowActivation) error {
	panic("unexpected ActivateWindows call")
}
func (SubscriptionRepositoryNoop) ResetUsageWindows(context.Context, int64, bool, bool, bool, time.Time) error {
	panic("unexpected ResetUsageWindows call")
}
func (SubscriptionRepositoryNoop) ResetDailyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetDailyUsage call")
}
func (SubscriptionRepositoryNoop) ResetWeeklyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetWeeklyUsage call")
}
func (SubscriptionRepositoryNoop) ResetMonthlyUsage(context.Context, int64, *time.Time, time.Time) error {
	panic("unexpected ResetMonthlyUsage call")
}
func (SubscriptionRepositoryNoop) IncrementUsage(context.Context, int64, float64) error {
	panic("unexpected IncrementUsage call")
}
func (SubscriptionRepositoryNoop) BatchUpdateExpiredStatus(context.Context) (int64, error) {
	panic("unexpected BatchUpdateExpiredStatus call")
}

type SubscriptionRepository struct {
	SubscriptionRepositoryNoop

	nextID      int64
	ByID        map[int64]*billing.UserSubscription
	byUserPlan  map[string][]int64
	CreateCalls int
}

func NewSubscriptionRepository() *SubscriptionRepository {
	return &SubscriptionRepository{
		nextID:     1,
		ByID:       make(map[int64]*billing.UserSubscription),
		byUserPlan: make(map[string][]int64),
	}
}
func (s *SubscriptionRepository) key(userID, planID int64) string {
	return strconv.FormatInt(userID, 10) + ":" + strconv.FormatInt(planID, 10)
}
func (s *SubscriptionRepository) RebuildIndex() {
	s.byUserPlan = make(map[string][]int64)
	for id, sub := range s.ByID {
		key := s.key(sub.UserID, sub.PlanID)
		s.byUserPlan[key] = append(s.byUserPlan[key], id)
	}
	for key := range s.byUserPlan {
		ids := s.byUserPlan[key]
		for i := 0; i < len(ids); i++ {
			for j := i + 1; j < len(ids); j++ {
				left := s.ByID[ids[i]]
				right := s.ByID[ids[j]]
				if right.StartsAt.Before(left.StartsAt) ||
					(right.StartsAt.Equal(left.StartsAt) && right.CreatedAt.Before(left.CreatedAt)) {
					ids[i], ids[j] = ids[j], ids[i]
				}
			}
		}
		s.byUserPlan[key] = ids
	}
}
func (s *SubscriptionRepository) Seed(sub *billing.UserSubscription) {
	if sub == nil {
		return
	}
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	s.ByID[cp.ID] = &cp
	s.RebuildIndex()
}
func (s *SubscriptionRepository) Create(_ context.Context, sub *billing.UserSubscription) error {
	if sub == nil {
		return nil
	}
	s.CreateCalls++
	cp := *sub
	if cp.ID == 0 {
		cp.ID = s.nextID
		s.nextID++
	}
	sub.ID = cp.ID
	s.ByID[cp.ID] = &cp
	s.RebuildIndex()
	return nil
}
func (s *SubscriptionRepository) GetByID(_ context.Context, id int64) (*billing.UserSubscription, error) {
	sub := s.ByID[id]
	if sub == nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}
func (s *SubscriptionRepository) GetByIDIncludeDeleted(_ context.Context, id int64) (*billing.UserSubscription, error) {
	sub := s.ByID[id]
	if sub == nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	cp := *sub
	return &cp, nil
}
func (s *SubscriptionRepository) GetLatestByUserIDAndPlanID(_ context.Context, userID, planID int64) (*billing.UserSubscription, error) {
	ids := s.byUserPlan[s.key(userID, planID)]
	if len(ids) == 0 {
		return nil, billing.ErrSubscriptionNotFound
	}
	latest := s.ByID[ids[0]]
	for _, id := range ids[1:] {
		candidate := s.ByID[id]
		if candidate.ExpiresAt.After(latest.ExpiresAt) ||
			(candidate.ExpiresAt.Equal(latest.ExpiresAt) && candidate.CreatedAt.After(latest.CreatedAt)) {
			latest = candidate
		}
	}
	cp := *latest
	return &cp, nil
}
func (s *SubscriptionRepository) Update(_ context.Context, sub *billing.UserSubscription) error {
	if sub == nil {
		return nil
	}
	cp := *sub
	s.ByID[cp.ID] = &cp
	s.RebuildIndex()
	return nil
}
func (s *SubscriptionRepository) Restore(_ context.Context, subscriptionID int64, restoredStatus string) (*billing.UserSubscription, error) {
	sub := s.ByID[subscriptionID]
	if sub == nil {
		return nil, billing.ErrSubscriptionNotFound
	}
	cp := *sub
	cp.Status = restoredStatus
	cp.DeletedAt = nil
	cp.UpdatedAt = time.Now().UTC()
	s.ByID[subscriptionID] = &cp
	s.RebuildIndex()
	return &cp, nil
}
func (s *SubscriptionRepository) ListByUserID(_ context.Context, userID int64) ([]billing.UserSubscription, error) {
	out := make([]billing.UserSubscription, 0)
	for _, sub := range s.ByID {
		if sub.UserID == userID {
			out = append(out, *sub)
		}
	}
	return out, nil
}
func (s *SubscriptionRepository) ListByUserIDAndPlanID(_ context.Context, userID, planID int64) ([]billing.UserSubscription, error) {
	ids := s.byUserPlan[s.key(userID, planID)]
	out := make([]billing.UserSubscription, 0, len(ids))
	for _, id := range ids {
		out = append(out, *s.ByID[id])
	}
	return out, nil
}
func (s *SubscriptionRepository) ListActiveByUserID(_ context.Context, userID int64) ([]billing.UserSubscription, error) {
	now := time.Now()
	out := make([]billing.UserSubscription, 0)
	for _, sub := range s.ByID {
		if sub.UserID == userID && sub.EffectiveStatus(now) == billing.SubscriptionStatusActive {
			out = append(out, *sub)
		}
	}
	return out, nil
}
func (s *SubscriptionRepository) ListBySourceOrderID(_ context.Context, sourceOrderID int64) ([]billing.UserSubscription, error) {
	out := make([]billing.UserSubscription, 0)
	for _, sub := range s.ByID {
		if sub.SourceOrderID != nil && *sub.SourceOrderID == sourceOrderID {
			out = append(out, *sub)
		}
	}
	return out, nil
}
