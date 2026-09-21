//go:build integration

package repository

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	identitycore "github.com/TokenFlux/TokenRouter/internal/identity"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/authidentity"
	"github.com/TokenFlux/TokenRouter/ent/authidentitychannel"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	"github.com/stretchr/testify/suite"
)

type UserRepoSuite struct {
	suite.Suite
	ctx    context.Context
	client *dbent.Client
	repo   *postgres.UserStore
}

func (s *UserRepoSuite) SetupTest() {
	s.ctx = context.Background()
	s.client = testEntClient(s.T())
	s.repo = postgres.NewUserStoreWithSQL(s.client, integrationDB)

	// 清理测试数据，确保每个测试从干净状态开始
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM auth_identity_channels")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM auth_identities")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM user_subscriptions")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM user_allowed_groups")
	_, _ = integrationDB.ExecContext(s.ctx, "DELETE FROM users")
}

func TestUserRepoSuite(t *testing.T) {
	suite.Run(t, new(UserRepoSuite))
}

func (s *UserRepoSuite) mustCreateUser(u *identitycore.User) *identitycore.User {
	s.T().Helper()

	if u.Email == "" {
		u.Email = "user-" + time.Now().Format(time.RFC3339Nano) + "@example.com"
	}
	if u.PasswordHash == "" {
		u.PasswordHash = "test-password-hash"
	}
	if u.Role == "" {
		u.Role = identitycore.RoleUser
	}
	if u.Status == "" {
		u.Status = billing.StatusActive
	}
	if u.Concurrency == 0 {
		u.Concurrency = 5
	}

	s.Require().NoError(s.repo.Create(s.ctx, u), "create user")
	return u
}

func (s *UserRepoSuite) mustCreateGroup(name string) *routing.Group {
	s.T().Helper()

	g, err := s.client.Group.Create().
		SetName(name).
		SetStatus(billing.StatusActive).
		Save(s.ctx)
	s.Require().NoError(err, "create group")
	return routingpostgres.GroupFromEnt(g)
}

func (s *UserRepoSuite) TestUpdateForkSpecificFields() {
	user := s.mustCreateUser(&identitycore.User{Email: "fork-update-fields@example.com", APIKeyLimit: 100})
	group := s.mustCreateGroup("fork-update-public-group")

	loaded, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	loaded.APIKeyLimit = 7
	loaded.DisabledPublicGroups = []int64{group.ID}

	// fork 的数量上限与公共分组禁用关系必须独立受掩码控制。
	s.Require().NoError(s.repo.Update(s.ctx, loaded, identitycore.UserUpdateFields{
		APIKeyLimit:          true,
		DisabledPublicGroups: true,
	}))

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(7, updated.APIKeyLimit)
	s.Require().Equal([]int64{group.ID}, updated.DisabledPublicGroups)
}

func (s *UserRepoSuite) mustCreatePlan(name string) *billing.SubscriptionPlan {
	s.T().Helper()

	plan, err := s.client.SubscriptionPlan.Create().
		SetName(name).
		SetDescription("test plan").
		SetPrice(9.9).
		SetValidityDays(30).
		SetValidityUnit("day").
		SetFeatures("").
		SetProductName("").
		SetForSale(true).
		SetSortOrder(0).
		Save(s.ctx)
	s.Require().NoError(err, "create plan")
	return billingpostgres.PlanFromEntity(plan)
}

func (s *UserRepoSuite) mustCreateSubscription(userID, planID int64, mutate func(*dbent.UserSubscriptionCreate)) *dbent.UserSubscription {
	s.T().Helper()

	now := time.Now()
	create := s.client.UserSubscription.Create().
		SetUserID(userID).
		SetPlanID(planID).
		SetStartsAt(now.Add(-1 * time.Hour)).
		SetExpiresAt(now.Add(24 * time.Hour)).
		SetStatus(billing.SubscriptionStatusActive).
		SetAssignedAt(now).
		SetNotes("")

	if mutate != nil {
		mutate(create)
	}

	sub, err := create.Save(s.ctx)
	s.Require().NoError(err, "create subscription")
	return sub
}

// --- Create / GetByID / GetByEmail / Update / Delete ---

func (s *UserRepoSuite) TestCreate() {
	user := s.mustCreateUser(&identitycore.User{
		Email:        "create@test.com",
		Username:     "testuser",
		PasswordHash: "test-password-hash",
		Role:         identitycore.RoleUser,
		Status:       billing.StatusActive,
	})

	s.Require().NotZero(user.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("create@test.com", got.Email)
}

func (s *UserRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
}

func (s *UserRepoSuite) TestGetByEmail() {
	user := s.mustCreateUser(&identitycore.User{Email: "byemail@test.com"})

	got, err := s.repo.GetByEmail(s.ctx, user.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user.ID, got.ID)
}

func (s *UserRepoSuite) TestGetByEmail_NotFound() {
	_, err := s.repo.GetByEmail(s.ctx, "nonexistent@test.com")
	s.Require().Error(err, "expected error for non-existent email")
}

func (s *UserRepoSuite) TestExistsByEmail_NormalizesSpacingAndCaseOnPostgres() {
	s.mustCreateUser(&identitycore.User{Email: " Legacy@Example.com "})

	exists, err := s.repo.ExistsByEmail(s.ctx, "  LEGACY@example.com  ")
	s.Require().NoError(err, "ExistsByEmail normalized lookup")
	s.Require().True(exists)
}

func (s *UserRepoSuite) TestUpdate() {
	user := s.mustCreateUser(&identitycore.User{Email: "update@test.com", Username: "original"})

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	got.Username = "updated"
	s.Require().NoError(s.repo.Update(s.ctx, got, identitycore.UserUpdateFields{Username: true}), "Update")

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("updated", updated.Username)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsUpdatesOnlyProvidedFields() {
	user := s.mustCreateUser(&identitycore.User{
		Email:       "batch-limits-one-field@test.com",
		Concurrency: 4,
		RPMLimit:    20,
	})
	concurrency := 9

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{user.ID}, &concurrency, nil)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Equal(9, updated.Concurrency)
	s.Equal(20, updated.RPMLimit)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsUpdatesBothFieldsToZero() {
	user := s.mustCreateUser(&identitycore.User{
		Email:       "batch-limits-zero@test.com",
		Concurrency: 4,
		RPMLimit:    20,
	})
	zero := 0

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{user.ID}, &zero, &zero)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Zero(updated.Concurrency)
	s.Zero(updated.RPMLimit)
}

func (s *UserRepoSuite) TestBatchUpdateLimitsIgnoresDeletedUsersAndReturnsAffectedRows() {
	active := s.mustCreateUser(&identitycore.User{Email: "batch-limits-active@test.com", RPMLimit: 10})
	deleted := s.mustCreateUser(&identitycore.User{Email: "batch-limits-deleted@test.com", RPMLimit: 10})
	s.Require().NoError(s.client.User.DeleteOneID(deleted.ID).Exec(s.ctx))
	rpmLimit := 45

	affected, err := s.repo.BatchUpdateLimits(s.ctx, []int64{active.ID, deleted.ID}, nil, &rpmLimit)
	s.Require().NoError(err)
	s.Equal(1, affected)

	updatedActive, err := s.repo.GetByID(s.ctx, active.ID)
	s.Require().NoError(err)
	s.Equal(45, updatedActive.RPMLimit)
	updatedDeleted, err := s.repo.GetByIDIncludeDeleted(s.ctx, deleted.ID)
	s.Require().NoError(err)
	s.Equal(10, updatedDeleted.RPMLimit)
}

func (s *UserRepoSuite) TestUpdateIgnoresNoRowsFromConflictingEmailIdentityUpsert() {
	user := s.mustCreateUser(&identitycore.User{Email: "update-existing-identity@test.com", Username: "original"})

	identityCount, err := s.client.AuthIdentity.Query().
		Where(
			authidentity.UserIDEQ(user.ID),
			authidentity.ProviderTypeEQ("email"),
			authidentity.ProviderKeyEQ("email"),
			authidentity.ProviderSubjectEQ("update-existing-identity@test.com"),
		).
		Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(1, identityCount)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	got.Username = "updated"
	s.Require().NoError(s.repo.Update(s.ctx, got, identitycore.UserUpdateFields{Username: true}), "Update should tolerate ON CONFLICT DO NOTHING returning no rows")

	updated, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal("updated", updated.Username)
}

func (s *UserRepoSuite) TestDelete() {
	user := s.mustCreateUser(&identitycore.User{Email: "delete@test.com"})

	err := s.repo.Delete(s.ctx, user.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, user.ID)
	s.Require().Error(err, "expected error after delete")
}

func (s *UserRepoSuite) TestDeleteRemovesAuthIdentitiesAndChannels() {
	user := s.mustCreateUser(&identitycore.User{Email: "delete-oauth@test.com"})

	identity, err := s.client.AuthIdentity.Create().
		SetUserID(user.ID).
		SetProviderType("linuxdo").
		SetProviderKey("linuxdo").
		SetProviderSubject("delete-oauth-subject").
		Save(s.ctx)
	s.Require().NoError(err)

	_, err = s.client.AuthIdentityChannel.Create().
		SetIdentityID(identity.ID).
		SetProviderType("wechat").
		SetProviderKey("wechat").
		SetChannel("open").
		SetChannelAppID("app-id").
		SetChannelSubject("openid-123").
		Save(s.ctx)
	s.Require().NoError(err)

	err = s.repo.Delete(s.ctx, user.ID)
	s.Require().NoError(err)

	identityCount, err := s.client.AuthIdentity.Query().Where(authidentity.UserIDEQ(user.ID)).Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Zero(identityCount)

	channelCount, err := s.client.AuthIdentityChannel.Query().Where(authidentitychannel.IdentityIDEQ(identity.ID)).Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Zero(channelCount)
}

// --- List / ListWithFilters ---

func (s *UserRepoSuite) TestList() {
	s.mustCreateUser(&identitycore.User{Email: "list1@test.com"})
	s.mustCreateUser(&identitycore.User{Email: "list2@test.com"})

	users, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	s.Require().Len(users, 2)
	s.Require().Equal(int64(2), page.Total)
}

func (s *UserRepoSuite) TestListWithFilters_Status() {
	s.mustCreateUser(&identitycore.User{Email: "active@test.com", Status: billing.StatusActive})
	s.mustCreateUser(&identitycore.User{Email: "disabled@test.com", Status: billing.StatusDisabled})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Status: billing.StatusActive})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(billing.StatusActive, users[0].Status)
}

func (s *UserRepoSuite) TestListWithFilters_Role() {
	s.mustCreateUser(&identitycore.User{Email: "user@test.com", Role: identitycore.RoleUser})
	s.mustCreateUser(&identitycore.User{Email: "admin@test.com", Role: identitycore.RoleAdmin})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Role: identitycore.RoleAdmin})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal(identitycore.RoleAdmin, users[0].Role)
}

func (s *UserRepoSuite) TestListWithFilters_Search() {
	s.mustCreateUser(&identitycore.User{Email: "alice@test.com", Username: "Alice"})
	s.mustCreateUser(&identitycore.User{Email: "bob@test.com", Username: "Bob"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Search: "alice"})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Contains(users[0].Email, "alice")
}

func (s *UserRepoSuite) TestListWithFilters_SearchByUsername() {
	s.mustCreateUser(&identitycore.User{Email: "u1@test.com", Username: "JohnDoe"})
	s.mustCreateUser(&identitycore.User{Email: "u2@test.com", Username: "JaneSmith"})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Search: "john"})
	s.Require().NoError(err)
	s.Require().Len(users, 1)
	s.Require().Equal("JohnDoe", users[0].Username)
}

func (s *UserRepoSuite) TestListWithFilters_LoadsActiveSubscriptions() {
	user := s.mustCreateUser(&identitycore.User{Email: "sub@test.com", Status: billing.StatusActive})
	planActive := s.mustCreatePlan("plan-sub-active")
	planExpired := s.mustCreatePlan("plan-sub-expired")

	_ = s.mustCreateSubscription(user.ID, planActive.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStatus(billing.SubscriptionStatusActive)
		c.SetExpiresAt(time.Now().Add(1 * time.Hour))
	})
	_ = s.mustCreateSubscription(user.ID, planExpired.ID, func(c *dbent.UserSubscriptionCreate) {
		c.SetStatus(billing.SubscriptionStatusExpired)
		c.SetExpiresAt(time.Now().Add(-1 * time.Hour))
	})

	users, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Search: "sub@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Len(users, 1, "expected 1 user")
	s.Require().Len(users[0].Subscriptions, 1, "expected 1 active subscription")
	s.Require().NotNil(users[0].Subscriptions[0].Plan, "expected subscription plan preload")
	s.Require().Equal(planActive.ID, users[0].Subscriptions[0].Plan.ID, "plan ID mismatch")
}

func (s *UserRepoSuite) TestListWithFilters_CombinedFilters() {
	s.mustCreateUser(&identitycore.User{
		Email:    "a@example.com",
		Username: "Alice",
		Role:     identitycore.RoleUser,
		Status:   billing.StatusActive,
		Balance:  10,
	})
	target := s.mustCreateUser(&identitycore.User{
		Email:    "b@example.com",
		Username: "Bob",
		Role:     identitycore.RoleAdmin,
		Status:   billing.StatusActive,
		Balance:  1,
	})
	s.mustCreateUser(&identitycore.User{
		Email:  "c@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusDisabled,
	})

	users, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, identitycore.UserListFilters{Status: billing.StatusActive, Role: identitycore.RoleAdmin, Search: "b@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(target.ID, users[0].ID, "ListWithFilters result mismatch")
}

// --- Balance operations ---

func (s *UserRepoSuite) TestUpdateBalance() {
	user := s.mustCreateUser(&identitycore.User{Email: "bal@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, 2.5)
	s.Require().NoError(err, "UpdateBalance")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(12.5, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestUpdateBalance_Negative() {
	user := s.mustCreateUser(&identitycore.User{Email: "balneg@test.com", Balance: 10})

	err := s.repo.UpdateBalance(s.ctx, user.ID, -3)
	s.Require().NoError(err, "UpdateBalance with negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(7.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestApplyRedeemBalanceAdjustment_ConcurrentNeverNegative() {
	user := s.mustCreateUser(&identitycore.User{Email: "redeem-bal-concurrent@test.com", Balance: 10})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.repo.ApplyRedeemBalanceAdjustment(context.Background(), user.ID, -7)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance() {
	user := s.mustCreateUser(&identitycore.User{Email: "deduct@test.com", Balance: 10})

	deducted, err := s.repo.DeductBalance(s.ctx, user.ID, 5)
	s.Require().NoError(err, "DeductBalance")
	s.Require().InDelta(5.0, deducted, 1e-6)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(5.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance_InsufficientFunds() {
	user := s.mustCreateUser(&identitycore.User{Email: "insuf@test.com", Balance: 5})

	deducted, err := s.repo.DeductBalance(s.ctx, user.ID, 999)
	s.Require().NoError(err, "DeductBalance should clamp to available balance")
	s.Require().InDelta(5.0, deducted, 1e-6)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(0.0, got.Balance, 1e-6, "Balance should clamp to zero")
}

func (s *UserRepoSuite) TestDeductBalance_ExactAmount() {
	user := s.mustCreateUser(&identitycore.User{Email: "exact@test.com", Balance: 10})

	deducted, err := s.repo.DeductBalance(s.ctx, user.ID, 10)
	s.Require().NoError(err, "DeductBalance exact amount")
	s.Require().InDelta(10.0, deducted, 1e-6)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(0.0, got.Balance, 1e-6)
}

func (s *UserRepoSuite) TestDeductBalance_LeavesLegacyNegativeBalanceUnchanged() {
	user := s.mustCreateUser(&identitycore.User{Email: "legacy-negative@test.com", Balance: -5.0})

	deducted, err := s.repo.DeductBalance(s.ctx, user.ID, 10.0)
	s.Require().NoError(err, "DeductBalance should ignore already non-positive balance")
	s.Require().InDelta(0.0, deducted, 1e-6)

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().InDelta(-5.0, got.Balance, 1e-6, "Existing negative balance should remain unchanged")
}

// --- Concurrency ---

func (s *UserRepoSuite) TestUpdateConcurrency() {
	user := s.mustCreateUser(&identitycore.User{Email: "conc@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, 3)
	s.Require().NoError(err, "UpdateConcurrency")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(8, got.Concurrency)
}

func (s *UserRepoSuite) TestUpdateConcurrency_Negative() {
	user := s.mustCreateUser(&identitycore.User{Email: "concneg@test.com", Concurrency: 5})

	err := s.repo.UpdateConcurrency(s.ctx, user.ID, -2)
	s.Require().NoError(err, "UpdateConcurrency negative")

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(3, got.Concurrency)
}

func (s *UserRepoSuite) TestApplyRedeemConcurrencyAdjustment_ConcurrentNeverNegative() {
	user := s.mustCreateUser(&identitycore.User{Email: "redeem-concurrency-concurrent@test.com", Concurrency: 10})

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.repo.ApplyRedeemConcurrencyAdjustment(context.Background(), user.ID, -7)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		s.Require().NoError(err)
	}

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal(0, got.Concurrency)
}

// --- ExistsByEmail ---

func (s *UserRepoSuite) TestExistsByEmail() {
	s.mustCreateUser(&identitycore.User{Email: "exists@test.com"})

	exists, err := s.repo.ExistsByEmail(s.ctx, "exists@test.com")
	s.Require().NoError(err, "ExistsByEmail")
	s.Require().True(exists)

	notExists, err := s.repo.ExistsByEmail(s.ctx, "notexists@test.com")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

func (s *UserRepoSuite) TestExistsByNormalizedEmail() {
	s.mustCreateUser(&identitycore.User{Email: " Y.o.u.r.N.a.m.e+promo@GoogleMail.com. "})
	s.mustCreateUser(&identitycore.User{Email: "first.last+promo@qq.com"})

	exists, err := s.repo.ExistsByNormalizedEmail(s.ctx, "yourname@gmail.com")
	s.Require().NoError(err, "ExistsByNormalizedEmail")
	s.Require().True(exists)

	exists, err = s.repo.ExistsByNormalizedEmail(s.ctx, "first.last@qq.com")
	s.Require().NoError(err)
	s.Require().True(exists)

	dotDistinct, err := s.repo.ExistsByNormalizedEmail(s.ctx, "firstlast@qq.com")
	s.Require().NoError(err)
	s.Require().False(dotDistinct)

	notExists, err := s.repo.ExistsByNormalizedEmail(s.ctx, "other@example.com")
	s.Require().NoError(err)
	s.Require().False(notExists)
}

// TestCreateWithNormalizedEmailGuardSerializesProviderAliases 验证同一收件箱的别名并发注册时只有一个事务成功。
func (s *UserRepoSuite) TestCreateWithNormalizedEmailGuardSerializesProviderAliases() {
	candidates := []*identitycore.User{
		{
			Email:        "d.axis.2026+first@gmail.com",
			Username:     "gmail-alias-first",
			PasswordHash: "hash",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
		},
		{
			Email:        "da.xis.2026+second@googlemail.com.",
			Username:     "gmail-alias-second",
			PasswordHash: "hash",
			Role:         identitycore.RoleUser,
			Status:       billing.StatusActive,
		},
	}

	errs := make(chan error, len(candidates))
	var wg sync.WaitGroup
	for _, candidate := range candidates {
		candidate := candidate
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.repo.CreateWithNormalizedEmailGuard(
				s.ctx,
				candidate,
				identitycore.NormalizeRegistrationEmailAddress(candidate.Email),
			)
		}()
	}
	wg.Wait()
	close(errs)

	successes := 0
	conflicts := 0
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, identitycore.ErrEmailExists):
			conflicts++
		default:
			s.Require().NoError(err)
		}
	}
	s.Require().Equal(1, successes)
	s.Require().Equal(1, conflicts)
	count, err := s.client.User.Query().Count(s.ctx)
	s.Require().NoError(err)
	s.Require().Equal(1, count)
}

func (s *UserRepoSuite) TestUpdateWithNormalizedEmailGuard_RejectsConflict() {
	s.mustCreateUser(&identitycore.User{Email: "your.name@gmail.com"})
	other := s.mustCreateUser(&identitycore.User{Email: "second@example.com"})

	got, err := s.repo.GetByID(s.ctx, other.ID)
	s.Require().NoError(err)
	got.Email = "yourname+alias@googlemail.com."

	err = s.repo.UpdateWithNormalizedEmailGuard(s.ctx, got, identitycore.NormalizeRegistrationEmailAddress(got.Email), identitycore.UserUpdateFields{Email: true})
	s.Require().ErrorIs(err, identitycore.ErrEmailExists)

	reloaded, err := s.repo.GetByID(s.ctx, other.ID)
	s.Require().NoError(err)
	s.Require().Equal("second@example.com", reloaded.Email)
}

func (s *UserRepoSuite) TestUpdateWithNormalizedEmailGuard_AllowsSameUser() {
	user := s.mustCreateUser(&identitycore.User{Email: "your.name+seed@gmail.com"})

	got, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	got.Email = "yourname@gmail.com"

	err = s.repo.UpdateWithNormalizedEmailGuard(s.ctx, got, identitycore.NormalizeRegistrationEmailAddress(got.Email), identitycore.UserUpdateFields{Email: true})
	s.Require().NoError(err)

	reloaded, err := s.repo.GetByID(s.ctx, user.ID)
	s.Require().NoError(err)
	s.Require().Equal("yourname@gmail.com", reloaded.Email)
}

// --- RemoveGroupFromAllowedGroups ---

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups() {
	target := s.mustCreateGroup("target-42")
	other := s.mustCreateGroup("other-7")

	userA := s.mustCreateUser(&identitycore.User{
		Email:         "a1@example.com",
		AllowedGroups: []int64{target.ID, other.ID},
	})
	s.mustCreateUser(&identitycore.User{
		Email:         "a2@example.com",
		AllowedGroups: []int64{other.ID},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, target.ID)
	s.Require().NoError(err, "RemoveGroupFromAllowedGroups")
	s.Require().Equal(int64(1), affected, "expected 1 affected row")

	got, err := s.repo.GetByID(s.ctx, userA.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().NotContains(got.AllowedGroups, target.ID)
	s.Require().Contains(got.AllowedGroups, other.ID)
}

func (s *UserRepoSuite) TestRemoveGroupFromAllowedGroups_NoMatch() {
	groupA := s.mustCreateGroup("nomatch-a")
	groupB := s.mustCreateGroup("nomatch-b")

	s.mustCreateUser(&identitycore.User{
		Email:         "nomatch@test.com",
		AllowedGroups: []int64{groupA.ID, groupB.ID},
	})

	affected, err := s.repo.RemoveGroupFromAllowedGroups(s.ctx, 999999)
	s.Require().NoError(err)
	s.Require().Zero(affected, "expected no affected rows")
}

// --- GetFirstAdmin ---

func (s *UserRepoSuite) TestGetFirstAdmin() {
	admin1 := s.mustCreateUser(&identitycore.User{
		Email:  "admin1@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusActive,
	})
	s.mustCreateUser(&identitycore.User{
		Email:  "admin2@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(admin1.ID, got.ID, "GetFirstAdmin mismatch")
}

func (s *UserRepoSuite) TestGetFirstAdmin_NoAdmin() {
	s.mustCreateUser(&identitycore.User{
		Email:  "user@example.com",
		Role:   identitycore.RoleUser,
		Status: billing.StatusActive,
	})

	_, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().Error(err, "expected error when no admin exists")
}

func (s *UserRepoSuite) TestGetFirstAdmin_DisabledAdminIgnored() {
	s.mustCreateUser(&identitycore.User{
		Email:  "disabled@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusDisabled,
	})
	activeAdmin := s.mustCreateUser(&identitycore.User{
		Email:  "active@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusActive,
	})

	got, err := s.repo.GetFirstAdmin(s.ctx)
	s.Require().NoError(err, "GetFirstAdmin")
	s.Require().Equal(activeAdmin.ID, got.ID, "should return only active admin")
}

// --- Combined ---

func (s *UserRepoSuite) TestCRUD_And_Filters_And_AtomicUpdates() {
	user1 := s.mustCreateUser(&identitycore.User{
		Email:    "a@example.com",
		Username: "Alice",
		Role:     identitycore.RoleUser,
		Status:   billing.StatusActive,
		Balance:  10,
	})
	user2 := s.mustCreateUser(&identitycore.User{
		Email:    "b@example.com",
		Username: "Bob",
		Role:     identitycore.RoleAdmin,
		Status:   billing.StatusActive,
		Balance:  1,
	})
	s.mustCreateUser(&identitycore.User{
		Email:  "c@example.com",
		Role:   identitycore.RoleAdmin,
		Status: billing.StatusDisabled,
	})

	got, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal(user1.Email, got.Email, "GetByID email mismatch")

	gotByEmail, err := s.repo.GetByEmail(s.ctx, user2.Email)
	s.Require().NoError(err, "GetByEmail")
	s.Require().Equal(user2.ID, gotByEmail.ID, "GetByEmail ID mismatch")

	got.Username = "Alice2"
	s.Require().NoError(s.repo.Update(s.ctx, got, identitycore.UserUpdateFields{Username: true}), "Update")
	got2, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after update")
	s.Require().Equal("Alice2", got2.Username, "Update did not persist")

	s.Require().NoError(s.repo.UpdateBalance(s.ctx, user1.ID, 2.5), "UpdateBalance")
	got3, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateBalance")
	s.Require().InDelta(12.5, got3.Balance, 1e-6)

	deducted, err := s.repo.DeductBalance(s.ctx, user1.ID, 5)
	s.Require().NoError(err, "DeductBalance")
	s.Require().InDelta(5.0, deducted, 1e-6)
	got4, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after DeductBalance")
	s.Require().InDelta(7.5, got4.Balance, 1e-6)

	deducted, err = s.repo.DeductBalance(s.ctx, user1.ID, 999)
	s.Require().NoError(err, "DeductBalance should clamp to remaining balance")
	s.Require().InDelta(7.5, deducted, 1e-6)
	gotOverdraft, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after clamp")
	s.Require().InDelta(0.0, gotOverdraft.Balance, 1e-6, "Balance should clamp to zero")

	s.Require().NoError(s.repo.UpdateConcurrency(s.ctx, user1.ID, 3), "UpdateConcurrency")
	got5, err := s.repo.GetByID(s.ctx, user1.ID)
	s.Require().NoError(err, "GetByID after UpdateConcurrency")
	s.Require().Equal(user1.Concurrency+3, got5.Concurrency)

	params := pagination.PaginationParams{Page: 1, PageSize: 10}
	users, page, err := s.repo.ListWithFilters(s.ctx, params, identitycore.UserListFilters{Status: billing.StatusActive, Role: identitycore.RoleAdmin, Search: "b@"})
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total, "ListWithFilters total mismatch")
	s.Require().Len(users, 1, "ListWithFilters len mismatch")
	s.Require().Equal(user2.ID, users[0].ID, "ListWithFilters result mismatch")
}

// --- UpdateBalance/UpdateConcurrency 影响行数校验测试 ---

func (s *UserRepoSuite) TestUpdateBalance_NotFound() {
	err := s.repo.UpdateBalance(s.ctx, 999999, 10.0)
	s.Require().Error(err, "expected error for non-existent user")
	s.Require().ErrorIs(err, identitycore.ErrUserNotFound)
}

func (s *UserRepoSuite) TestUpdateConcurrency_NotFound() {
	err := s.repo.UpdateConcurrency(s.ctx, 999999, 5)
	s.Require().Error(err, "expected error for non-existent user")
	s.Require().ErrorIs(err, identitycore.ErrUserNotFound)
}

func (s *UserRepoSuite) TestDeductBalance_NotFound() {
	_, err := s.repo.DeductBalance(s.ctx, 999999, 5)
	s.Require().Error(err, "expected error for non-existent user")
	s.Require().ErrorIs(err, identitycore.ErrUserNotFound)
}
