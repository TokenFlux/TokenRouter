//go:build integration

package repository

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

	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"

	stripe "github.com/stripe/stripe-go/v85"
)

// TestPaymentRefundPostgresAtomicity 通过公开退款确认入口验证真实仓储参与外层事务。
// 仅替换供应商 HTTP，余额 SQL、订单认领、审计写入和提交均使用生产实现。
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
				if r.Method != http.MethodGet || r.URL.Path != "/v1/refunds/re_s00" {
					http.Error(w, "unexpected refund request", http.StatusBadRequest)
					return
				}
				// 两个调用均先读到 pending，再同时进入真实数据库认领。
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
				_ = json.NewEncoder(w).Encode(map[string]string{
					"id": "re_s00", "object": "refund", "status": "succeeded",
				})
			}))
			t.Cleanup(server.Close)
			originalBackend := stripe.GetBackend(stripe.APIBackend)
			stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{
				URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0),
			}))
			t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, originalBackend) })

			user, err := client.User.Create().SetEmail("s00-refund@example.com").
				SetPasswordHash("test-hash").SetUsername("s00-refund").SetBalance(100).Save(ctx)
			require.NoError(t, err)
			instance, err := client.PaymentProviderInstance.Create().SetProviderKey(payment.TypeStripe).
				SetName("s00-refund").SetConfig(`{"secretKey":"test-only-key","currency":"USD"}`).
				SetSupportedTypes(payment.TypeStripe).SetRefundEnabled(true).Save(ctx)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, client.PaymentProviderInstance.DeleteOneID(instance.ID).Exec(context.Background()))
			})
			order, err := client.PaymentOrder.Create().
				SetUserID(user.ID).SetUserEmail(user.Email).SetUserName(user.Username).
				SetAmount(50).SetPayAmount(50).SetRechargeCode("s00-refund").
				SetOutTradeNo("s00-refund").SetPaymentType(payment.TypeStripe).
				SetPaymentTradeNo("pi_s00").SetOrderType(payment.OrderTypeBalance).
				SetStatus(service.OrderStatusRefundPending).SetRefundAmount(50).
				SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).
				SetClientIP("127.0.0.1").SetSrcHost("test.local").
				SetProviderInstanceID(strconv.FormatInt(instance.ID, 10)).Save(ctx)
			require.NoError(t, err)
			orderID := strconv.FormatInt(order.ID, 10)
			// 订单与审计没有依赖 users 的级联清理，需要显式回收本用例的数据。
			t.Cleanup(func() {
				_, cleanupErr := client.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(orderID)).Exec(context.Background())
				require.NoError(t, cleanupErr)
				require.NoError(t, client.PaymentOrder.DeleteOneID(order.ID).Exec(context.Background()))
			})
			_, err = client.PaymentAuditLog.Create().SetOrderID(orderID).SetAction("REFUND_PENDING").
				SetOperator("admin").SetDetail(`{"refundID":"re_s00","deductBalance":true,"balanceDeducted":50,"deductionRollbackOK":true}`).Save(ctx)
			require.NoError(t, err)
			svc := service.NewPaymentService(client, payment.NewRegistry(),
				payment.NewDefaultLoadBalancer(paymentpostgres.NewInstanceStore(client), nil), nil, nil, nil,
				NewUserRepository(client, integrationDB), nil, nil)

			if concurrent {
				type outcome struct {
					result *service.RefundResult
					err    error
				}
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
				// 触发器先确认扣款已在同一事务内可见，再使最后的审计写入失败。
				functionName := fmt.Sprintf("s00_refund_audit_failure_%d", order.ID)
				triggerName := functionName + "_trigger"
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
				require.ErrorContains(t, callErr, "forced refund audit failure")
				assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 100, service.OrderStatusRefundPending, 0)

				_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON payment_audit_logs", triggerName))
				require.NoError(t, err)
				result, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
				require.NoError(t, err)
				require.True(t, result.Success)
				require.Equal(t, 50.0, result.BalanceDeducted)
			}

			assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 50, service.OrderStatusRefunded, 1)
			// 已完成订单再次确认必须拒绝，不能触发第二次资金回收。
			_, err = svc.QueryAndFinalizeRefund(ctx, order.ID)
			require.Error(t, err)
			assertRefundPostgresState(t, ctx, client, user.ID, order.ID, 50, service.OrderStatusRefunded, 1)
		})
	}
}

// assertRefundPostgresState 从事务外读取三个持久化结果，避免仅检查返回值。
func assertRefundPostgresState(t *testing.T, ctx context.Context, client *dbent.Client, userID, orderID int64, balance float64, status string, audits int) {
	t.Helper()
	user, err := client.User.Get(ctx, userID)
	require.NoError(t, err)
	require.Equal(t, balance, user.Balance)
	order, err := client.PaymentOrder.Get(ctx, orderID)
	require.NoError(t, err)
	require.Equal(t, status, order.Status)
	count, err := client.PaymentAuditLog.Query().
		Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(orderID, 10)), paymentauditlog.ActionEQ("REFUND_SUCCESS")).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, audits, count)
}
