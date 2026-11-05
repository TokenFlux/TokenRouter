package payment_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	_ "modernc.org/sqlite"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/enttest"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
)

// Plain public order verification exposes a small state response, while signed
// resume lookup supplies the financial fields needed by the purchase frontend.
// SQLite handler cases check the listed JSON keys and reject claims naming
// another user. Independent WeChat claim cases check restored amount and
// subscription selections, including rejection when the requested payment type
// disagrees with the resume claim.

// TestApplyWeChatPaymentResumeClaims applies saved WeChat payment claims to an
// unfinished create-order request. The request receives the supplied OpenID,
// parsed amount, subscription type, and plan ID.
func TestApplyWeChatPaymentResumeClaims(t *testing.T) {
	t.Parallel()

	req := paymenthttp.CreateOrderRequest{
		Amount:      0,
		PaymentType: payment.TypeWxpay,
		OrderType:   payment.OrderTypeBalance,
	}

	err := paymenthttp.UserApplyWeChatPaymentResumeClaims(&req, &payment.WeChatPaymentResumeClaims{
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

// TestApplyWeChatPaymentResumeClaimsRejectsPaymentTypeMismatch supplies WeChat
// claims to an Alipay create-order request. Applying those claims returns an
// error when their payment types disagree.
func TestApplyWeChatPaymentResumeClaimsRejectsPaymentTypeMismatch(t *testing.T) {
	t.Parallel()

	req := paymenthttp.CreateOrderRequest{
		PaymentType: payment.TypeAlipay,
	}

	err := paymenthttp.UserApplyWeChatPaymentResumeClaims(&req, &payment.WeChatPaymentResumeClaims{
		OpenID:      "openid-123",
		PaymentType: payment.TypeWxpay,
		Amount:      "12.50",
		OrderType:   payment.OrderTypeBalance,
	})
	if err == nil {
		t.Fatal("applyWeChatPaymentResumeClaims should reject mismatched payment types")
	}
}

// TestVerifyOrderPublicReturnsMinimalOrderState stores a pending order with
// financial and provider details in SQLite. Plain verification returns
// reference, status, paid flag, and timestamps while excluding the listed
// detailed fields.
func TestVerifyOrderPublicReturnsMinimalOrderState(t *testing.T) {
	t.Parallel()

	db, err := sql.Open("sqlite", "file:payment_handler_public_verify?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool cleanup before constructing Ent so later client cleanup
	// runs first; both callbacks discard close errors after the handler
	// observations finish.
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// Close the Ent wrapper before the earlier pool cleanup releases the
	// same database; this callback discards shutdown errors after the
	// response has been inspected.
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
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "HKD"}).
		Save(context.Background())
	require.NoError(t, err)

	core := payment.NewFulfillment(paymentpostgres.NewOrderStore(client), nil, nil, nil, nil, payment.FulfillmentRuntime{})
	resume := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	runtime := &payment.Runtime{OrderLifecycle: payment.NewOrderLifecycle(core, resume, nil)}
	h := paymenthttp.NewPaymentHandler(runtime, nil, nil)

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
		Code int            `json:"code"` // application response status
		Data map[string]any `json:"data"` // decoded public order fields
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, 0, resp.Code)
	require.Equal(t, "legacy-order-no", resp.Data["out_trade_no"])
	require.Equal(t, payment.OrderStatusPending, resp.Data["status"])
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

// TestPublicOrderStatusPaidIncludesRefundPending compares three supplied order
// statuses used by public responses. Pending is unpaid, while recharging and
// refund-pending are classified as paid by the helper.
func TestPublicOrderStatusPaidIncludesRefundPending(t *testing.T) {
	require.False(t, paymenthttp.UserPublicOrderStatusPaid(payment.OrderStatusPending))
	require.True(t, paymenthttp.UserPublicOrderStatusPaid(payment.OrderStatusRecharging))
	require.True(t, paymenthttp.UserPublicOrderStatusPaid(payment.OrderStatusRefundPending))
}

// TestResolveOrderPublicByResumeTokenReturnsFrontendContractFields signs a
// matching token for a paid balance order. Resolution returns the listed
// financial, currency, type, and timestamp fields while excluding the selected
// refund administration fields and plan ID.
func TestResolveOrderPublicByResumeTokenReturnsFrontendContractFields(t *testing.T) {
	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool cleanup before constructing Ent so later client cleanup
	// runs first; both callbacks discard close errors after the handler
	// observations finish.
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// Close the Ent wrapper before the earlier pool cleanup releases the
	// same database; this callback discards shutdown errors after the
	// response has been inspected.
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
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderSnapshot(map[string]any{"currency": "USD"}).
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(payment.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	core := payment.NewFulfillment(paymentpostgres.NewOrderStore(client), nil, nil, nil, nil, payment.FulfillmentRuntime{})
	resume := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	runtime := &payment.Runtime{OrderLifecycle: payment.NewOrderLifecycle(core, resume, nil)}
	h := paymenthttp.NewPaymentHandler(runtime, nil, nil)

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
		Code int            `json:"code"` // application response status
		Data map[string]any `json:"data"` // decoded public order fields
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
	require.Equal(t, payment.OrderStatusPaid, resp.Data["status"])
	require.Contains(t, resp.Data, "created_at")
	require.Contains(t, resp.Data, "expires_at")
	require.Contains(t, resp.Data, "refund_amount")
	require.NotContains(t, resp.Data, "refund_reason")
	require.NotContains(t, resp.Data, "refund_request_reason")
	require.NotContains(t, resp.Data, "refund_requested_by")
	require.NotContains(t, resp.Data, "refund_requested_at")
	require.NotContains(t, resp.Data, "plan_id")
}

// TestResolveOrderPublicByResumeTokenReturnsBadRequestForMismatchedToken signs
// a valid token whose user ID differs from the SQLite order. Resolution returns
// HTTP 400 with INVALID_RESUME_TOKEN in the error envelope.
func TestResolveOrderPublicByResumeTokenReturnsBadRequestForMismatchedToken(t *testing.T) {
	db, err := sql.Open("sqlite", "file:payment_handler_public_resolve_mismatch?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool cleanup before constructing Ent so later client cleanup
	// runs first; both callbacks discard close errors after the handler
	// observations finish.
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// Close the Ent wrapper before the earlier pool cleanup releases the
	// same database; this callback discards shutdown errors after the
	// response has been inspected.
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
		SetStatus(payment.OrderStatusPaid).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(context.Background())
	require.NoError(t, err)

	resumeSvc := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	token, err := resumeSvc.CreateToken(payment.ResumeTokenClaims{
		OrderID:            order.ID,
		UserID:             user.ID + 999,
		PaymentType:        payment.TypeAlipay,
		CanonicalReturnURL: "https://app.example.com/payment/result",
	})
	require.NoError(t, err)

	core := payment.NewFulfillment(paymentpostgres.NewOrderStore(client), nil, nil, nil, nil, payment.FulfillmentRuntime{})
	resume := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	runtime := &payment.Runtime{OrderLifecycle: payment.NewOrderLifecycle(core, resume, nil)}
	h := paymenthttp.NewPaymentHandler(runtime, nil, nil)

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
		Code    int    `json:"code"`    // application error status
		Reason  string `json:"reason"`  // stable error reason
		Message string `json:"message"` // response error message
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_RESUME_TOKEN", resp.Reason)
}

// TestVerifyOrderPublicRejectsBlankOutTradeNo sends whitespace as the local
// order reference through the HTTP handler. Verification returns HTTP 400 and
// the INVALID_OUT_TRADE_NO error reason.
func TestVerifyOrderPublicRejectsBlankOutTradeNo(t *testing.T) {
	db, err := sql.Open("sqlite", "file:payment_handler_public_verify_blank?mode=memory&cache=shared")
	require.NoError(t, err)
	// Register pool cleanup before constructing Ent so later client cleanup
	// runs first; both callbacks discard close errors after the handler
	// observations finish.
	t.Cleanup(func() { _ = db.Close() })

	_, err = db.Exec("PRAGMA foreign_keys = ON")
	require.NoError(t, err)

	drv := entsql.OpenDB(dialect.SQLite, db)
	client := enttest.NewClient(t, enttest.WithOptions(dbent.Driver(drv)))
	// Close the Ent wrapper before the earlier pool cleanup releases the
	// same database; this callback discards shutdown errors after the
	// response has been inspected.
	t.Cleanup(func() { _ = client.Close() })

	core := payment.NewFulfillment(paymentpostgres.NewOrderStore(client), nil, nil, nil, nil, payment.FulfillmentRuntime{})
	resume := payment.NewPaymentResumeService([]byte("0123456789abcdef0123456789abcdef"))
	runtime := &payment.Runtime{OrderLifecycle: payment.NewOrderLifecycle(core, resume, nil)}
	h := paymenthttp.NewPaymentHandler(runtime, nil, nil)

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
		Code   int    `json:"code"`   // application error status
		Reason string `json:"reason"` // stable error reason
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &resp))
	require.Equal(t, http.StatusBadRequest, resp.Code)
	require.Equal(t, "INVALID_OUT_TRADE_NO", resp.Reason)
}
