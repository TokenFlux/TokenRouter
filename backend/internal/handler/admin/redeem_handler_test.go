package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/pagination"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newCreateAndRedeemHandler creates a RedeemHandler with a non-nil (but minimal)
// RedeemService so that CreateAndRedeem's nil guard passes and we can test the
// parameter-validation layer that runs before any service call.
func newCreateAndRedeemHandler() *RedeemHandler {
	return &RedeemHandler{
		adminService:  newStubAdminService(),
		redeemService: &service.RedeemService{}, // non-nil to pass nil guard
	}
}

// postCreateAndRedeemValidation calls CreateAndRedeem and returns the response
// status code. For cases that pass validation and proceed into the service layer,
// a panic may occur (because RedeemService internals are nil); this is expected
// and treated as "validation passed" (returns 0 to indicate panic).
func postCreateAndRedeemValidation(t *testing.T, handler *RedeemHandler, body any) (code int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)

	jsonBytes, err := json.Marshal(body)
	require.NoError(t, err)
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/v1/admin/redeem-codes/create-and-redeem", bytes.NewReader(jsonBytes))
	c.Request.Header.Set("Content-Type", "application/json")

	defer func() {
		if r := recover(); r != nil {
			// Panic means we passed validation and entered service layer (expected for minimal stub).
			code = 0
		}
	}()
	handler.CreateAndRedeem(c)
	return w.Code
}

func TestCreateAndRedeem_TypeDefaultsToBalance(t *testing.T) {
	// 不传 type 字段时应默认 balance，不触发 subscription 校验。
	// 验证通过后进入 service 层会 panic（返回 0），说明默认值生效。
	h := newCreateAndRedeemHandler()
	code := postCreateAndRedeemValidation(t, h, map[string]any{
		"code":    "test-balance-default",
		"value":   10.0,
		"user_id": 1,
	})

	assert.NotEqual(t, http.StatusBadRequest, code,
		"omitting type should default to balance and pass validation")
}

func TestCreateAndRedeem_SubscriptionRequiresPlanID(t *testing.T) {
	h := newCreateAndRedeemHandler()
	code := postCreateAndRedeemValidation(t, h, map[string]any{
		"code":    "test-sub-no-plan",
		"type":    "subscription",
		"value":   29.9,
		"user_id": 1,
	})

	assert.Equal(t, http.StatusBadRequest, code)
}

func TestCreateAndRedeem_SubscriptionRequiresPositivePlanID(t *testing.T) {
	h := newCreateAndRedeemHandler()

	t.Run("zero", func(t *testing.T) {
		code := postCreateAndRedeemValidation(t, h, map[string]any{
			"code":    "test-sub-bad-plan-zero",
			"type":    "subscription",
			"value":   29.9,
			"user_id": 1,
			"plan_id": 0,
		})

		assert.Equal(t, http.StatusBadRequest, code)
	})

	t.Run("negative", func(t *testing.T) {
		code := postCreateAndRedeemValidation(t, h, map[string]any{
			"code":    "test-sub-bad-plan-negative",
			"type":    "subscription",
			"value":   29.9,
			"user_id": 1,
			"plan_id": -7,
		})

		assert.Equal(t, http.StatusBadRequest, code)
	})
}

func TestCreateAndRedeem_SubscriptionValidParamsPassValidation(t *testing.T) {
	planID := int64(5)
	h := newCreateAndRedeemHandler()
	code := postCreateAndRedeemValidation(t, h, map[string]any{
		"code":    "test-sub-valid",
		"type":    "subscription",
		"value":   29.9,
		"user_id": 1,
		"plan_id": planID,
	})

	assert.NotEqual(t, http.StatusBadRequest, code,
		"valid subscription params should pass validation")
}

func TestCreateAndRedeem_BalanceIgnoresSubscriptionFields(t *testing.T) {
	h := newCreateAndRedeemHandler()
	// balance 类型不传 plan_id，不应报 400
	code := postCreateAndRedeemValidation(t, h, map[string]any{
		"code":    "test-balance-no-extras",
		"type":    "balance",
		"value":   50.0,
		"user_id": 1,
	})

	assert.NotEqual(t, http.StatusBadRequest, code,
		"balance type should not require plan_id")
}

func TestResolveRedeemCodeExpiresAt_FromDays(t *testing.T) {
	days := 3

	expiresAt, err := resolveRedeemCodeExpiresAt(nil, &days)

	require.NoError(t, err)
	require.NotNil(t, expiresAt)
	require.WithinDuration(t, time.Now().UTC().AddDate(0, 0, days), *expiresAt, 2*time.Second)
}

func TestResolveRedeemCodeExpiresAt_FromUnixSeconds(t *testing.T) {
	futureUnix := time.Now().UTC().Add(time.Hour).Unix()

	expiresAt, err := resolveRedeemCodeExpiresAt(&futureUnix, nil)

	require.NoError(t, err)
	require.NotNil(t, expiresAt)
	require.Equal(t, futureUnix, expiresAt.Unix())
	require.Equal(t, time.UTC, expiresAt.Location())
}

func TestResolveRedeemCodeExpiresAt_ZeroMeansNoExpiry(t *testing.T) {
	zero := int64(0)

	expiresAt, err := resolveRedeemCodeExpiresAt(&zero, nil)

	require.NoError(t, err)
	require.Nil(t, expiresAt)
}

func TestResolveRedeemCodeExpiresAt_RejectsPastAbsoluteTime(t *testing.T) {
	pastUnix := time.Now().UTC().Add(-time.Minute).Unix()

	expiresAt, err := resolveRedeemCodeExpiresAt(&pastUnix, nil)

	require.Error(t, err)
	require.Nil(t, expiresAt)
}

func TestResolveRedeemCodeExpiresAt_RejectsNonPositiveDays(t *testing.T) {
	days := 0

	expiresAt, err := resolveRedeemCodeExpiresAt(nil, &days)

	require.Error(t, err)
	require.Nil(t, expiresAt)
}

func TestResolveRedeemCodeExpiresAt_RejectsConflictingInputs(t *testing.T) {
	futureUnix := time.Now().UTC().Add(time.Hour).Unix()
	days := 3

	expiresAt, err := resolveRedeemCodeExpiresAt(&futureUnix, &days)

	require.Error(t, err)
	require.Nil(t, expiresAt)
}

func TestGenerate_AcceptsInvitationExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	adminSvc := newStubAdminService()
	h := NewRedeemHandler(adminSvc, nil)
	futureUnix := time.Now().UTC().Add(time.Hour).Unix()

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, err := json.Marshal(map[string]any{
		"count":      1,
		"type":       "invitation",
		"value":      0,
		"expires_at": futureUnix,
	})
	require.NoError(t, err)
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/v1/admin/redeem-codes/generate", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.Generate(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.NotNil(t, adminSvc.lastGenerateRedeemCodes)
	require.NotNil(t, adminSvc.lastGenerateRedeemCodes.ExpiresAt)
	require.Equal(t, futureUnix, adminSvc.lastGenerateRedeemCodes.ExpiresAt.Unix())
}

func TestRedeemBatchUpdate_NullExpiresAtClearsExpiry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	status := service.StatusDisabled
	notes := "批量维护"
	repo := &batchUpdateRedeemRepoStub{}
	redeemSvc := service.NewRedeemService(repo, nil, nil, nil, nil, nil, nil, nil)
	h := NewRedeemHandler(newStubAdminService(), redeemSvc)

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	body, err := json.Marshal(map[string]any{
		"ids": []int64{1, 2},
		"fields": map[string]any{
			"status":     status,
			"expires_at": nil,
			"notes":      notes,
		},
	})
	require.NoError(t, err)
	c.Request, _ = http.NewRequest(http.MethodPost, "/api/v1/admin/redeem-codes/batch-update", bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")

	h.BatchUpdate(c)

	require.Equal(t, http.StatusOK, w.Code)
	require.Equal(t, []int64{1, 2}, repo.ids)
	require.Equal(t, &status, repo.fields.Status)
	require.True(t, repo.fields.ExpiresAt.Set)
	require.Nil(t, repo.fields.ExpiresAt.Value)
	require.Equal(t, &notes, repo.fields.Notes)
}

type batchUpdateRedeemRepoStub struct {
	ids    []int64
	fields service.RedeemCodeBatchUpdateFields
}

func (s *batchUpdateRedeemRepoStub) BatchUpdate(ctx context.Context, ids []int64, fields service.RedeemCodeBatchUpdateFields) (int64, error) {
	s.ids = append([]int64(nil), ids...)
	s.fields = fields
	return int64(len(ids)), nil
}

func (s *batchUpdateRedeemRepoStub) Create(ctx context.Context, code *service.RedeemCode) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) CreateBatch(ctx context.Context, codes []service.RedeemCode) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) GetByID(ctx context.Context, id int64) (*service.RedeemCode, error) {
	return nil, service.ErrRedeemCodeNotFound
}

func (s *batchUpdateRedeemRepoStub) GetByIDForUpdate(ctx context.Context, id int64) (*service.RedeemCode, error) {
	return nil, service.ErrRedeemCodeNotFound
}

func (s *batchUpdateRedeemRepoStub) GetByCode(ctx context.Context, code string) (*service.RedeemCode, error) {
	return nil, service.ErrRedeemCodeNotFound
}

func (s *batchUpdateRedeemRepoStub) GetByCodeForUpdate(ctx context.Context, code string) (*service.RedeemCode, error) {
	return nil, service.ErrRedeemCodeNotFound
}

func (s *batchUpdateRedeemRepoStub) Update(ctx context.Context, code *service.RedeemCode) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) Delete(ctx context.Context, id int64) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) Use(ctx context.Context, id, userID int64) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) CreateUsage(ctx context.Context, usage *service.RedeemCodeUsage) error {
	return errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) GetUsageByRedeemCodeAndUser(ctx context.Context, redeemCodeID, userID int64) (*service.RedeemCodeUsage, error) {
	return nil, errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]service.RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, codeType, status, search string) ([]service.RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) ListByUser(ctx context.Context, userID int64, limit int) ([]service.RedeemCode, error) {
	return nil, errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) ListByUserPaginated(ctx context.Context, userID int64, params pagination.PaginationParams, codeType string) ([]service.RedeemCode, *pagination.PaginationResult, error) {
	return nil, nil, errors.New("not implemented")
}

func (s *batchUpdateRedeemRepoStub) SumPositiveBalanceByUser(ctx context.Context, userID int64) (float64, error) {
	return 0, errors.New("not implemented")
}
