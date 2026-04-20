//go:build integration

package repository

import (
	"context"
	"testing"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/suite"
)

type UserSubscriptionRepoSuite struct {
	suite.Suite
	ctx    context.Context
	client *dbent.Client
	repo   *userSubscriptionRepository
}

func (s *UserSubscriptionRepoSuite) SetupTest() {
	s.ctx = context.Background()
	tx := testEntTx(s.T())
	s.client = tx.Client()
	s.repo = NewUserSubscriptionRepository(s.client).(*userSubscriptionRepository)
}

func TestUserSubscriptionRepoSuite(t *testing.T) {
	suite.Run(t, new(UserSubscriptionRepoSuite))
}

func (s *UserSubscriptionRepoSuite) mustCreateUser(email string, role string) *service.User {
	s.T().Helper()
	if role == "" {
		role = service.RoleUser
	}

	user, err := s.client.User.Create().
		SetEmail(email).
		SetPasswordHash("test-password-hash").
		SetStatus(service.StatusActive).
		SetRole(role).
		Save(s.ctx)
	s.Require().NoError(err, "create user")
	return userEntityToService(user)
}

func (s *UserSubscriptionRepoSuite) mustCreatePlan(name string, validityDays int) *service.SubscriptionPlan {
	s.T().Helper()
	if validityDays <= 0 {
		validityDays = 30
	}

	plan, err := s.client.SubscriptionPlan.Create().
		SetName(name).
		SetDescription("test plan").
		SetPrice(19.9).
		SetValidityDays(validityDays).
		SetValidityUnit("day").
		SetFeatures("").
		SetProductName("").
		SetForSale(true).
		SetSortOrder(0).
		Save(s.ctx)
	s.Require().NoError(err, "create plan")
	return subscriptionPlanEntityToService(plan)
}

func (s *UserSubscriptionRepoSuite) mustCreateSubscription(userID, planID int64, mutate func(*dbent.UserSubscriptionCreate)) *dbent.UserSubscription {
	s.T().Helper()

	now := time.Now().UTC().Truncate(time.Second)
	create := s.client.UserSubscription.Create().
		SetUserID(userID).
		SetPlanID(planID).
		SetStartsAt(now.Add(-1 * time.Hour)).
		SetExpiresAt(now.Add(24 * time.Hour)).
		SetStatus(service.SubscriptionStatusActive).
		SetAssignedAt(now).
		SetNotes("")

	if mutate != nil {
		mutate(create)
	}

	sub, err := create.Save(s.ctx)
	s.Require().NoError(err, "create user subscription")
	return sub
}

func (s *UserSubscriptionRepoSuite) TestCreateAndGetByID_WithPreloads() {
	user := s.mustCreateUser("sub-create@test.com", service.RoleUser)
	plan := s.mustCreatePlan("plan-create", 30)
	admin := s.mustCreateUser("sub-admin@test.com", service.RoleAdmin)

	now := time.Now().UTC().Truncate(time.Second)
	sub := &service.UserSubscription{
		UserID:        user.ID,
		PlanID:        plan.ID,
		Status:        service.SubscriptionStatusActive,
		StartsAt:      now.Add(-2 * time.Hour),
		ExpiresAt:     now.Add(48 * time.Hour),
		AssignedBy:    &admin.ID,
		AssignedAt:    now,
		Notes:         "created by repo test",
		SourceOrderID: func() *int64 { v := int64(42); return &v }(),
	}

	err := s.repo.Create(s.ctx, sub)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(sub.ID)

	got, err := s.repo.GetByID(s.ctx, sub.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal(sub.UserID, got.UserID)
	s.Require().Equal(sub.PlanID, got.PlanID)
	s.Require().NotNil(got.User)
	s.Require().NotNil(got.Plan)
	s.Require().NotNil(got.AssignedByUser)
	s.Require().Equal(user.ID, got.User.ID)
	s.Require().Equal(plan.ID, got.Plan.ID)
	s.Require().Equal(admin.ID, got.AssignedByUser.ID)
}

func (s *UserSubscriptionRepoSuite) TestGetLatestByUserIDAndPlanID() {
	user := s.mustCreateUser("latest@test.com", service.RoleUser)
	plan := s.mustCreatePlan("plan-latest", 30)

	older := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-10 * 24 * time.Hour))
		c.SetExpiresAt(time.Now().Add(5 * 24 * time.Hour))
	})
	newer := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(6 * 24 * time.Hour))
		c.SetExpiresAt(time.Now().Add(35 * 24 * time.Hour))
		c.SetStatus(service.SubscriptionStatusPending)
	})

	got, err := s.repo.GetLatestByUserIDAndPlanID(s.ctx, user.ID, plan.ID)
	s.Require().NoError(err)
	s.Require().Equal(newer.ID, got.ID)
	s.Require().NotEqual(older.ID, got.ID)
}

func (s *UserSubscriptionRepoSuite) TestListByUserIDAndPlanID_OrderedByStartsAt() {
	user := s.mustCreateUser("plan-list@test.com", service.RoleUser)
	plan := s.mustCreatePlan("plan-list", 30)

	first := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		c.SetExpiresAt(time.Date(2026, 1, 31, 0, 0, 0, 0, time.UTC))
	})
	second := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC))
		c.SetExpiresAt(time.Date(2026, 3, 3, 0, 0, 0, 0, time.UTC))
		c.SetStatus(service.SubscriptionStatusPending)
	})

	subs, err := s.repo.ListByUserIDAndPlanID(s.ctx, user.ID, plan.ID)
	s.Require().NoError(err)
	s.Require().Len(subs, 2)
	s.Require().Equal(first.ID, subs[0].ID)
	s.Require().Equal(second.ID, subs[1].ID)
}

func (s *UserSubscriptionRepoSuite) TestListActiveByUserID_OnlyReturnsEffectiveSubscriptions() {
	user := s.mustCreateUser("active-list@test.com", service.RoleUser)
	activePlan := s.mustCreatePlan("plan-active", 30)
	expiredPlan := s.mustCreatePlan("plan-expired", 30)
	pendingPlan := s.mustCreatePlan("plan-pending", 30)

	active := s.mustCreateSubscription(user.ID, activePlan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-2 * time.Hour))
		c.SetExpiresAt(time.Now().Add(2 * time.Hour))
	})
	_ = s.mustCreateSubscription(user.ID, expiredPlan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-48 * time.Hour))
		c.SetExpiresAt(time.Now().Add(-24 * time.Hour))
		c.SetStatus(service.SubscriptionStatusExpired)
	})
	_ = s.mustCreateSubscription(user.ID, pendingPlan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(24 * time.Hour))
		c.SetExpiresAt(time.Now().Add(48 * time.Hour))
		c.SetStatus(service.SubscriptionStatusPending)
	})

	subs, err := s.repo.ListActiveByUserID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Len(subs, 1)
	s.Require().Equal(active.ID, subs[0].ID)
	s.Require().Equal(activePlan.ID, subs[0].PlanID)
}

func (s *UserSubscriptionRepoSuite) TestList_FilterByPlanAndPendingStatus() {
	user := s.mustCreateUser("filter@test.com", service.RoleUser)
	planA := s.mustCreatePlan("plan-filter-a", 30)
	planB := s.mustCreatePlan("plan-filter-b", 30)

	pending := s.mustCreateSubscription(user.ID, planA.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(24 * time.Hour))
		c.SetExpiresAt(time.Now().Add(48 * time.Hour))
		c.SetStatus(service.SubscriptionStatusPending)
	})
	_ = s.mustCreateSubscription(user.ID, planB.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-2 * time.Hour))
		c.SetExpiresAt(time.Now().Add(4 * time.Hour))
	})

	planID := planA.ID
	subs, page, err := s.repo.List(
		s.ctx,
		pagination.PaginationParams{Page: 1, PageSize: 10},
		&user.ID,
		&planID,
		service.SubscriptionStatusPending,
		"",
		"starts_at",
		"asc",
	)
	s.Require().NoError(err)
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(subs, 1)
	s.Require().Equal(pending.ID, subs[0].ID)
}

func (s *UserSubscriptionRepoSuite) TestListBySourceOrderIDAndUsageMutations() {
	user := s.mustCreateUser("source-order@test.com", service.RoleUser)
	plan := s.mustCreatePlan("plan-source-order", 30)
	now := time.Now().UTC().Truncate(time.Second)
	sourceOrderID := int64(12345)

	sub := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetSourceOrderID(sourceOrderID)
		c.SetDailyUsageUsd(4)
		c.SetWeeklyUsageUsd(5)
		c.SetMonthlyUsageUsd(6)
		c.SetDailyWindowStart(now.Add(-2 * time.Hour))
		c.SetWeeklyWindowStart(now.Add(-3 * 24 * time.Hour))
		c.SetMonthlyWindowStart(now.Add(-10 * 24 * time.Hour))
	})

	list, err := s.repo.ListBySourceOrderID(s.ctx, sourceOrderID)
	s.Require().NoError(err)
	s.Require().Len(list, 1)
	s.Require().Equal(sub.ID, list[0].ID)

	resetAt := now.Add(1 * time.Hour)
	s.Require().NoError(s.repo.ResetDailyUsage(s.ctx, sub.ID, resetAt))
	s.Require().NoError(s.repo.ResetWeeklyUsage(s.ctx, sub.ID, resetAt))
	s.Require().NoError(s.repo.ResetMonthlyUsage(s.ctx, sub.ID, resetAt))
	s.Require().NoError(s.repo.IncrementUsage(s.ctx, sub.ID, 2.5))

	got, err := s.repo.GetByID(s.ctx, sub.ID)
	s.Require().NoError(err)
	s.Require().Equal(2.5, got.DailyUsageUSD)
	s.Require().Equal(2.5, got.WeeklyUsageUSD)
	s.Require().Equal(2.5, got.MonthlyUsageUSD)
	s.Require().NotNil(got.DailyWindowStart)
	s.Require().Equal(resetAt, *got.DailyWindowStart)
}

func (s *UserSubscriptionRepoSuite) TestBatchUpdateExpiredStatus() {
	user := s.mustCreateUser("expire@test.com", service.RoleUser)
	plan := s.mustCreatePlan("plan-expire", 30)

	expired := s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-48 * time.Hour))
		c.SetExpiresAt(time.Now().Add(-24 * time.Hour))
		c.SetStatus(service.SubscriptionStatusActive)
	})
	_ = s.mustCreateSubscription(user.ID, plan.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStartsAt(time.Now().Add(-2 * time.Hour))
		c.SetExpiresAt(time.Now().Add(24 * time.Hour))
		c.SetStatus(service.SubscriptionStatusActive)
	})

	updated, err := s.repo.BatchUpdateExpiredStatus(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(int64(1), updated)

	got, err := s.repo.GetByID(s.ctx, expired.ID)
	s.Require().NoError(err)
	s.Require().Equal(service.SubscriptionStatusExpired, got.Status)
}
