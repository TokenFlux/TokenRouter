//go:build unit

package handler

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/ent/enttest"
	"github.com/BrandonVee/TokenRouter/internal/payment"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	_ "modernc.org/sqlite"
)

func TestApplyWeChatPaymentResumeClaims(t *testing.T) {
	t.Parallel()

	req := CreateOrderRequest{
		Amount:      0,
		PaymentType: payment.TypeWxpay,
		OrderType:   payment.OrderTypeBalance,
	}

	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeSubscription,
		PlanID:      7,
	})
	if err != nil {
		t.Fatalf("applyWeChatPaymentResumeClaims returned error: %v", err)
	}
	if req.OpenID != "openid-123" {
		t.Fatalf("openid = %q, want %q", req.OpenID, "openid-123")
	}
	if req.Amount != 12.5 {
		t.Fatalf("amount = %v, want 12.5", req.Amount)
	}
	if req.OrderType != payment.OrderTypeSubscription {
		t.Fatalf("order_type = %q, want %q", req.OrderType, payment.OrderTypeSubscription)
	}
	if req.PlanID != 7 {
		t.Fatalf("plan_id = %d, want 7", req.PlanID)
	}
}

func TestApplyWeChatPaymentResumeClaimsRejectsPaymentTypeMismatch(t *testing.T) {
	t.Parallel()

	req := CreateOrderRequest{
		PaymentType: payment.TypeAlipay,
	}

	err := applyWeChatPaymentResumeClaims(&req, &service.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeBalance,
	})
	if err == nil {
		t.Fatal("applyWeChatPaymentResumeClaims should reject mismatched payment types")
	}
}

func TestVerifyOrderPublicReturnsMinimalOrderState(t *testing.T) {
	t.Parallel()

	gin.SetMode(gin.TestMode)

	db, err := sql.Open("sqlite", "file:payment_handler_public_verify?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-verify@example.com").
		SetPasswordHash("hash").
		SetUsername("public-verify-user").
		Save(context.Background())
	require.NoError(t, err)

	_, err = client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(90.64).
		SetFeeRate(0.03).
		SetFeeFixed(1.1).
		SetFeeRateAmount(1.54).
		SetFeeAmount(2.64).
		SetRechargeCode("PUBLIC-VERIFY").
		SetOutTradeNo("legacy-order-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-verify").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "HKD"}).
		Save(context.Background())
	require.NoError(t, err)

	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/verify",
		bytes.NewBufferString(`{"out_trade_no":"legacy-order-no"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.VerifyOrderPublic(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "legacy-order-no", resp.Data["out_trade_no"])
	require.Equal(t, service.OrderStatusPending, resp.Data["status"])
	require.Equal(t, false, resp.Data["paid"])
	require.NotEmpty(t, resp.Data["created_at"])
	require.NotEmpty(t, resp.Data["expires_at"])
	require.NotContains(t, resp.Data, "id")
	require.NotContains(t, resp.Data, "amount")
	require.NotContains(t, resp.Data, "pay_amount")
	require.NotContains(t, resp.Data, "fee_rate")
	require.NotContains(t, resp.Data, "fee_fixed")
	require.NotContains(t, resp.Data, "fee_rate_amount")
	require.NotContains(t, resp.Data, "fee_amount")
	require.NotContains(t, resp.Data, "currency")
	require.NotContains(t, resp.Data, "payment_type")
	require.NotContains(t, resp.Data, "order_type")
	require.NotContains(t, resp.Data, "refund_amount")
}

func TestPublicOrderStatusPaidIncludesRefundPending(t *testing.T) {
	require.False(t, publicOrderStatusPaid(service.OrderStatusPending))
	require.True(t, publicOrderStatusPaid(service.OrderStatusRecharging))
	require.True(t, publicOrderStatusPaid(service.OrderStatusRefundPending))
}

func TestResolveOrderPublicByResumeTokenReturnsFrontendContractFields(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-resolve@example.com").
		SetPasswordHash("hash").
		SetUsername("public-resolve-user").
		Save(context.Background())
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(103).
		SetFeeRate(0.03).
		SetFeeFixed(1.25).
		SetFeeRateAmount(1.75).
		SetFeeAmount(3).
		SetRechargeCode("PUBLIC-RESOLVE").
		SetOutTradeNo("resolve-order-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-resolve").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "USD"}).
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := service.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(service.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	configSvc := service.NewPaymentConfigService(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, configSvc, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/resolve",
		bytes.NewBufferString(`{"resume_token":"`+token+`"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ResolveOrderPublicByResumeToken(ctx)

	require.Equal(t, http.StatusOK, recorder.Code)

	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, float64(order.ID), resp.Data["id"])
	require.Equal(t, "resolve-order-no", resp.Data["out_trade_no"])
	require.Equal(t, 100.0, resp.Data["amount"])
	require.Equal(t, 103.0, resp.Data["pay_amount"])
	require.Equal(t, 0.03, resp.Data["fee_rate"])
	require.Equal(t, 1.25, resp.Data["fee_fixed"])
	require.Equal(t, 1.75, resp.Data["fee_rate_amount"])
	require.Equal(t, 3.0, resp.Data["fee_amount"])
	require.Equal(t, "USD", resp.Data["currency"])
	require.Equal(t, payment.TypeAlipay, resp.Data["payment_type"])
	require.Equal(t, payment.OrderTypeBalance, resp.Data["order_type"])
	require.Equal(t, service.OrderStatusPaid, resp.Data["status"])
	require.Contains(t, resp.Data, "created_at")
	require.Contains(t, resp.Data, "expires_at")
	require.Contains(t, resp.Data, "refund_amount")
	require.NotContains(t, resp.Data, "refund_reason")
	require.NotContains(t, resp.Data, "refund_request_reason")
	require.NotContains(t, resp.Data, "refund_requested_by")
	require.NotContains(t, resp.Data, "refund_requested_at")
	require.NotContains(t, resp.Data, "plan_id")
}

func TestResolveOrderPublicByResumeTokenReturnsBadRequestForMismatchedToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Setenv("PAYMENT_RESUME_SIGNING_KEY", "0123456789abcdef0123456789abcdef")

	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve_mismatch?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	user, err := client.User.Create().
		SetEmail("public-resolve-mismatch@example.com").
		SetPasswordHash("hash").
		SetUsername("public-resolve-mismatch-user").
		Save(context.Background())
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(103).
		SetFeeRate(0.03).
		SetRechargeCode("PUBLIC-RESOLVE-MISMATCH").
		SetOutTradeNo("resolve-order-mismatch-no").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-public-resolve-mismatch").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(service.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := service.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(service.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID + 999,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	configSvc := service.NewPaymentConfigService(client, nil, []byte("0123456789abcdef0123456789abcdef"))
	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, configSvc, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/resolve",
		bytes.NewBufferString(`{"resume_token":"`+token+`"}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.ResolveOrderPublicByResumeToken(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code    int    `json:"code"`
		Reason  string `json:"reason"`
		Message string `json:"message"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_RESUME_TOKEN", resp.Reason)
}

func TestVerifyOrderPublicRejectsBlankOutTradeNo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	db, err := sql.Open("sqlite", "file:payment_handler_public_verify_blank?mode=memory&cache=shared")
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	t.Cleanup(func() { _ = client.Close() })

	paymentSvc := service.NewPaymentService(client, payment.NewRegistry(), nil, nil, nil, nil, nil, nil, nil)
	h := NewPaymentHandler(paymentSvc, nil)

	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(
		http.MethodPost,
		"/api/v1/payment/public/orders/verify",
		bytes.NewBufferString(`{"out_trade_no":"   "}`),
	)
	ctx.Request.Header.Set("Content-Type", "application/json")

	h.VerifyOrderPublic(ctx)

	require.Equal(t, http.StatusBadRequest, recorder.Code)

	var resp struct {
		Code   int    `json:"code"`
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_OUT_TRADE_NO", resp.Reason)
}
