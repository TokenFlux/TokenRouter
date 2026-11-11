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

	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// Refund confirmation can compete after two callers have already loaded the
// same pending recovery state. The PostgreSQL fixture holds both local Stripe
// status responses until each query reaches the HTTP gate, then lets the
// workflow's conditional confirmation choose one successful claimant and checks
// one balance deduction and success audit.
//
// A separate case rejects the success audit after its trigger observes the
// reduced balance inside the refund transaction. Outside state reads check
// rollback to the pending order and original funds, followed by successful
// retry after removing that trigger. Provider transport remains the local test
// server, and the process-wide Stripe API backend is restored before server
// cleanup. Repeating completed confirmation checks rejection of the terminal
// order while outside reads confirm the retained final state.

// TestPaymentRefundPostgresAtomicity combines gated concurrent confirmation
// with a success-audit failure and retry against PostgreSQL. Exactly one
// confirmation succeeds in the race, and final balance, order status, and audit
// count reflect one deduction.
func TestPaymentRefundPostgresAtomicity(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		name := "audit_failure_rolls_back_then_retry"
		if concurrent {
			name = "concurrent_confirmation_deducts_once"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			client := testEntClient(t)
			var calls atomic.Int32
			ready := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/refunds/re_test" {
					http.Error(w, "unexpected refund request", http.StatusBadRequest)
					return
				}
				// Hold provider status replies until both
				// queries arrive so each workflow has loaded
				// pending recovery facts before competing for
				// the conditional PostgreSQL confirmation.
				if calls.Add(1) == 2 && concurrent {
					close(ready)
				}
				if concurrent {
					select {
					case <-ready:
					case <-r.Context().Done():
						return
					}
				}
				w.Header().Set("Content-Type", "application/json")
				// Return the local provider success shape
				// needed by confirmation; this handler discards
				// encoding errors while the caller assertions
				// inspect workflow and stored effects.
				_ = json.NewEncoder(w).Encode(map[string]any{
					"id": "re_test", "object": "refund", "status": "succeeded",
					"payment_intent": "pi_test", "amount": 5000, "currency": "usd",
				})
			}))
			// Keep the provider server alive through workflow
			// calls, and register its closure before backend
			// restoration so the later restoration callback runs
			// first.
			t.Cleanup(server.Close)
			// Replace the process-wide Stripe API backend with this
			// local server and disable SDK network retries, then
			// restore its preceding backend at test cleanup.
			originalBackend := stripe.GetBackend(stripe.APIBackend)
			stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			}))
			t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, originalBackend) })

			user, err := client.User.Create().SetEmail("test-refund@example.com").
				SetPasswordHash("test-hash").SetUsername("test-refund").SetBalance(100).Save(ctx)
			require.NoError(t, err)
			instance, err := client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeStripe).
				SetName("test-refund").SetConfig(`{"secretKey":"test-only-key","currency":"USD"}`).
				SetSupportedTypes(payment.TypeStripe).SetRefundEnabled(true).Save(ctx)
			require.NoError(t, err)
			// Remove the committed provider instance after the
			// later order cleanup so the passing case deletes its
			// separate provider record before the shared fixture
			// truncates users.
			t.Cleanup(func() {
				require.NoError(t, client.PaymentProviderInstance.DeleteOneID(instance.ID).Exec(context.Background()))
			})
			order, err := client.PaymentOrder.Create().
				SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
				SetAmount(50).SetPayAmount(50).SetRechargeCode("test-refund").
				SetOutTradeNo("test-refund").SetPaymentType(payment.TypeStripe).
				SetPaymentTradeNo("pi_test").SetOrderType(payment.OrderTypeBalance).
				SetProviderSnapshot(map[string]any{"currency": "USD"}).
				SetStatus(payment.OrderStatusRefundPending).SetRefundAmount(50).
				SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).
				SetClientIP("127.0.0.1").SetSrcHost("test.local").
				SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).Save(ctx)
			require.NoError(t, err)
			orderID := strconv.FormatInt(order.ID, 10)
			// Remove order audits before the committed order and
			// provider instance so their separate cleanup precedes
			// the shared fixture's cascading user-table truncation.
			t.Cleanup(func() {
				_, cleanupErr := client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(orderID)).Exec(context.Background())
				require.NoError(t, cleanupErr)
				require.NoError(t, client.PaymentOrder.DeleteOneID(order.ID).Exec(context.Background()))
			})
			_, err = client.PaymentAuditLog.Create().SetOrderID(orderID).SetAction("REFUND_PENDING").
				SetOperator("admin").SetDetail(`{"refundID":"re_test","deductBalance":true,"balanceDeducted":50,"deductionRollbackOK":true}`).Save(ctx)
			require.NoError(t, err)
			svc := newPostgresRefundWorkflow(client)

			if concurrent {
				// outcome records a confirmation worker's
				// report and failure for the competing refund
				// claim.
				type outcome struct {
					result *payment.RefundResult // completed confirmation result
					err    error                 // refund confirmation failure
				}
				// Buffer both worker results before receiving
				// them so their completion can proceed
				// independently; the state assertion follows
				// both returned confirmation attempts.
				results := make(chan outcome, 2)
				for range 2 {
					go func() {
						result, callErr := svc.QueryAndFinalizeRefund(ctx, order.ID)
						results <- outcome{result: result, err: callErr}
					}()
				}
				successes := 0
				for range 2 {
					got := <-results
					if got.err == nil {
						require.True(t, got.result.Success)
						require.Equal(t, 50.0, got.result.BalanceDeducted)
						successes++
					} else {
						require.ErrorContains(t, got.err, "order status changed")
					}
				}
				require.Equal(t, 1, successes)
			} else {
				// Inspect the already reduced balance from the
				// audit trigger before rejecting the success
				// insert, then use outside reads to detect
				// effects that escaped rollback.
				functionName := fmt.Sprintf("test_refund_audit_failure_%d", order.ID)
				triggerName := functionName + "_trigger"
				// Remove the failure trigger before its
				// function on every test exit; IF EXISTS also
				// tolerates the successful retry path's earlier
				// trigger removal.
				t.Cleanup(func() {
					_, cleanupErr := integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON payment_audit_logs", triggerName))
					require.NoError(t, cleanupErr)
					_, cleanupErr = integrationDB.ExecContext(context.Background(), fmt.Sprintf("DROP FUNCTION IF EXISTS %s()", functionName))
					require.NoError(t, cleanupErr)
				})
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf(`
CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
	IF NEW.order_id = '%s' AND NEW.action = 'REFUND_SUCCESS' THEN
		IF (SELECT balance FROM users WHERE id = %d) <> 50 THEN
			RAISE EXCEPTION 'deduction was not visible before audit';
		END IF;
		RAISE EXCEPTION 'forced refund audit failure';
	END IF;
	RETURN NEW;
END;
$$`, functionName, orderID, user.ID))
				require.NoError(t, err)
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("CREATE TRIGGER %s BEFORE INSERT ON payment_audit_logs FOR EACH ROW EXECUTE FUNCTION %s()", triggerName, functionName))
				require.NoError(t, err)

				result, callErr := svc.QueryAndFinalizeRefund(ctx, order.ID)
				require.Nil(t, result)
				require.Equal(t, "REFUND_RECOVERY_REQUIRED", apperror.Reason(callErr))
				require.Equal(t, "could not record the confirmed refund", apperror.FromError(callErr).Metadata["reason"])
				assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 100, payment.OrderStatusRefundPending, 0)

				// Disable the injected audit failure before
				// repeating confirmation so final state can
				// demonstrate a successful retry after the
				// failed transaction rolled back.
				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON payment_audit_logs", triggerName))
				require.NoError(t, err)
				result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
				require.NoError(t, err)
				require.True(t, result.Success)
				require.Equal(t, 50.0, result.BalanceDeducted)
			}

			assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 50, payment.OrderStatusRefunded, 1)
			// Retry after completed confirmation with the same
			// order to check rejection of that terminal state and
			// retained balance, status, and success-audit
			// observations.
			_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.Error(t, err)
			assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 50, payment.OrderStatusRefunded, 1)
		})
	}
}
