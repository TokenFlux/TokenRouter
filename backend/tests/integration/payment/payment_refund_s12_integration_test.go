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

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/stretchr/testify/require"
	stripe "github.com/stripe/stripe-go/v85"
)

// 隔离数据库与本地供应商；只调用公开生产入口。
func s12RefundRefund(t *testing.T, status string) (*dbent.Client, *payment.RefundWorkflow, *dbent.PaymentOrder) {
	t.Helper()
	ctx := context.Background()
	c := testEntClient(t)
	u, e := c.User.Create().SetEmail("s12-refund@example.com").SetPasswordHash("test-hash").SetUsername("s12").SetBalance(100).Save(ctx)
	require.NoError(t, e)
	inst, e := c.PaymentProviderInstance.Create().SetName("s12").SetProviderKey(payment.TypeStripe).SetConfig(`{"secretKey":"test-only","currency":"USD"}`).SetSupportedTypes(payment.TypeStripe).SetRefundEnabled(true).Save(ctx)
	require.NoError(t, e)
	o, e := c.PaymentOrder.Create().SetUserID(u.ID).SetUserEmail(u.Email).SetUserName(u.Username).SetAmount(50).SetPayAmount(50).SetRechargeCode("s12-refund").SetOutTradeNo("s12-refund").SetPaymentType(payment.TypeStripe).SetPaymentTradeNo("pi_s12").SetOrderType(payment.OrderTypeBalance).SetStatus(status).SetRefundAmount(50).SetExpiresAt(time.Now().Add(time.Hour)).SetPaidAt(time.Now()).SetClientIP("127.0.0.1").SetSrcHost("test.local").SetProviderInstanceID(strconv.FormatInt(inst.ID, 10)).Save(ctx)
	require.NoError(t, e)
	t.Cleanup(func() {
		_, e := c.PaymentAuditLog.Delete().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10))).Exec(context.Background())
		require.NoError(t, e)
		require.NoError(t, c.PaymentOrder.DeleteOneID(o.ID).Exec(context.Background()))
		require.NoError(t, c.PaymentProviderInstance.DeleteOneID(inst.ID).Exec(context.Background()))
	})
	s := newPostgresRefundWorkflow(c)
	return c, s, o
}
func s12RefundStripe(t *testing.T, h http.HandlerFunc) {
	t.Helper()
	server := httptest.NewServer(h)
	t.Cleanup(server.Close)
	original := stripe.GetBackend(stripe.APIBackend)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(server.URL), HTTPClient: server.Client(), MaxNetworkRetries: stripe.Int64(0)}))
	t.Cleanup(func() { stripe.SetBackend(stripe.APIBackend, original) })
}
func s12RefundFailAudit(t *testing.T, c *dbent.Client, id int64, action string) {
	t.Helper()
	name := fmt.Sprintf("s12_planning_audit_%d", id)
	ctx := context.Background()
	_, e := integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE FUNCTION %s() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.order_id='%d' AND NEW.action='%s' THEN RAISE EXCEPTION 's12 forced audit failure'; END IF; RETURN NEW; END; $$`, name, id, action))
	require.NoError(t, e)
	_, e = integrationDB.ExecContext(ctx, fmt.Sprintf(`CREATE TRIGGER %s BEFORE INSERT ON payment_audit_logs FOR EACH ROW EXECUTE FUNCTION %s()`, name, name))
	require.NoError(t, e)
	t.Cleanup(func() {
		_, e := integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER IF EXISTS %s ON payment_audit_logs; DROP FUNCTION IF EXISTS %s()", name, name))
		require.NoError(t, e)
	})
}
func TestS12StaleRefundFailureOverwritesSuccess(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	c, s, o := s12RefundRefund(t, payment.OrderStatusRefundPending)
	_, e := c.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(o.ID, 10)).SetAction("REFUND_PENDING").SetOperator("admin").SetDetail(`{"refundID":"re_s12","deductBalance":true,"balanceDeducted":50,"deductionRollbackOK":true}`).Save(ctx)
	require.NoError(t, e)
	entered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
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
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "re_s12", "object": "refund", "status": status})
	})
	done := make(chan struct{})
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
func TestS12PendingRefundAuditLossSkipsDeduction(t *testing.T) {
	ctx := context.Background()
	c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"pending"}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"re_s12","object":"refund","status":"succeeded"}],"has_more":false}`))
	})
	s12RefundFailAudit(t, c, o.ID, "REFUND_PENDING")
	plan, warning, e := s.PrepareRefund(ctx, o.ID, 50, "test", false, true)
	require.NoError(t, e)
	require.Nil(t, warning)
	result, e := s.ExecuteRefund(ctx, plan)
	require.ErrorContains(t, e, "s12 forced audit failure")
	require.Nil(t, result)
	cur, readErr := c.PaymentOrder.Get(ctx, o.ID)
	require.NoError(t, readErr)
	require.Equal(t, payment.OrderStatusRefunding, cur.Status)
	before, readErr := c.User.Get(ctx, o.UserID)
	require.NoError(t, readErr)
	require.Equal(t, 50.0, before.Balance, "补偿与 pending 审计必须一起回滚，保留准备扣减")
	result, e = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.NoError(t, e)
	require.True(t, result.Success)
	u, e := c.User.Get(ctx, o.UserID)
	require.NoError(t, e)
	t.Logf("confirmed refund deducted=%v balance=%v", result.BalanceDeducted, u.Balance)
	require.Equal(t, 50.0, u.Balance, "缺失 pending 审计不能静默跳过权益回收")
}
func TestS12ImmediateRefundAuditFailureIsNotReported(t *testing.T) {
	ctx := context.Background()
	c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))
	})
	s12RefundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
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

// 准备记录失败不得发生渠道调用，预扣、状态与审计必须全部回滚。
func TestS12RefundPreparationAuditFailureStopsChannel(t *testing.T) {
	ctx := context.Background()
	c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
	var calls atomic.Int32
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))
	})
	s12RefundFailAudit(t, c, o.ID, "REFUND_PREPARED")
	plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "prepare failure", false, true)
	require.NoError(t, err)
	require.Nil(t, warning)
	result, err := s.ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.ErrorContains(t, err, "s12 forced audit failure")
	require.Zero(t, calls.Load())
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 100, payment.OrderStatusCompleted, 0)
	count, err := c.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(o.ID, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Count(ctx)
	require.NoError(t, err)
	require.Zero(t, count)
}

// 即时成功落库失败后，只查询既有渠道结果；重复恢复不再次扣减或退款。
func TestS12RefundPreparedRecoveryDoesNotRepeatChannelOrDeduction(t *testing.T) {
	ctx := context.Background()
	c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
	var refunds atomic.Int32
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			refunds.Add(1)
			_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"re_s12","object":"refund","status":"succeeded"}],"has_more":false}`))
	})
	s12RefundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
	plan, warning, err := s.PrepareRefund(ctx, o.ID, 50, "recover", false, true)
	require.NoError(t, err)
	require.Nil(t, warning)
	result, err := s.ExecuteRefund(ctx, plan)
	require.Nil(t, result)
	require.Error(t, err)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunding, 0)
	// 保留故障时再次确认仍不能声称成功，准备扣减也不能再次执行。
	result, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.Nil(t, result)
	require.Error(t, err)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunding, 0)
	name := fmt.Sprintf("s12_planning_audit_%d", o.ID)
	_, err = integrationDB.ExecContext(ctx, fmt.Sprintf("DROP TRIGGER %s ON payment_audit_logs", name))
	require.NoError(t, err)
	result, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.NoError(t, err)
	require.True(t, result.Success)
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunded, 1)
	_, err = s.QueryAndFinalizeRefund(ctx, o.ID)
	require.Error(t, err)
	require.Equal(t, int32(1), refunds.Load())
	assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 50, payment.OrderStatusRefunded, 1)
}

// 旧记录缺失或损坏时不得按默认零扣减完成，也不请求渠道。
func TestS12RefundMissingRecoveryRequiresManualVerification(t *testing.T) {
	for _, status := range []string{payment.OrderStatusRefundPending, payment.OrderStatusRefunding} {
		t.Run(status, func(t *testing.T) {
			ctx := context.Background()
			c, s, o := s12RefundRefund(t, status)
			var calls atomic.Int32
			s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1); http.Error(w, "must not query", 500) })
			result, err := s.QueryAndFinalizeRefund(ctx, o.ID)
			require.Nil(t, result)
			require.ErrorContains(t, err, "manual verification")
			require.Zero(t, calls.Load())
			assertRefundPostgresState(t, ctx, c, o.UserID, o.ID, 100, status, 0)
		})
	}
}

// 新恢复格式的缺字段、损坏和矛盾值都不能退化为默认零扣减。
func TestS12RefundInvalidPreparedFactsRequireManualVerification(t *testing.T) {
	for _, variant := range []string{"missing_choice", "corrupt_json", "contradictory_deduction"} {
		t.Run(variant, func(t *testing.T) {
			ctx := context.Background()
			c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
			var calls atomic.Int32
			s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))
			})
			s12RefundFailAudit(t, c, o.ID, "REFUND_SUCCESS")
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

// 生产迁移已对订单/动作建立唯一索引；必要审计不能让第二次合法尝试永久冲突。
func TestS12RefundRepeatedAttemptsPreserveFactsWithUniqueAction(t *testing.T) {
	ctx := context.Background()
	c, s, o := s12RefundRefund(t, payment.OrderStatusCompleted)
	var existed bool
	require.NoError(t, integrationDB.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_indexes WHERE indexname='idx_payment_audit_logs_order_action_uniq')").Scan(&existed))
	if !existed {
		_, err := integrationDB.ExecContext(ctx, "CREATE UNIQUE INDEX idx_payment_audit_logs_order_action_uniq ON payment_audit_logs(order_id, action)")
		require.NoError(t, err)
		t.Cleanup(func() {
			_, e := integrationDB.ExecContext(context.Background(), "DROP INDEX IF EXISTS idx_payment_audit_logs_order_action_uniq")
			require.NoError(t, e)
		})
	}
	var calls atomic.Int32
	s12RefundStripe(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if calls.Add(1) < 3 {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"fixture refund rejected"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"re_s12","object":"refund","status":"succeeded"}`))
	})
	for attempt := 0; attempt < 3; attempt++ {
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
		OperationID string `json:"operationID"`
		History     []struct {
			OperationID string  `json:"operationID"`
			Balance     float64 `json:"balanceDeducted"`
		} `json:"history"`
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
