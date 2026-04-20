package admin

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
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
