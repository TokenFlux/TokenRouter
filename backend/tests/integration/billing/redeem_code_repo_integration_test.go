//go:build integration

package billing_test

import (
	"context"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/redeemcodeusage"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/suite"
)

type RedeemCodeRepoSuite struct {
	suite.Suite
	ctx    context.Context
	client *dbent.Client
	repo   *billingpostgres.RedeemStore
}

func (s *RedeemCodeRepoSuite) SetupTest() {
	s.ctx = context.Background()
	tx := entitlementTx(s.T())
	s.client = tx.Client()
	s.repo = billingpostgres.NewRedeemCodeRepository(s.client)
}

func TestRedeemCodeRepoSuite(t *testing.T) {
	suite.Run(t, new(RedeemCodeRepoSuite))
}

func (s *RedeemCodeRepoSuite) createUser(email string) *dbent.User {
	u, err := s.client.User.Create().
		SetEmail(email).
		SetPasswordHash("test-password-hash").
		Save(s.ctx)
	s.Require().NoError(err, "create user")
	return u
}

func (s *RedeemCodeRepoSuite) createPlan(name string) *dbent.SubscriptionPlan {
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
	return plan
}

func (s *RedeemCodeRepoSuite) createUsedCode(codeType, code string, userID int64, usedAt time.Time, planID *int64) *dbent.RedeemCode {
	create := s.client.RedeemCode.Create().
		SetCode(code).
		SetType(codeType).
		SetStatus(billing.StatusUsed).
		SetValue(0).
		SetNotes("").
		SetMaxUses(1).
		SetUsedCount(1).
		SetUsedBy(userID).
		SetUsedAt(usedAt)
	if planID != nil {
		create.SetPlanID(*planID)
	}

	redeemCode, err := create.Save(s.ctx)
	s.Require().NoError(err, "create used redeem code")

	_, err = s.client.RedeemCodeUsage.Create().
		SetRedeemCodeID(redeemCode.ID).
		SetUserID(userID).
		SetUsedAt(usedAt).
		Save(s.ctx)
	s.Require().NoError(err, "create redeem usage")

	return redeemCode
}

// --- Create / CreateBatch / GetByID / GetByCode ---

func (s *RedeemCodeRepoSuite) TestCreate() {
	code := &billing.RedeemCode{
		Code:   "TEST-CREATE",
		Type:   billing.RedeemTypeBalance,
		Value:  100,
		Status: billing.StatusUnused,
	}

	err := s.repo.Create(s.ctx, code)
	s.Require().NoError(err, "Create")
	s.Require().NotZero(code.ID, "expected ID to be set")

	got, err := s.repo.GetByID(s.ctx, code.ID)
	s.Require().NoError(err, "GetByID")
	s.Require().Equal("TEST-CREATE", got.Code)
}

func (s *RedeemCodeRepoSuite) TestCreateBatch() {
	codes := []billing.RedeemCode{
		{Code: "BATCH-1", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUnused},
		{Code: "BATCH-2", Type: billing.RedeemTypeBalance, Value: 20, Status: billing.StatusUnused},
	}

	err := s.repo.CreateBatch(s.ctx, codes)
	s.Require().NoError(err, "CreateBatch")

	got1, err := s.repo.GetByCode(s.ctx, "BATCH-1")
	s.Require().NoError(err)
	s.Require().Equal(float64(10), got1.Value)

	got2, err := s.repo.GetByCode(s.ctx, "BATCH-2")
	s.Require().NoError(err)
	s.Require().Equal(float64(20), got2.Value)
}

func (s *RedeemCodeRepoSuite) TestGetByID_NotFound() {
	_, err := s.repo.GetByID(s.ctx, 999999)
	s.Require().Error(err, "expected error for non-existent ID")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeNotFound)
}

func (s *RedeemCodeRepoSuite) TestGetByCode() {
	_, err := s.client.RedeemCode.Create().
		SetCode("GET-BY-CODE").
		SetType(billing.RedeemTypeBalance).
		SetStatus(billing.StatusUnused).
		SetValue(0).
		SetNotes("").
		Save(s.ctx)
	s.Require().NoError(err, "seed redeem code")

	got, err := s.repo.GetByCode(s.ctx, "GET-BY-CODE")
	s.Require().NoError(err, "GetByCode")
	s.Require().Equal("GET-BY-CODE", got.Code)
}

func (s *RedeemCodeRepoSuite) TestGetByCode_NotFound() {
	_, err := s.repo.GetByCode(s.ctx, "NON-EXISTENT")
	s.Require().Error(err, "expected error for non-existent code")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeNotFound)
}

// --- Delete ---

func (s *RedeemCodeRepoSuite) TestDelete() {
	created, err := s.client.RedeemCode.Create().
		SetCode("TO-DELETE").
		SetType(billing.RedeemTypeBalance).
		SetStatus(billing.StatusUnused).
		SetValue(0).
		SetNotes("").
		Save(s.ctx)
	s.Require().NoError(err)

	err = s.repo.Delete(s.ctx, created.ID)
	s.Require().NoError(err, "Delete")

	_, err = s.repo.GetByID(s.ctx, created.ID)
	s.Require().Error(err, "expected error after delete")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeNotFound)
}

// --- List / ListWithFilters ---

func (s *RedeemCodeRepoSuite) TestList() {
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "LIST-1", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "LIST-2", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))

	codes, page, err := s.repo.List(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10})
	s.Require().NoError(err, "List")
	s.Require().Len(codes, 2)
	s.Require().Equal(int64(2), page.Total)
}

func (s *RedeemCodeRepoSuite) TestListWithFilters_Type() {
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "TYPE-BAL", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "TYPE-SUB", Type: billing.RedeemTypeSubscription, Value: 0, Status: billing.StatusUnused}))

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, billing.RedeemTypeSubscription, "", "")
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().Equal(billing.RedeemTypeSubscription, codes[0].Type)
}

func (s *RedeemCodeRepoSuite) TestListWithFilters_Status() {
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "STAT-UNUSED", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{
		Code:      "STAT-USED",
		Type:      billing.RedeemTypeBalance,
		Value:     0,
		Status:    billing.StatusUsed,
		MaxUses:   1,
		UsedCount: 1,
	}))

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", billing.StatusUsed, "")
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().Equal(billing.StatusUsed, codes[0].Status)
}

func (s *RedeemCodeRepoSuite) TestListWithFilters_StatusExpiredIncludesInvitationExpiry() {
	past := time.Now().UTC().Add(-time.Hour)
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{
		Code:      "INVITE-EXP",
		Type:      billing.RedeemTypeInvitation,
		Status:    billing.StatusUnused,
		MaxUses:   1,
		ExpiresAt: &past,
	}))

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", billing.StatusExpired, "")

	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().Equal(billing.RedeemTypeInvitation, codes[0].Type)
	s.Require().Equal(billing.StatusExpired, codes[0].Status)
}

func (s *RedeemCodeRepoSuite) TestListWithFilters_Search() {
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "ALPHA-CODE", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))
	s.Require().NoError(s.repo.Create(s.ctx, &billing.RedeemCode{Code: "BETA-CODE", Type: billing.RedeemTypeBalance, Value: 0, Status: billing.StatusUnused}))

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "alpha")
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().Contains(codes[0].Code, "ALPHA")
}

func (s *RedeemCodeRepoSuite) TestListWithFilters_PlanPreload() {
	plan := s.createPlan(uniqueTestValue(s.T(), "plan-preload"))
	_, err := s.client.RedeemCode.Create().
		SetCode("WITH-GROUP").
		SetType(billing.RedeemTypeSubscription).
		SetStatus(billing.StatusUnused).
		SetValue(0).
		SetNotes("").
		SetPlanID(plan.ID).
		Save(s.ctx)
	s.Require().NoError(err)

	codes, _, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, "", "", "")
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().NotNil(codes[0].Plan, "expected Plan preload")
	s.Require().Equal(plan.ID, codes[0].Plan.ID)
}

// --- Update ---

func (s *RedeemCodeRepoSuite) TestUpdate() {
	code := &billing.RedeemCode{
		Code:   "UPDATE-ME",
		Type:   billing.RedeemTypeBalance,
		Value:  10,
		Status: billing.StatusUnused,
	}
	s.Require().NoError(s.repo.Create(s.ctx, code))

	code.Value = 50
	err := s.repo.Update(s.ctx, code)
	s.Require().NoError(err, "Update")

	got, err := s.repo.GetByID(s.ctx, code.ID)
	s.Require().NoError(err)
	s.Require().Equal(float64(50), got.Value)
}

// --- Use ---

func (s *RedeemCodeRepoSuite) TestUse() {
	user := s.createUser(uniqueTestValue(s.T(), "use") + "@example.com")
	code := &billing.RedeemCode{
		Code:    "USE-ME",
		Type:    billing.RedeemTypeBalance,
		Value:   0,
		Status:  billing.StatusUnused,
		MaxUses: 1,
	}
	s.Require().NoError(s.repo.Create(s.ctx, code))

	err := s.repo.Use(s.ctx, code.ID, user.ID)
	s.Require().NoError(err, "Use")

	got, err := s.repo.GetByID(s.ctx, code.ID)
	s.Require().NoError(err)
	s.Require().Equal(billing.StatusUsed, got.Status)
	s.Require().NotNil(got.UsedBy)
	s.Require().Equal(user.ID, *got.UsedBy)
	s.Require().NotNil(got.UsedAt)
}

func (s *RedeemCodeRepoSuite) TestUse_Idempotency() {
	user := s.createUser(uniqueTestValue(s.T(), "idem") + "@example.com")
	code := &billing.RedeemCode{
		Code:    "IDEM-CODE",
		Type:    billing.RedeemTypeBalance,
		Value:   0,
		Status:  billing.StatusUnused,
		MaxUses: 1,
	}
	s.Require().NoError(s.repo.Create(s.ctx, code))

	err := s.repo.Use(s.ctx, code.ID, user.ID)
	s.Require().NoError(err, "Use first time")

	// Second use should fail
	err = s.repo.Use(s.ctx, code.ID, user.ID)
	s.Require().Error(err, "Use expected error on second call")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeUsed)
}

func (s *RedeemCodeRepoSuite) TestUse_AlreadyUsed() {
	user := s.createUser(uniqueTestValue(s.T(), "already") + "@example.com")
	code := &billing.RedeemCode{
		Code:      "ALREADY-USED",
		Type:      billing.RedeemTypeBalance,
		Value:     0,
		Status:    billing.StatusUsed,
		MaxUses:   1,
		UsedCount: 1,
	}
	s.Require().NoError(s.repo.Create(s.ctx, code))

	err := s.repo.Use(s.ctx, code.ID, user.ID)
	s.Require().Error(err, "expected error for already used code")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeUsed)
}

func (s *RedeemCodeRepoSuite) TestUse_ExpiredInvitationRejected() {
	user := s.createUser(uniqueTestValue(s.T(), "expired-invite") + "@example.com")
	past := time.Now().UTC().Add(-time.Hour)
	code := &billing.RedeemCode{
		Code:      "EXPIRED-INVITE",
		Type:      billing.RedeemTypeInvitation,
		Status:    billing.StatusUnused,
		MaxUses:   1,
		ExpiresAt: &past,
	}
	s.Require().NoError(s.repo.Create(s.ctx, code))

	err := s.repo.Use(s.ctx, code.ID, user.ID)

	s.Require().Error(err, "expected error for expired invitation code")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeUsed)
}

// --- ListByUser ---

func (s *RedeemCodeRepoSuite) TestListByUser() {
	user := s.createUser(uniqueTestValue(s.T(), "listby") + "@example.com")
	base := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)

	s.createUsedCode(billing.RedeemTypeBalance, "USER-1", user.ID, base, nil)
	s.createUsedCode(billing.RedeemTypeBalance, "USER-2", user.ID, base.Add(1*time.Hour), nil)

	codes, err := s.repo.ListByUser(s.ctx, user.ID, 10)
	s.Require().NoError(err, "ListByUser")
	s.Require().Len(codes, 2)
	// Ordered by used_at DESC, so USER-2 first
	s.Require().Equal("USER-2", codes[0].Code)
	s.Require().Equal("USER-1", codes[1].Code)
}

func (s *RedeemCodeRepoSuite) TestListByUser_WithPlanPreload() {
	user := s.createUser(uniqueTestValue(s.T(), "grp") + "@example.com")
	plan := s.createPlan(uniqueTestValue(s.T(), "plan-listby"))
	s.createUsedCode(billing.RedeemTypeSubscription, "WITH-GRP", user.ID, time.Now(), &plan.ID)

	codes, err := s.repo.ListByUser(s.ctx, user.ID, 10)
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
	s.Require().NotNil(codes[0].Plan)
	s.Require().Equal(plan.ID, codes[0].Plan.ID)
}

func (s *RedeemCodeRepoSuite) TestListByUser_DefaultLimit() {
	user := s.createUser(uniqueTestValue(s.T(), "deflimit") + "@example.com")
	s.createUsedCode(billing.RedeemTypeBalance, "DEF-LIM", user.ID, time.Now(), nil)

	// limit <= 0 should default to 10
	codes, err := s.repo.ListByUser(s.ctx, user.ID, 0)
	s.Require().NoError(err)
	s.Require().Len(codes, 1)
}

// --- Combined original test ---

func (s *RedeemCodeRepoSuite) TestCreateBatch_Filters_Use_Idempotency_ListByUser() {
	user := s.createUser(uniqueTestValue(s.T(), "rc") + "@example.com")
	plan := s.createPlan(uniqueTestValue(s.T(), "plan-rc"))
	planID := plan.ID

	codes := []billing.RedeemCode{
		{Code: "CODEA", Type: billing.RedeemTypeBalance, Value: 1, Status: billing.StatusUnused, Notes: ""},
		{Code: "CODEB", Type: billing.RedeemTypeSubscription, Value: 0, Status: billing.StatusUnused, Notes: "", PlanID: &planID},
	}
	s.Require().NoError(s.repo.CreateBatch(s.ctx, codes), "CreateBatch")

	list, page, err := s.repo.ListWithFilters(s.ctx, pagination.PaginationParams{Page: 1, PageSize: 10}, billing.RedeemTypeSubscription, billing.StatusUnused, "code")
	s.Require().NoError(err, "ListWithFilters")
	s.Require().Equal(int64(1), page.Total)
	s.Require().Len(list, 1)
	s.Require().NotNil(list[0].Plan, "expected Plan preload")
	s.Require().Equal(plan.ID, list[0].Plan.ID)

	codeB, err := s.repo.GetByCode(s.ctx, "CODEB")
	s.Require().NoError(err, "GetByCode")
	s.Require().NoError(s.repo.Use(s.ctx, codeB.ID, user.ID), "Use")
	err = s.repo.Use(s.ctx, codeB.ID, user.ID)
	s.Require().Error(err, "Use expected error on second call")
	s.Require().ErrorIs(err, billing.ErrRedeemCodeUsed)

	codeA, err := s.repo.GetByCode(s.ctx, "CODEA")
	s.Require().NoError(err, "GetByCode")

	// Use fixed time instead of time.Sleep for deterministic ordering.
	codeBUsedAt := time.Date(2025, 1, 1, 12, 0, 0, 0, time.UTC)
	_, err = s.client.RedeemCode.UpdateOneID(codeB.ID).
		SetUsedAt(codeBUsedAt).
		Save(s.ctx)
	s.Require().NoError(err)
	_, err = s.client.RedeemCodeUsage.Update().
		Where(
			redeemcodeusage.RedeemCodeIDEQ(codeB.ID),
			redeemcodeusage.UserIDEQ(user.ID),
		).
		SetUsedAt(codeBUsedAt).
		Save(s.ctx)
	s.Require().NoError(err)
	s.Require().NoError(s.repo.Use(s.ctx, codeA.ID, user.ID), "Use codeA")
	codeAUsedAt := time.Date(2025, 1, 1, 13, 0, 0, 0, time.UTC)
	_, err = s.client.RedeemCode.UpdateOneID(codeA.ID).
		SetUsedAt(codeAUsedAt).
		Save(s.ctx)
	s.Require().NoError(err)
	_, err = s.client.RedeemCodeUsage.Update().
		Where(
			redeemcodeusage.RedeemCodeIDEQ(codeA.ID),
			redeemcodeusage.UserIDEQ(user.ID),
		).
		SetUsedAt(codeAUsedAt).
		Save(s.ctx)
	s.Require().NoError(err)

	used, err := s.repo.ListByUser(s.ctx, user.ID, 10)
	s.Require().NoError(err, "ListByUser")
	s.Require().Len(used, 2, "expected 2 used codes")
	s.Require().Equal("CODEA", used[0].Code, "expected newest used code first")
}
