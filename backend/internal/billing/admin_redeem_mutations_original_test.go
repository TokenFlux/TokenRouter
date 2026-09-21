//go:build unit

package billing_test

import (
	"context"
	"errors"
	"testing"
	"time"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

type redeemRepoStub struct {
	getErrByID    map[int64]error
	codesByID     map[int64]*billing.RedeemCode
	deleteErrByID map[int64]error
	updateErr     error
	updatedCodes  []*billing.RedeemCode
	deletedIDs    []int64
	lockedGetIDs  []int64

	batchUpdateIDs    []int64
	batchUpdateFields billing.RedeemCodeBatchUpdateFields
	batchUpdateResult int64
	batchUpdateErr    error
	batchUpdateCalled bool
}

func (s *redeemRepoStub) Create(ctx context.Context, code *billing.RedeemCode) error {
	panic("unexpected Create call")
}

func (s *redeemRepoStub) CreateBatch(ctx context.Context, codes []billing.RedeemCode) error {
	panic("unexpected CreateBatch call")
}

func (s *redeemRepoStub) GetByID(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	if s.getErrByID != nil {
		if err, ok := s.getErrByID[id]; ok {
			return nil, err
		}
	}
	if s.codesByID != nil {
		if code, ok := s.codesByID[id]; ok {
			return code, nil
		}
	}
	return &billing.RedeemCode{ID: id}, nil
}

func (s *redeemRepoStub) GetByIDForUpdate(ctx context.Context, id int64) (*billing.RedeemCode, error) {
	s.lockedGetIDs = append(s.lockedGetIDs, id)
	return s.GetByID(ctx, id)
}

func (s *redeemRepoStub) GetByCode(ctx context.Context, code string) (*billing.RedeemCode, error) {
	panic("unexpected GetByCode call")
}

func (s *redeemRepoStub) GetByCodeForUpdate(ctx context.Context, code string) (*billing.RedeemCode, error) {
	panic("unexpected GetByCodeForUpdate call")
}

func (s *redeemRepoStub) Update(ctx context.Context, code *billing.RedeemCode) error {
	s.updatedCodes = append(s.updatedCodes, code)
	if s.codesByID == nil {
		s.codesByID = make(map[int64]*billing.RedeemCode)
	}
	cloned := *code
	s.codesByID[code.ID] = &cloned
	return s.updateErr
}

func (s *redeemRepoStub) BatchUpdate(ctx context.Context, ids []int64, fields billing.RedeemCodeBatchUpdateFields) (int64, error) {
	s.batchUpdateCalled = true
	s.batchUpdateIDs = append([]int64(nil), ids...)
	s.batchUpdateFields = fields
	if s.batchUpdateErr != nil {
		return 0, s.batchUpdateErr
	}
	if s.batchUpdateResult != 0 {
		return s.batchUpdateResult, nil
	}
	return int64(len(ids)), nil
}

func (s *redeemRepoStub) Delete(ctx context.Context, id int64) error {
	s.deletedIDs = append(s.deletedIDs, id)
	if s.deleteErrByID != nil {
		if err, ok := s.deleteErrByID[id]; ok {
			return err
		}
	}
	return nil
}

func (s *redeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	panic("unexpected Use call")
}

func (s *redeemRepoStub) CreateUsage(ctx context.Context, usage *billing.RedeemCodeUsage) error {
	return nil
}

func (s *redeemRepoStub) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*billing.RedeemCodeUsage, error) {
	return nil, nil
}

func (s *redeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *redeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *redeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]billing.RedeemCode, error) {
	panic("unexpected ListByUser call")
}

func (s *redeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]billing.RedeemCode, *pagination.PaginationResult, error) {
	panic("unexpected ListByUserPaginated call")
}

func (s *redeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	panic("unexpected SumPositiveBalanceByUser call")
}

func TestAdminService_DeleteRedeemCode_Success(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	err := svc.DeleteRedeemCode(context.Background(), 10)
	require.NoError(t, err)
	require.Equal(t, []int64{10}, repo.deletedIDs)
}

func TestAdminService_DeleteRedeemCode_Idempotent(t *testing.T) {
	repo := &redeemRepoStub{getErrByID: map[int64]error{999: billing.ErrRedeemCodeNotFound}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	err := svc.DeleteRedeemCode(context.Background(), 999)
	require.ErrorIs(t, err, billing.ErrRedeemCodeNotFound)
	require.Empty(t, repo.deletedIDs)
}

func TestAdminService_DeleteRedeemCode_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &redeemRepoStub{deleteErrByID: map[int64]error{1: deleteErr}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	err := svc.DeleteRedeemCode(context.Background(), 1)
	require.ErrorIs(t, err, deleteErr)
	require.Equal(t, []int64{1}, repo.deletedIDs)
}

func TestAdminService_BatchDeleteRedeemCodes_Success(t *testing.T) {
	repo := &redeemRepoStub{}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	deleted, err := svc.BatchDeleteRedeemCodes(context.Background(), []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, int64(3), deleted)
	require.Equal(t, []int64{1, 2, 3}, repo.deletedIDs)
}

func TestAdminService_BatchDeleteRedeemCodes_PartialFailures(t *testing.T) {
	repo := &redeemRepoStub{
		deleteErrByID: map[int64]error{
			2: errors.New("db error"),
		},
	}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	deleted, err := svc.BatchDeleteRedeemCodes(context.Background(), []int64{1, 2, 3})
	require.NoError(t, err)
	require.Equal(t, int64(2), deleted)
	require.Equal(t, []int64{1, 2, 3}, repo.deletedIDs)
}

func TestAdminService_UpdateRedeemCode_UnusedBalanceUpdatesValueLimitAndExpiry(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	value := 25.5
	maxUses := 3
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "R-1", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUnused, MaxUses: 1},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{
		Value:        &value,
		MaxUses:      &maxUses,
		ExpiresAt:    &expiresAt,
		ExpiresAtSet: true,
	})

	require.NoError(t, err)
	require.Equal(t, value, updated.Value)
	require.Equal(t, maxUses, updated.MaxUses)
	require.Equal(t, &expiresAt, updated.ExpiresAt)
	require.Equal(t, billing.StatusUnused, updated.Status)
	require.Len(t, repo.updatedCodes, 1)
	require.Equal(t, []int64{1}, repo.lockedGetIDs)
}

func TestAdminService_UpdateRedeemCode_UsedValueLocked(t *testing.T) {
	value := 50.0
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "R-1", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{Value: &value})

	require.Error(t, err)
	require.ErrorContains(t, err, "value or plan cannot be updated")
	require.Empty(t, repo.updatedCodes)
}

func TestAdminService_UpdateRedeemCode_RejectsMaxUsesBelowUsedCount(t *testing.T) {
	maxUses := 1
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "R-1", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusActive, MaxUses: 3, UsedCount: 2},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.Error(t, err)
	require.ErrorContains(t, err, "max_uses cannot be less than used_count")
	require.Empty(t, repo.updatedCodes)
}

func TestAdminService_UpdateRedeemCode_RestoresExhaustedCodeWhenLimitIncreases(t *testing.T) {
	maxUses := 2
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "R-1", Type: billing.RedeemTypeBalance, Value: 10, Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.NoError(t, err)
	require.Equal(t, billing.StatusActive, updated.Status)
	require.Equal(t, maxUses, updated.MaxUses)
}

func TestAdminService_UpdateRedeemCode_RestoresExpiredCodeWhenExpiryCleared(t *testing.T) {
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {
			ID:        1,
			Code:      "R-1",
			Type:      billing.RedeemTypeBalance,
			Value:     10,
			Status:    billing.StatusExpired,
			MaxUses:   3,
			UsedCount: 1,
			ExpiresAt: ptrTime(time.Now().Add(-time.Hour)),
		},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{ExpiresAtSet: true})

	require.NoError(t, err)
	require.Nil(t, updated.ExpiresAt)
	require.Equal(t, billing.StatusActive, updated.Status)
}

func TestAdminService_UpdateRedeemCode_InvitationKeepsExpiry(t *testing.T) {
	expiresAt := time.Now().Add(24 * time.Hour).Truncate(time.Second)
	maxUses := 0
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "INVITE-1", Type: billing.RedeemTypeInvitation, Status: billing.StatusUnused, MaxUses: 1},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	updated, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{
		MaxUses:      &maxUses,
		ExpiresAt:    &expiresAt,
		ExpiresAtSet: true,
	})

	require.NoError(t, err)
	require.Equal(t, 1, updated.MaxUses)
	require.Equal(t, &expiresAt, updated.ExpiresAt)
	require.Equal(t, billing.StatusUnused, updated.Status)
}

func TestAdminService_UpdateRedeemCode_RejectsSystemRecords(t *testing.T) {
	maxUses := 2
	repo := &redeemRepoStub{codesByID: map[int64]*billing.RedeemCode{
		1: {ID: 1, Code: "AFF-1", Type: "affiliate_balance", Value: 10, Status: billing.StatusUsed, MaxUses: 1, UsedCount: 1},
	}}
	svc := billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(nil), time.Now)

	_, err := svc.UpdateRedeemCode(context.Background(), 1, &billing.UpdateRedeemCodeInput{MaxUses: &maxUses})

	require.Error(t, err)
	require.ErrorContains(t, err, "system redeem records cannot be updated")
	require.Empty(t, repo.updatedCodes)
}
