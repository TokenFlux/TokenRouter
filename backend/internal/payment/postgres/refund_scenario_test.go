package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	paymentadapter "github.com/TokenFlux/TokenRouter/internal/payment/provider"
	paymenttestkit "github.com/TokenFlux/TokenRouter/internal/payment/testkit"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	sqlitetest "github.com/TokenFlux/TokenRouter/internal/testutil/sqlite"
)

// Refund recovery combines stored order state with prepared or pending audit
// data before channel observations can finalize local results. SQLite
// scenarios exercise identity refusal, recorded deduction quantities, pending
// compensation, success/failure classification and rejection of a stale second
// finalization.
//
// Balance fixtures normally report quantities through callbacks, so those
// counters and receipt fields do not prove a wallet mutation. One failure
// fixture instead updates the user through the transaction in context and then
// returns an error, allowing persisted balance and order rollback to be
// asserted. Preparation helpers write row metadata and audit records
// separately and apply no rights themselves. Provider doubles return local
// observations or empty no-op results, keeping the scenarios limited to
// workflow and SQLite persistence rather than PostgreSQL locking or remote
// refund execution.

// refundBalanceFixture supplies selected balance callbacks while RefundStore
// owns order and audit transactions.
type refundBalanceFixture struct {
	// RefundRights supplies unoverridden methods; balance-only fixtures leave
	// this embedded interface unset, and invoking one of its promoted methods
	// would panic.
	payment.RefundRights
	// user is returned directly by the runtime lookup; a nil field yields a fresh
	// zero-valued user when the fixture itself exists.
	user *payment.RefundUser
	// updateBalanceFn handles compensation when supplied; an absent callback
	// reports success without changing wallet storage in this fixture.
	updateBalanceFn func(context.Context, int64, float64) error
	// deductBalanceFn runs only when deductBalanceResultFn is absent and reports
	// zero deducted quantity with its returned error.
	deductBalanceFn func(context.Context, int64, float64) error
	// deductBalanceResultFn has first deduction precedence and returns its
	// supplied quantity and error, including callbacks that use TxFromContext for
	// actual SQLite mutation.
	deductBalanceResultFn func(context.Context, int64, float64) (float64, error)
}

// refundProviderTestDouble advertises Stripe while returning empty successful
// no-op results for base channel operations.
type refundProviderTestDouble struct{}

// refundQueryProviderTestDouble adds a supplied refund-status observation to
// the no-op Stripe fixture.
type refundQueryProviderTestDouble struct {
	refundProviderTestDouble // embedded base channel fixture
	// refundResponse is returned directly by QueryRefund; callers retain the
	// supplied pointer and keep it stable during synchronous workflow reads.
	refundResponse *payment.RefundResponse
}

// TestValidateRefundRequestRejectsLegacyGuessedProviderInstance stores a
// completed unpinned Alipay order and an otherwise refund-enabled instance. It
// expects USER_REFUND_DISABLED because user refund validation requires
// historical binding rather than a guessed compatible row.
func TestValidateRefundRequestRejectsLegacyGuessedProviderInstance(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("refund-legacy@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-legacy-user").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("alipay-refund-instance").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetAllowUserRefund(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("REFUND-LEGACY-ORDER").
		SetOutTradeNo("sub2_refund_legacy_order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-legacy-refund").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	svc := newRefundWorkflowFixture(client, nil, nil, nil)

	_, err = svc.ValidateRefundRequest(ctx, order.ID, user.ID)
	require.Error(t, err)
	require.Equal(t, "USER_REFUND_DISABLED", apperror.Reason(err))
}

// TestPrepareRefundRejectsLegacyGuessedProviderInstance stores a completed
// unpinned order with a compatible refund-enabled Alipay instance. It expects
// administrator preparation to return no plan or result and the
// REFUND_DISABLED reason.
func TestPrepareRefundRejectsLegacyGuessedProviderInstance(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("refund-legacy-admin@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-legacy-admin-user").
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("alipay-refund-admin-instance").
		SetConfig("{}").
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetAllowUserRefund(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(188).
		SetPayAmount(188).
		SetFeeRate(0).
		SetRechargeCode("REFUND-LEGACY-ADMIN-ORDER").
		SetOutTradeNo("sub2_refund_legacy_admin_order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-legacy-admin-refund").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	svc := newRefundWorkflowFixture(client, nil, nil, nil)

	plan, result, err := svc.PrepareRefund(ctx, order.ID, 0, "", false, false)
	require.Nil(t, plan)
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, "REFUND_DISABLED", apperror.Reason(err))
}

// TestPrepDeductBalanceRequiresForceWhenBalanceIsInsufficient compares
// insufficient, forced, negative and exactly sufficient user balances. It
// checks the force warning or planned available deduction, without performing
// a wallet write in these planning cases.
func TestPrepDeductBalanceRequiresForceWhenBalanceIsInsufficient(t *testing.T) {
	for _, tc := range []struct {
		name        string  // named balance planning case
		balance     float64 // supplied available balance points
		force       bool    // supplied force deduction choice
		wantDeduct  float64 // expected planned deduction points
		wantWarning bool    // expected force warning flag
	}{
		{name: "insufficient balance", balance: 40, wantWarning: true},
		{name: "forced insufficient balance", balance: 40, force: true, wantDeduct: 40},
		{name: "negative balance with force", balance: -10, force: true},
		{name: "equal balance", balance: 100, wantDeduct: 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := &payment.RefundPlan{RefundAmount: 100}
			svc := newRefundWorkflowFixture(nil, &refundBalanceFixture{user: &payment.RefundUser{Balance: tc.balance}}, nil, nil)

			result := svc.PrepDeduct(context.Background(), &payment.Order{
				UserID:    1,
				OrderType: payment.OrderTypeBalance,
			}, plan, tc.force)

			if tc.wantWarning {
				require.NotNil(t, result)
				require.False(t, result.Success)
				require.True(t, result.RequireForce)
				require.Equal(t, "user balance is insufficient for deduction, use force", result.Warning)
				require.Zero(t, plan.BalanceToDeduct)
				return
			}
			require.Nil(t, result)
			require.Equal(t, payment.DeductionTypeBalance, plan.DeductionType)
			require.Equal(t, tc.wantDeduct, plan.BalanceToDeduct)
		})
	}
}

// TestExecuteRefundUsesActualAvailableBalanceDeduction supplies a callback
// reporting twenty-five from a planned hundred-point deduction and an order
// with empty channel reference. It expects that reported quantity in plan,
// result and success audit for local-only refund completion.
func TestExecuteRefundUsesActualAvailableBalanceDeduction(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	user, err := client.User.Create().
		SetEmail("refund-execute-clamp@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-execute-clamp").
		Save(ctx)
	require.NoError(t, err)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("REFUND-EXECUTE-CLAMP").
		SetOutTradeNo("refund_execute_clamp").
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	repo := &refundBalanceFixture{deductBalanceResultFn: func(_ context.Context, id int64, amount float64) (float64, error) {
		require.Equal(t, user.ID, id)
		require.Equal(t, 100.0, amount)
		return 25, nil
	}}
	plan := &payment.RefundPlan{
		OrderID: order.ID, Order: paymentpostgres.OrderFromEntity(order), RefundAmount: 100, GatewayAmount: 100,
		Reason: "concurrent spend", Force: true, DeductBalance: true, DeductionType: payment.DeductionTypeBalance, BalanceToDeduct: 100,
	}

	result, err := newRefundWorkflowFixture(client, repo, nil, nil).ExecuteRefund(ctx, plan)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, 25.0, plan.BalanceToDeduct)
	require.Equal(t, 25.0, result.BalanceDeducted)
	audit, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Only(ctx)
	require.NoError(t, err)
	require.Contains(t, audit.Detail, `"balanceDeducted":25`)
}

// TestGwRefundRejectsAlipayMerchantIdentitySnapshotMismatch binds an Alipay
// instance whose current app identity differs from the saved snapshot. It
// expects identity validation to fail before refund submission can use those
// credentials.
func TestGwRefundRejectsAlipayMerchantIdentitySnapshotMismatch(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("refund-snapshot-mismatch@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-snapshot-mismatch-user").
		Save(ctx)
	require.NoError(t, err)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeAlipay).
		SetName("alipay-refund-mismatch-instance").
		SetConfig(paymenttestkit.LegacyConfig(t, "alipay:runtime-mismatch")).
		SetSupportedTypes("alipay").
		SetEnabled(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	// Encode the numeric instance identity as the decimal text shared by the
	// order pin and its merchant snapshot.
	instID := strconv.FormatInt(inst.ID, 10)
	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(88).
		SetPayAmount(88).
		SetFeeRate(0).
		SetRechargeCode("REFUND-SNAPSHOT-MISMATCH-ORDER").
		SetOutTradeNo("sub2_refund_snapshot_mismatch_order").
		SetPaymentType(payment.TypeAlipay).
		SetPaymentTradeNo("trade-refund-snapshot-mismatch").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusCompleted).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderInstanceID(instID).
		SetProviderKey(payment.TypeAlipay).
		SetProviderSnapshot(map[string]any{
			"schema_version":       2,
			"provider_instance_id": instID,
			"provider_key":         payment.TypeAlipay,
			"merchant_app_id":      "expected-alipay-app",
		}).
		Save(ctx)
	require.NoError(t, err)

	svc := newRefundWorkflowFixture(client, nil, paymenttestkit.LegacyLoadBalancer(client), nil)

	_, err = svc.GwRefund(ctx, &payment.RefundPlan{
		OrderID:       order.ID,
		Order:         paymentpostgres.OrderFromEntity(order),
		RefundAmount:  order.Amount,
		GatewayAmount: order.Amount,
		Reason:        "snapshot mismatch",
	})
	require.ErrorContains(t, err, "alipay app_id mismatch")
}

// TestCalculateGatewayRefundAmountUsesCurrencyPrecision supplies proportional
// and full KWD refunds plus a JPY proportional amount. It expects
// three-decimal KWD and whole-unit JPY rounding within the stated numeric
// tolerance.
func TestCalculateGatewayRefundAmountUsesCurrencyPrecision(t *testing.T) {
	// Use a tight floating tolerance for currency-rounded gateway amount
	// assertions.
	require.InDelta(t, 6.173, payment.CalculateGatewayRefundAmount(100, 12.345, 50, "KWD"), 1e-12)
	require.InDelta(t, 12.345, payment.CalculateGatewayRefundAmount(100, 12.345, 100, "KWD"), 1e-12)
	require.InDelta(t, 52, payment.CalculateGatewayRefundAmount(100, 103, 50, "JPY"), 1e-12)
}

// TestFormatGatewayRefundAmountUsesOrderCurrency supplies KWD in the saved
// order provider snapshot and a three-decimal amount. It expects formatted
// gateway money to retain that currency-specific precision.
func TestFormatGatewayRefundAmountUsesOrderCurrency(t *testing.T) {
	order := &payment.Order{
		ProviderSnapshot: map[string]any{
			"currency": "KWD",
		},
	}

	require.Equal(t, "12.345", payment.FormatGatewayRefundAmount(12.345, order))
}

// TestValidateRefundProviderResponseAcceptsPending compares pending, success,
// failure and nil channel responses. It accepts progress or success and
// rejects the failed or absent observations before workflow finalization.
func TestValidateRefundProviderResponseAcceptsPending(t *testing.T) {
	require.NoError(t, payment.ValidateRefundProviderResponse(&payment.RefundResponse{Status: payment.ProviderStatusPending}))
	require.NoError(t, payment.ValidateRefundProviderResponse(&payment.RefundResponse{Status: payment.ProviderStatusSuccess}))
	require.Error(t, payment.ValidateRefundProviderResponse(&payment.RefundResponse{Status: payment.ProviderStatusFailed}))
	require.Error(t, payment.ValidateRefundProviderResponse(nil))
}

// TestFinishRefundPendingMarksOrderPendingAndRollsBackDeduction seeds
// preparation and supplies a pending channel response with a compensation
// counter. It expects recorded compensation, cleared plan quantity, pending
// local state and one pending audit with no success audit.
func TestFinishRefundPendingMarksOrderPendingAndRollsBackDeduction(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)

	user, err := client.User.Create().
		SetEmail("refund-pending@example.com").
		SetPasswordHash("hash").
		SetUsername("refund-pending-user").
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("REFUND-PENDING-ORDER").
		SetOutTradeNo("sub2_refund_pending_order").
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("pi_refund_pending").
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusRefunding).
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		Save(ctx)
	require.NoError(t, err)

	var rolledBack float64
	userRepo := &refundBalanceFixture{}
	userRepo.updateBalanceFn = func(ctx context.Context, id int64, amount float64) error {
		require.Equal(t, user.ID, id)
		rolledBack += amount
		return nil
	}
	svc := newRefundWorkflowFixture(client, userRepo, nil, nil)
	plan := &payment.RefundPlan{
		OrderID:         order.ID,
		Order:           paymentpostgres.OrderFromEntity(order),
		RefundAmount:    40,
		GatewayAmount:   40,
		Reason:          "gateway accepted but not final",
		Force:           true,
		DeductBalance:   true,
		DeductionType:   payment.DeductionTypeBalance,
		BalanceToDeduct: 40,
	}

	recordPreparedRefundForTest(t, ctx, client, plan)
	result, err := svc.FinishRefund(ctx, plan, preparedReceiptForTest(t, ctx, client, plan.OrderID), &payment.RefundResponse{RefundID: "rf_pending", Status: payment.ProviderStatusPending})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.False(t, result.Success)
	require.Contains(t, result.Warning, "pending confirmation")
	require.Equal(t, 40.0, rolledBack)
	require.Zero(t, plan.BalanceToDeduct)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusRefundPending, reloaded.Status)
	require.Equal(t, 40.0, reloaded.RefundAmount)
	require.NotNil(t, reloaded.RefundReason)
	require.Equal(t, "gateway accepted but not final", *reloaded.RefundReason)
	require.Nil(t, reloaded.RefundAt)

	pendingAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, pendingAudits)
	successAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, successAudits)
}

// TestFinishRefundSuccessStatusesFinalize seeds prepared receipt quantities
// and supplies success or refunded channel status. It expects refunded order
// state, completion time, the recorded deduction result and one success audit
// for each fixture.
func TestFinishRefundSuccessStatusesFinalize(t *testing.T) {
	for _, status := range []string{payment.ProviderStatusSuccess, payment.ProviderStatusRefunded} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			client := sqlitetest.NewClient(t)

			user, err := client.User.Create().
				SetEmail("refund-success-" + status + "@example.com").
				SetPasswordHash("hash").
				SetUsername("refund-success-" + status).
				Save(ctx)
			require.NoError(t, err)

			order, err := client.PaymentOrder.Create().
				SetUserID(user.ID).
				SetUserEmail(user.Email).
				SetUserName(user.Username).
				SetAmount(100).
				SetPayAmount(100).
				SetFeeRate(0).
				SetRechargeCode("REFUND-SUCCESS-" + status).
				SetOutTradeNo("sub2_refund_success_" + status).
				SetPaymentType(payment.TypeStripe).
				SetPaymentTradeNo("pi_refund_success_" + status).
				SetOrderType(payment.OrderTypeBalance).
				SetStatus(payment.OrderStatusRefunding).
				SetExpiresAt(time.Now().Add(time.Hour)).
				SetPaidAt(time.Now()).
				SetClientIP("127.0.0.1").
				SetSrcHost("api.example.com").
				Save(ctx)
			require.NoError(t, err)

			svc := newRefundWorkflowFixture(client, nil, nil, nil)
			plan := &payment.RefundPlan{
				OrderID:         order.ID,
				Order:           paymentpostgres.OrderFromEntity(order),
				RefundAmount:    100,
				GatewayAmount:   100,
				Reason:          "final success",
				DeductBalance:   true,
				DeductionType:   payment.DeductionTypeBalance,
				BalanceToDeduct: 100,
			}

			recordPreparedRefundForTest(t, ctx, client, plan)
			result, err := svc.FinishRefund(ctx, plan, preparedReceiptForTest(t, ctx, client, plan.OrderID), &payment.RefundResponse{Status: status})
			require.NoError(t, err)
			require.NotNil(t, result)
			require.True(t, result.Success)
			require.Equal(t, 100.0, result.BalanceDeducted)

			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, payment.OrderStatusRefunded, reloaded.Status)
			require.NotNil(t, reloaded.RefundAt)

			successAudits, err := client.PaymentAuditLog.Query().
				Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
				Count(ctx)
			require.NoError(t, err)
			require.Equal(t, 1, successAudits)
			pendingAudits, err := client.PaymentAuditLog.Query().
				Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).
				Count(ctx)
			require.NoError(t, err)
			require.Zero(t, pendingAudits)
		})
	}
}

// TestQueryAndFinalizeRefundFinalizesProviderStatuses supplies success, failed
// or pending query observations with callback-reported available balance. It
// checks resulting local state and deduction counters, including reduced
// successful quantity retained in the result and audit.
func TestQueryAndFinalizeRefundFinalizesProviderStatuses(t *testing.T) {
	var selectedProvider payment.Provider

	for _, tc := range []struct {
		name       string  // named queried refund case
		status     string  // supplied channel refund status
		wantStatus string  // expected persisted order state
		wantDeduct float64 // expected recorded deduction points
		available  float64 // callback-reported available balance points
	}{
		{name: "success", status: payment.ProviderStatusSuccess, wantStatus: payment.OrderStatusRefunded, wantDeduct: 100, available: 100},
		{name: "success clamps current balance", status: payment.ProviderStatusSuccess, wantStatus: payment.OrderStatusRefunded, wantDeduct: 35, available: 35},
		{name: "failed", status: payment.ProviderStatusFailed, wantStatus: payment.OrderStatusRefundFailed},
		{name: "pending", status: payment.ProviderStatusPending, wantStatus: payment.OrderStatusRefundPending},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			client := sqlitetest.NewClient(t)
			order := createPendingRefundOrderForTest(t, ctx, client, "query-finalize-"+tc.name, true)

			var deducted float64
			svc := newRefundWorkflowFixture(client, &refundBalanceFixture{deductBalanceResultFn: func(ctx context.Context, id int64, amount float64) (float64, error) {
				deducted += tc.available
				return tc.available, nil
			}}, &paymenttestkit.CaptureLoadBalancer{}, func(string, string, map[string]string) (payment.Provider, error) { return selectedProvider, nil })
			selectedProvider = &refundQueryProviderTestDouble{
				refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: tc.status},
			}

			result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, tc.status == payment.ProviderStatusSuccess, result.Success)
			require.Equal(t, tc.wantDeduct, deducted)
			if tc.status == payment.ProviderStatusSuccess {
				require.Equal(t, tc.wantDeduct, result.BalanceDeducted)
				audit, err := client.PaymentAuditLog.Query().
					Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
					Only(ctx)
				require.NoError(t, err)
				require.Contains(t, audit.Detail, fmt.Sprintf(`"balanceDeducted":%v`, tc.wantDeduct))
			}

			reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, reloaded.Status)
		})
	}
}

// TestFinalizePendingRefundSuccessRejectsStaleCallerBeforeSecondDeduction
// submits the same saved pending observations twice sequentially. It expects
// the first success, second conflict and one deduction callback and success
// audit, with transaction context present during deduction.
func TestFinalizePendingRefundSuccessRejectsStaleCallerBeforeSecondDeduction(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "finalize-stale", true)
	pendingDetail := pendingDetailForTest(t, ctx, client, order.ID)

	deductions := 0
	svc := newRefundWorkflowFixture(client, &refundBalanceFixture{deductBalanceResultFn: func(ctx context.Context, id int64, amount float64) (float64, error) {
		require.NotNil(t, dbent.TxFromContext(ctx))
		deductions++
		return amount, nil
	}}, nil, nil)

	first, err := svc.FinalizePendingRefundSuccess(ctx, svc.RefundFinalizePlan(paymentpostgres.OrderFromEntity(order), pendingDetail))
	require.NoError(t, err)
	require.True(t, first.Success)

	second, err := svc.FinalizePendingRefundSuccess(ctx, svc.RefundFinalizePlan(paymentpostgres.OrderFromEntity(order), pendingDetail))
	require.Nil(t, second)
	require.Error(t, err)
	require.Equal(t, "CONFLICT", apperror.Reason(err))
	require.Equal(t, 1, deductions)

	successAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, successAudits)
}

// TestFinalizePendingRefundSuccessRollsBackPostDeductionFailure updates SQLite
// wallet balance through TxFromContext and then injects a callback error. It
// expects the original persisted balance, pending order state and absence of a
// success audit after rollback.
func TestFinalizePendingRefundSuccessRollsBackPostDeductionFailure(t *testing.T) {
	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "finalize-rollback", true)
	_, err := client.User.UpdateOneID(order.UserID).SetBalance(100).Save(ctx)
	require.NoError(t, err)
	pendingDetail := pendingDetailForTest(t, ctx, client, order.ID)

	svc := newRefundWorkflowFixture(client, &refundBalanceFixture{deductBalanceResultFn: func(ctx context.Context, id int64, amount float64) (float64, error) {
		tx := dbent.TxFromContext(ctx)
		require.NotNil(t, tx)
		// Mutate balance through the same transaction carried by finalization, then
		// fail the callback so persisted rollback can be observed.
		if _, updateErr := tx.Client().User.UpdateOneID(id).AddBalance(-amount).Save(ctx); updateErr != nil {
			return 0, updateErr
		}
		return 0, errors.New("injected failure after deduction")
	}}, nil, nil)

	result, err := svc.FinalizePendingRefundSuccess(ctx, svc.RefundFinalizePlan(paymentpostgres.OrderFromEntity(order), pendingDetail))
	require.Nil(t, result)
	require.ErrorContains(t, err, "injected failure after deduction")

	user, err := client.User.Get(ctx, order.UserID)
	require.NoError(t, err)
	require.Equal(t, 100.0, user.Balance)
	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusRefundPending, reloaded.Status)
	successAudits, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(order.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).
		Count(ctx)
	require.NoError(t, err)
	require.Zero(t, successAudits)
}

// TestQueryAndFinalizeRefundPreservesNoDeductionChoice seeds pending audit
// data with explicit false deduction intent and returns channel success. It
// expects refunded state and zero amount accumulated by the deduction
// callback.
func TestQueryAndFinalizeRefundPreservesNoDeductionChoice(t *testing.T) {
	var selectedProvider payment.Provider

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "query-finalize-no-deduct", false)
	var deducted float64
	svc := newRefundWorkflowFixture(client, &refundBalanceFixture{deductBalanceFn: func(ctx context.Context, id int64, amount float64) error {
		deducted += amount
		return nil
	}}, &paymenttestkit.CaptureLoadBalancer{}, func(string, string, map[string]string) (payment.Provider, error) { return selectedProvider, nil })
	selectedProvider = &refundQueryProviderTestDouble{
		refundResponse: &payment.RefundResponse{RefundID: "rf_test", Status: payment.ProviderStatusSuccess},
	}

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Zero(t, deducted)

	reloaded, err := client.PaymentOrder.Get(ctx, order.ID)
	require.NoError(t, err)
	require.Equal(t, payment.OrderStatusRefunded, reloaded.Status)
}

// TestQueryAndFinalizeRefundUnsupportedProviderReturnsClearError supplies a
// base provider double without refund-query capability for a pending order. It
// expects no result and the structured REFUND_QUERY_UNSUPPORTED reason.
func TestQueryAndFinalizeRefundUnsupportedProviderReturnsClearError(t *testing.T) {
	var selectedProvider payment.Provider

	ctx := context.Background()
	client := sqlitetest.NewClient(t)
	order := createPendingRefundOrderForTest(t, ctx, client, "query-finalize-unsupported", true)
	svc := newRefundWorkflowFixture(client, nil, &paymenttestkit.CaptureLoadBalancer{}, func(string, string, map[string]string) (payment.Provider, error) { return selectedProvider, nil })
	selectedProvider = refundProviderTestDouble{}

	result, err := svc.QueryAndFinalizeRefund(ctx, order.ID)
	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, "REFUND_QUERY_UNSUPPORTED", apperror.Reason(err))
}

// DeductBalance prefers the quantity-returning callback, then the error-only
// callback with zero quantity, then reports the requested amount. Callers
// receive these fixture observations as supplied; wallet mutation occurs only
// when the selected callback implements it.
func (f *refundBalanceFixture) DeductBalance(ctx context.Context, id int64, amount float64) (float64, error) {
	if f.deductBalanceResultFn != nil {
		return f.deductBalanceResultFn(ctx, id, amount)
	}
	if f.deductBalanceFn != nil {
		return 0, f.deductBalanceFn(ctx, id, amount)
	}
	return amount, nil
}

// CompensateBalance invokes the supplied balance-update callback or reports
// nil when it is absent. The default records no wallet change, so compensation
// tests install a counter or transaction-aware implementation when that
// observation matters.
func (f *refundBalanceFixture) CompensateBalance(ctx context.Context, id int64, amount float64) error {
	if f.updateBalanceFn != nil {
		return f.updateBalanceFn(ctx, id, amount)
	}
	return nil
}

// newRefundWorkflowFixture assembles SQLite order/audit storage, historical
// bindings and the supplied balance callbacks for workflow tests. An absent
// provider factory uses the adapter constructor, while the rights factory
// returns the same balances object for each transaction.
//
// Callbacks participate in the order transaction when they use its
// TxFromContext; assembly itself does not attach arbitrary callback writes to
// Ent transactions. User lookup returns its supplied pointer or a zero user. A
// nil balances pointer is suitable only for paths that never invoke user
// lookup or balance rights, because those methods dereference it.
func newRefundWorkflowFixture(client *dbent.Client, balances *refundBalanceFixture, balancer payment.LoadBalancer, factory func(string, string, map[string]string) (payment.Provider, error)) *payment.RefundWorkflow {
	if factory == nil {
		factory = paymentadapter.CreateProvider
	}
	bindings := payment.NewProviderBindings(paymentpostgres.NewInstanceStore(client), nil, balancer, payment.BindingRuntime{Factory: factory}, false)
	store := paymentpostgres.NewRefundStore(client, func(*dbent.Tx) payment.RefundRights { return balances })
	return payment.NewRefundWorkflow(store, payment.RefundRuntime{
		Instance: bindings.GetRefundOrderProviderInstance,
		Provider: bindings.GetRefundProvider,
		User: func(context.Context, int64) (*payment.RefundUser, error) {
			if balances.user == nil {
				return &payment.RefundUser{}, nil
			}
			return balances.user, nil
		},
	})
}

// preparedReceiptForTest reads an order and its preparation receipt through
// RefundRecovery, failing the test on either read error. Fixtures call it
// after preparation metadata and audit are saved to use the persisted
// operation version.
func preparedReceiptForTest(t *testing.T, ctx context.Context, client *dbent.Client, id int64) *payment.RefundReceipt {
	t.Helper()
	store := paymentpostgres.NewRefundStore(client, nil)
	order, err := store.Order(ctx, id)
	require.NoError(t, err)
	receipt, err := store.RefundRecovery(ctx, order)
	require.NoError(t, err)
	return receipt
}

// pendingDetailForTest reads the stored pending audit description through
// RefundStore and fails the test on retrieval error. Finalization cases use
// that observation with the saved order snapshot to construct their expected
// operation.
func pendingDetailForTest(t *testing.T, ctx context.Context, client *dbent.Client, id int64) payment.RefundPendingDetail {
	t.Helper()
	detail, err := paymentpostgres.NewRefundStore(client, nil).PendingDetail(ctx, id)
	require.NoError(t, err)
	return detail
}

// createPendingRefundOrderForTest saves a user, Stripe instance, pending order
// and legacy pending audit as separate SQLite fixture writes. The suffix
// supplies their identity labels, and deductBalance is serialized even when
// false so recovery can retain that choice.
//
// The audit records a hundred-point reported deduction with its rollback flag
// set true, while this helper performs no wallet deduction. Its stored refund
// reference drives local query fixtures. Every save failure fails the test,
// and the completed fixture can then be read by recovery; setup does not
// establish a single transaction spanning these independent writes.
func createPendingRefundOrderForTest(t *testing.T, ctx context.Context, client *dbent.Client, suffix string, deductBalance bool) *dbent.PaymentOrder {
	t.Helper()

	// Save prerequisite user and instance before the pending order and audit;
	// these fixture writes remain individually committed if a later setup step
	// fails.
	user, err := client.User.Create().
		SetEmail(suffix + "@example.com").
		SetPasswordHash("hash").
		SetUsername(suffix).
		Save(ctx)
	require.NoError(t, err)

	inst, err := client.PaymentProviderInstance.Create().
		SetProviderKey(payment.TypeStripe).
		SetName(suffix + "-provider").
		SetConfig("{}").
		SetSupportedTypes("stripe").
		SetEnabled(true).
		SetRefundEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	order, err := client.PaymentOrder.Create().
		SetUserID(user.ID).
		SetUserEmail(user.Email).
		SetUserName(user.Username).
		SetAmount(100).
		SetPayAmount(100).
		SetFeeRate(0).
		SetRechargeCode("REFUND-" + suffix).
		SetOutTradeNo("sub2_" + suffix).
		SetPaymentType(payment.TypeStripe).
		SetPaymentTradeNo("pi_" + suffix).
		SetOrderType(payment.OrderTypeBalance).
		SetStatus(payment.OrderStatusRefundPending).
		SetRefundAmount(100).
		SetRefundReason("pending refund").
		SetExpiresAt(time.Now().Add(time.Hour)).
		SetPaidAt(time.Now()).
		SetClientIP("127.0.0.1").
		SetSrcHost("api.example.com").
		SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).
		Save(ctx)
	require.NoError(t, err)

	_, err = client.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(order.ID, 10)).
		SetAction("REFUND_PENDING").
		SetOperator("admin").
		SetDetail(fmt.Sprintf(`{"refundID":"rf_test","deductBalance":%t,"deductionType":"balance","balanceDeducted":100,"deductionRollbackOK":true}`, deductBalance)).
		Save(ctx)
	require.NoError(t, err)
	return order
}

// Name returns the fixed display label for this refund fixture provider.
func (refundProviderTestDouble) Name() string { return "refund-test" }

// ProviderKey returns Stripe identity for the refund binding fixture.
func (refundProviderTestDouble) ProviderKey() string {
	return payment.TypeStripe
}

// SupportedTypes returns a fresh single-method list for this Stripe fixture
// provider.
func (refundProviderTestDouble) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeStripe}
}

// CreatePayment returns nil result and nil error as an unused checkout
// interface stub. Its success-shaped return performs no channel work, and
// these fixtures inspect refund-query capability separately.
func (refundProviderTestDouble) CreatePayment(context.Context, payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	return nil, nil
}

// QueryOrder returns nil result and nil error as an unused order query
// interface stub. Its success-shaped return performs no channel work, and
// these fixtures inspect refund-query capability separately.
func (refundProviderTestDouble) QueryOrder(context.Context, string) (*payment.QueryOrderResponse, error) {
	return nil, nil
}

// VerifyNotification returns nil result and nil error as an unused
// notification interface stub. Its success-shaped return performs no channel
// work, and these fixtures inspect refund-query capability separately.
func (refundProviderTestDouble) VerifyNotification(context.Context, string, map[string]string) (*payment.PaymentNotification, error) {
	return nil, nil
}

// Refund returns nil result and nil error as an unused refund interface stub.
// Its success-shaped return performs no channel work, and these fixtures
// inspect refund-query capability separately.
func (refundProviderTestDouble) Refund(context.Context, payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, nil
}

// QueryRefund returns the configured response pointer and nil error for
// synchronous workflow recovery. The caller retains ownership of that fixture
// response, and the double adds no copying or channel lookup.
func (p *refundQueryProviderTestDouble) QueryRefund(context.Context, payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	return p.refundResponse, nil
}

// recordPreparedRefundForTest writes refund amount, reason and force fields,
// rereads UpdatedAt and saves a version-one preparation receipt audit. These
// are separate SQLite writes rather than the production preparation
// transaction, and the helper fails the test on write, read or JSON errors.
//
// The receipt copies the supplied plan quantities and uses the saved row
// timestamp as OperationVersion for FinishRefund association. It declares
// completed as PreviousStatus and a fixed fixture OperationID. No entitlement
// deduction runs here, so quantities assert later receipt handling without
// proving that the fixture already changed a wallet or subscription.
func recordPreparedRefundForTest(t *testing.T, ctx context.Context, client *dbent.Client, p *payment.RefundPlan) {
	t.Helper()
	// Save refund fields before rereading the operation timestamp and serializing
	// its receipt, so later completion compares against this persisted row
	// observation.
	_, err := client.PaymentOrder.UpdateOneID(p.OrderID).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetForceRefund(p.Force).Save(ctx)
	require.NoError(t, err)
	o, err := client.PaymentOrder.Get(ctx, p.OrderID)
	require.NoError(t, err)
	receipt := payment.RefundReceipt{Version: 1, OperationID: "fixture-prepared", OrderID: p.OrderID, OperationVersion: o.UpdatedAt, PreviousStatus: payment.OrderStatusCompleted, RefundAmount: p.RefundAmount, GatewayAmount: p.GatewayAmount, Reason: p.Reason, Force: p.Force, RefundPendingDetail: payment.RefundPendingDetail{DeductBalance: p.DeductBalance, DeductionType: p.DeductionType, BalanceDeducted: p.BalanceToDeduct, SubDaysDeducted: p.SubDaysToDeduct, SubscriptionID: p.SubscriptionID}}
	data, err := json.Marshal(receipt)
	require.NoError(t, err)
	// Save the receipt after the separate order write; this fixture records
	// preparation metadata but has not executed any entitlement deduction.
	_, err = client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(p.OrderID, 10)).SetAction("REFUND_PREPARED").SetOperator("admin").SetDetail(string(data)).Save(ctx)
	require.NoError(t, err)
}
