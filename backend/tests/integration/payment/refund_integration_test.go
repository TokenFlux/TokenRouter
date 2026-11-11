//go:build integration

package payment_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v85"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// Refund recovery retains prepared deductions when later audit work fails, and
// delayed provider failures cannot replace a newer completed refund. PostgreSQL
// cases inspect preparation rollback, recoverable refunding state,
// success-audit failure, malformed recovery facts, and historical attempt
// records while provider HTTP runs through a local Stripe backend.
//
// The stale-result fixture parks the first provider reply, completes a second
// confirmation, then releases the preceding failure and inspects final status.
// Recovery fixtures retain the prepared audit while repeatedly querying the
// same operation, counting refund POSTs independently of status queries.
// Process-wide backend restoration and trigger cleanup precede order fixture
// deletion. Each asserted state is read through the shared base client after
// workflow work returns.

// refundOrderFixture stores a user, Stripe provider instance, and refund order
// with the supplied status, then returns the shared client and configured
// workflow. The user starts with a balance of one hundred and the order records
// a refund amount of fifty.
//
// The order refers to both preceding records, so its save follows user and
// instance creation. Audit, order, and instance cleanup is registered only
// after the order saves successfully; an earlier failure can leave the instance
// behind until suite teardown. Successful setup deletes audits before the order
// and instance, then the shared fixture truncates users.
func refundOrderFixture(t *testing.T, status string) (*dbent.Client, *payment.RefundWorkflow, *dbent.PaymentOrder) {
	t.Helper()
	ctx := context.Background()
	c := testEntClient(t)
	u, e := c.User.Create().SetEmail("test-refund@example.com").SetPasswordHash("test-hash").SetUsername("test").SetBalance(100).Save(ctx)
	require.NoError(t, e)
	inst, e := c.PaymentProviderInstance.Create().SetName("test").SetProviderKey(payment.TypeStripe).SetConfig(`{"secretKey":"test-only","currency":"USD"}`).SetSupportedTypes(payment.TypeStripe).SetRefundEnabled(true).Save(ctx)
	require.NoError(t, e)
	// Create the order after both referenced fixtures exist; independent
	// row cleanup is registered below only after this save succeeds, so
	// partial setup can outlive the shared user truncation.
	o, e := c.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName(u.Username).SetAmount(50).SetPayAmount(50).SetRechargeCode("test-refund").SetOutTradeNo("test-refund").SetPaymentType(payment.TypeStripe).SetPaymentTradeNo("pi_test").SetProviderSnapshot(map[string]any{"currency": "USD"}).SetOrderType(payment.OrderTypeBalance).SetStatus(status).SetRefundAmount(50).SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("test.local").SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).Save(ctx)
	require.NoError(t, e)
	// Delete dependent audit records before their order and provider
	// instance so these committed fixtures are removed before the shared
	// user cleanup truncates its tables.
	t.Cleanup(func() {
		_, e := c.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10))).Exec(context.Background())
		require.NoError(t, e)
		require.NoError(t, c.PaymentOrder.DeleteOneID(o.ID).Exec(context.Background()))
		require.NoError(t, c.PaymentProviderInstance.DeleteOneID(inst.ID).Exec(context.Background()))
	})
	s := newPostgresRefundWorkflow(c)
	return c, s, o
}

// refundStripeFixture routes the process-wide Stripe API backend to a local
// server using h and zero configured network retries. Cleanup restores the
// preceding backend before closing the server; cases serialize this global
// override.
func refundStripeFixture(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	// Capture the process-wide backend before substituting the local
	// provider server; its later cleanup restores that backend before the
	// earlier server-close callback runs.
	original := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0)}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, original) })
}

// refundFailAudit installs a PostgreSQL insert trigger rejecting one order and
// action with a fixture error. DDL uses integrationDB, so c is unused; callers
// supply the fixed fixture action because its text is interpolated into SQL.
//
// The function is created before its trigger, and cleanup is registered after
// both writes succeed. Trigger creation failure can therefore leave the
// function installed. Cleanup drops the trigger before the function and uses IF
// EXISTS to tolerate a recovery case that already removed the trigger.
func refundFailAudit(t *testing.T, c *dbent.Client, id int64, action string) {
	t.Helper()
	name := fmt.Sprintf("refund_audit_%d", id)
	ctx := context.Background()
	// Install action-specific rejection before exercising refund
	// persistence so the workflow's required audit failures can be observed
	// through its returned error and outside state reads.
	_, e := integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.order_id='%d' AND NEW.action='%s' THEN RAISE EXCEPTION 'test forced audit failure'; END IF; RETURN NEW; END; $$`, name, id, action))
	require.NoError(t, e)
	_, e = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON payment_audit_logs FOR EACH ROW EXECUTE FUNCTION %s()`, name, name))
	require.NoError(t, e)
	// Remove the injected trigger before its function; IF EXISTS keeps
	// cleanup valid after a recovery scenario has already removed the
	// trigger for its successful retry.
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON payment_audit_logs; DROP FUNCTION IF EXISTS %s()", name, name))
		require.NoError(t, e)
	})
}

// TestStaleRefundFailureOverwritesSuccess parks the earlier local provider
// failure while another confirmation succeeds. Releasing that first reply
// leaves the persisted order REFUNDED; the logged balance is not asserted by
// this case.
func TestStaleRefundFailureOverwritesSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, s, o := refundOrderFixture(t, payment.OrderStatusRefundPending)
	_, e := c.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("REFUND_PENDING").SetOperator("admin").SetDetail(`{"refundID":"re_test","deductBalance":true,"balanceDeducted":50,"deductionRollbackOK":true}`).Save(ctx)
	require.NoError(t, e)
	// Hold the first provider reply while allowing the next query to
	// confirm success, then release its stale failure after the newer
	// transaction has completed.
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		status := "succeeded"
		if calls.Add(1) == 1 {
			close(entered)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
			status = "failed"
		}
		w.Header().Set("Content-Type", "application/json")
		// Return the configured local refund status while discarding
		// response-write errors; assertions in the caller inspect
		// workflow errors or stored confirmation effects.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "re_test", "object": "refund", "status": status,
			"payment_intent": "pi_test", "amount": 5000, "currency": "usd",
		})
	})
	done := make(chan struct{})
	// Observe this older call through completion and final stored status;
	// its returned result and error are deliberately discarded before the
	// worker signals that it ended.
	go func() { _, _ = s.QueryAndFinalizeRefund(ctx, o.ID); close(done) }()
	<-entered
	result, e := s.QueryAndFinalizeRefund(ctx, o.ID)
	require.NoError(t, e)
	require.True(t, result.Success)
	close(release)
	<-done
	current, e := c.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, e)
	u, e := c.User.Get(ctx, o.UserID)
	require.NoError(t, e)
	t.Logf("balance=%v final_status=%s", u.Balance, current.Status)
	require.Equal(t, payment.OrderStatusRefunded, current.Status, "迟到失败不得覆盖已完成退款")
}

// TestPendingRefundAuditLossSkipsDeduction rejects the pending audit after
// ExecuteRefund deducts the planned funds. The order remains recoverable and
// balance stays fifty, including after successful status-query recovery of that
// recorded deduction.
func TestPendingRefundAuditLossSkipsDeduction(t *testing.T) {
	ctx := context.Background()
	c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			// Return a pending provider refund while discarding
			// write errors; the audit trigger supplies the workflow
			// failure that this case observes.
			_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"pending"}`))
			return
		}
		// Recovery retrieves the saved refund reference. Return the matching
		// payment and amount so the workflow can confirm it without listing refunds.
		_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded","payment_intent":"pi_test","amount":5000,"currency":"usd"}`))
	})
	refundFailAudit(t, c, o.ID, "REFUND_PENDING")
	plan, warning, e := s.PrepareRefund(ctx, o.ID, 50, "test", false, true)
	require.NoError(t, e)
	require.Nil(t, warning)
	result, e := s.ExecuteRefund(ctx, plan)
	require.Equal(t, "REFUND_RECOVERY_REQUIRED", apperror.Reason(e))
	require.Equal(t, "could not record pending refund", apperror.FromError(e).Metadata["reason"])
	require.Nil(t, result)
	cur, readErr := c.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, readErr)
	require.Equal(t, payment.OrderStatusRefunding, cur.Status)
	before, readErr := c.User.Get(ctx, o.UserID)
	require.NoError(t, readErr)
	require.Equal(t, 50.0, before.Balance, "补偿与 pending 审计必须一起回滚，保留准备扣减")
	// Recover through the recorded prepared operation after pending-audit
	// rejection; the balance read afterward detects an extra deduction
	// during this successful confirmation.
	result, e = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.NoError(t, e)
	require.True(t, result.Success)
	u, e := c.User.Get(ctx, o.UserID)
	require.NoError(t, e)
	t.Logf("confirmed refund deducted=%v balance=%v", result.BalanceDeducted, u.Balance)
	require.Equal(t, 50.0, u.Balance, "缺失 pending 审计不能静默跳过权益回收")
}

// TestImmediateRefundAuditFailureIsNotReported injects success-audit rejection
// after the local provider reports refund success. ExecuteRefund returns an
// error; the subsequently read balance, order status, and audit count are
// logged without assertions.
func TestImmediateRefundAuditFailureIsNotReported(t *testing.T) {
	ctx := context.Background()
	c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Return the configured local refund status while discarding
		// response-write errors; assertions in the caller inspect
		// workflow errors or stored confirmation effects.
		_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded"}`))
	})
	refundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
	plan, warning, e := s.PrepareRefund(ctx, o.ID, 50, "test", false, true)
	require.NoError(t, e)
	require.Nil(t, warning)
	result, e := s.ExecuteRefund(ctx, plan)
	u, readErr := c.User.Get(ctx, o.UserID)
	require.NoError(t, readErr)
	cur, readErr := c.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, readErr)
	count, readErr := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, readErr)
	t.Logf("result=%+v error=%v balance=%v status=%s success_audits=%d", result, e, u.Balance, cur.Status, count)
	require.Error(t, e, "必须保留的退款成功审计写入失败不能返回成功")
}

// TestRefundPreparationAuditFailureStopsChannel rejects the prepared audit
// before provider invocation. ExecuteRefund returns no result, leaves wallet
// and completed order state unchanged, creates no prepared audit, and makes
// zero provider calls.
func TestRefundPreparationAuditFailureStopsChannel(t *testing.T) {
	ctx := context.Background()
	c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
	var calls atomic.Int32
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		// Return the configured local refund status while discarding
		// response-write errors; assertions in the caller inspect
		// workflow errors or stored confirmation effects.
		_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded"}`))
	})
	refundFailAudit(t, c, o.ID, "REFUND_PREPARED")
	plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "prepare failure", false, true)
	require.NoError(t, err)
	require.Nil(t, warning)
	result, err := s.ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.ErrorContains(t, err, "test forced audit failure")
	require.Zero(t, calls.Load())
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 100, payment.OrderStatusCompleted, 0)
	count, err := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// TestRefundPreparedRecoveryDoesNotRepeatChannelOrDeduction fails the success
// audit after one provider refund. Query retries retain prepared funds until
// audit recovery succeeds, and a terminal query leaves one refund POST and one
// success audit.
func TestRefundPreparedRecoveryDoesNotRepeatChannelOrDeduction(t *testing.T) {
	ctx := context.Background()
	c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
	var refunds atomic.Int32
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			refunds.Add(1)
			// Return the configured local refund status while
			// discarding response-write errors; assertions in the
			// caller inspect workflow errors or stored confirmation
			// effects.
			_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded"}`))
			return
		}
		// Recovery retrieves the saved refund reference. Return the matching
		// payment and amount so the workflow can confirm it without listing refunds.
		_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded","payment_intent":"pi_test","amount":5000,"currency":"usd"}`))
	})
	refundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
	plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "recover", false, true)
	require.NoError(t, err)
	require.Nil(t, warning)
	result, err := s.ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.Error(t, err)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunding, 0)
	// Repeat confirmation while success-audit rejection remains installed
	// so outside state reads can detect a second deduction before the
	// retained recovery record can complete.
	result, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.Nil(t, result)
	require.Error(t, err)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunding, 0)
	name := fmt.Sprintf("refund_audit_%d", o.ID)
	// Remove success-audit rejection before retrying recovery so the
	// retained prepared deduction can complete confirmation without issuing
	// another provider refund or reducing funds again.
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON payment_audit_logs", name))
	require.NoError(t, err)
	result, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunded, 1)
	// Query the terminal order again so its rejection is checked alongside
	// the unchanged refund POST count and the final stored balance, order,
	// and success-audit count.
	_, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.Error(t, err)
	require.Equal(t, int32(1), refunds.Load())
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunded, 1)
}

// TestRefundMissingRecoveryRequiresManualVerification supplies pending and
// refunding orders with no retained recovery facts. Query returns a
// manual-verification error before any provider call and retains the asserted
// wallet, status, and audit count.
func TestRefundMissingRecoveryRequiresManualVerification(t *testing.T) {
	for _, status := range []string{payment.OrderStatusRefundPending, payment.OrderStatusRefunding} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			c, s, o := refundOrderFixture(t, status)
			var calls atomic.Int32
			refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				http.Error(w, "must not query", http.StatusInternalServerError)
			})
			result, err := s.QueryAndFinalizeRefund(ctx, o.ID)
			require.Nil(t, result)
			require.ErrorContains(t, err, "manual verification")
			require.Zero(t, calls.Load())
			assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 100, status, 0)
		})
	}
}

// TestRefundInvalidPreparedFactsRequireManualVerification corrupts or
// contradicts the prepared recovery detail after a provider operation. Later
// query requires manual verification, makes no additional provider call, and
// retains the prepared deduction and refunding state.
func TestRefundInvalidPreparedFactsRequireManualVerification(t *testing.T) {
	for _, variant := range []string{"missing_choice", "corrupt_json", "contradictory_deduction"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
			var calls atomic.Int32
			refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				// Return the configured local refund status
				// while discarding response-write errors;
				// assertions in the caller inspect workflow
				// errors or stored confirmation effects.
				_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded"}`))
			})
			refundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
			plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "recover", false, true)
			require.NoError(t, err)
			require.Nil(t, warning)
			_, err = s.ExecuteRefund(ctx, plan)
			require.Error(t, err)
			row, err := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Only(ctx)
			require.NoError(t, err)
			var fields map[string]any
			require.NoError(t, json.Unmarshal([]byte(row.Detail), &fields))
			data := []byte("not-json")
			switch variant {
			case "missing_choice":
				delete(fields, "deductBalance")
				data, err = json.Marshal(fields)
			case "contradictory_deduction":
				fields["deductBalance"] = false
				data, err = json.Marshal(fields)
			}
			require.NoError(t, err)
			_, err = c.PaymentAuditLog.UpdateOneID(row.ID).SetDetail(string(data)).Save(ctx)
			require.NoError(t, err)
			before := calls.Load()
			result, err := s.QueryAndFinalizeRefund(ctx, o.ID)
			require.Nil(t, result)
			require.ErrorContains(t, err, "manual verification")
			require.Equal(t, before, calls.Load())
			assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunding, 0)
		})
	}
}

// TestRefundRepeatedAttemptsPreserveFactsWithUniqueAction executes two rejected
// manual refunds followed by a successful third attempt. The unique
// prepared-action record retains two earlier distinct operation IDs and
// deduction facts, while provider calls total three.
func TestRefundRepeatedAttemptsPreserveFactsWithUniqueAction(t *testing.T) {
	ctx := context.Background()
	c, s, o := refundOrderFixture(t, payment.OrderStatusCompleted)
	var existed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='idx_payment_audit_logs_order_action_uniq')").Scan(&existed))
	// Add the unique order/action index only when this named fixture index
	// is absent, then remove only that newly created index after the
	// attempt-history observations finish.
	if !existed {
		_, err := integrationDB.ExecContext(ctx, "CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action)")
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := integrationDB.ExecContext(context.Background(), "DROP INDEX IF EXISTS idx_payment_audit_logs_order_action_uniq")
			require.NoError(t, e)
		})
	}
	var calls atomic.Int32
	refundStripeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// Reject the first two explicit refund attempts and accept the
		// third so the retained unique-action record must hold both
		// preceding operation facts in its history.
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadRequest)
			// Return the selected provider rejection while
			// discarding write errors; the repeated-attempt
			// assertions inspect compensation and archived
			// operation facts.
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","code":"parameter_invalid_integer","message":"fixture refund rejected"}}`))
			return
		}
		// Return the configured local refund status while discarding
		// response-write errors; assertions in the caller inspect
		// workflow errors or stored confirmation effects.
		_, _ = w.Write([]byte(`{"id":"re_test","object":"refund","status":"succeeded"}`))
	})
	for attempt := range 3 {
		plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "same refund", false, true)
		require.NoError(t, err)
		require.Nil(t, warning)
		result, err := s.ExecuteRefund(ctx, plan)
		require.NoError(t, err)
		if attempt < 2 {
			require.False(t, result.Success)
			assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 100, payment.OrderStatusCompleted, 0)
		} else {
			require.True(t, result.Success)
			assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunded, 1)
		}
	}
	row, err := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Only(ctx)
	require.NoError(t, err)
	var latest struct {
		// OperationID identifies the current attempt and History stores
		// preceding facts archived under the unique order/action row;
		// the test compares their IDs and earlier deductions.
		OperationID string `json:"operationID"` // current prepared operation ID
		History     []struct {
			// preceding prepared operation ID
			OperationID string `json:"operationID"`
			// preceding actual balance deduction
			Balance float64 `json:"balanceDeducted"`
		} `json:"history"` // preceding operation recovery records
	}
	require.NoError(t, json.Unmarshal([]byte(row.Detail), &latest))
	require.Len(t, latest.History, 2)
	seen := map[string]bool{latest.OperationID: true}
	for _, previous := range latest.History {
		require.NotEmpty(t, previous.OperationID)
		require.False(t, seen[previous.OperationID])
		seen[previous.OperationID] = true
		require.Equal(t, 50.0, previous.Balance)
	}
	require.Equal(t, int32(3), calls.Load(), "只执行管理员明确发起的三次尝试，不自动重发")
}
