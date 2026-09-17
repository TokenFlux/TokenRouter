// 退款闭合事务仅持有本地数据库连接；渠道请求不在本文件执行。
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"

	"entgo.io/ent/dialect/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/google/uuid"
)

type RefundRightsFactory func(*dbent.Tx) payment.RefundRights
type RefundStore struct {
	client *dbent.Client
	rights RefundRightsFactory
}

func NewRefundStore(client *dbent.Client, rights RefundRightsFactory) *RefundStore {
	return &RefundStore{client: client, rights: rights}
}
func (s *RefundStore) Order(ctx context.Context, id int64) (*payment.Order, error) {
	o, e := s.client.PaymentOrder.Get(ctx, id)
	return OrderFromEntity(o), e
}
func refundConflict() error { return apperror.Conflict("CONFLICT", "order status changed") }

// refundAudit 在订单行锁内维护必要事实，兼容原 (order_id,action) 唯一索引。
// 同动作新尝试将旧详情完整归档到 history，不删除或覆盖旧退款事实。
func refundAudit(ctx context.Context, client *dbent.Client, id int64, action string, detail any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("marshal refund audit: %w", err)
	}
	var current map[string]json.RawMessage
	if err = json.Unmarshal(data, &current); err != nil {
		return err
	}
	current["recordedAt"], err = json.Marshal(time.Now().UTC())
	if err != nil {
		return err
	}
	if _, ok := current["version"]; !ok {
		current["version"] = json.RawMessage("1")
	}
	existing, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ(action)).Only(ctx)
	if err != nil && !dbent.IsNotFound(err) {
		return fmt.Errorf("read refund audit: %w", err)
	}
	if existing != nil {
		var previous map[string]json.RawMessage
		if json.Unmarshal([]byte(existing.Detail), &previous) != nil || previous == nil {
			return payment.RefundRecoveryRequired("previous audit record malformed")
		}
		var history []json.RawMessage
		if raw, ok := previous["history"]; ok {
			if json.Unmarshal(raw, &history) != nil {
				return payment.RefundRecoveryRequired("previous audit history malformed")
			}
		}
		delete(previous, "history")
		if _, ok := previous["recordedAt"]; !ok {
			previous["recordedAt"], err = json.Marshal(existing.CreatedAt)
			if err != nil {
				return err
			}
		}
		prior, marshalErr := json.Marshal(previous)
		if marshalErr != nil {
			return marshalErr
		}
		history = append(history, prior)
		current["history"], err = json.Marshal(history)
		if err != nil {
			return err
		}
	}
	data, err = json.Marshal(current)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err = client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(id, 10)).SetAction(action).SetOperator("admin").SetDetail(string(data)).Save(ctx)
	} else {
		_, err = client.PaymentAuditLog.UpdateOneID(existing.ID).SetOperator("admin").SetDetail(string(data)).Save(ctx)
	}
	if err != nil {
		return fmt.Errorf("write refund audit: %w", err)
	}
	return nil
}

// 锁内核对时间值，避免测试 SQLite 的时间文本格式参与版本比较。
// 生产 PostgreSQL 始终使用行锁；SQLite 仅用于非并发契约测试。
func lockedRefundOrder(ctx context.Context, client *dbent.Client, id int64) (*dbent.PaymentOrder, error) {
	return client.PaymentOrder.Query().Where(paymentorder.IDEQ(id)).Where(func(q *sql.Selector) {
		if q.Dialect() != "sqlite3" {
			q.ForUpdate()
		}
	}).Only(ctx)
}
func claimRefund(ctx context.Context, client *dbent.Client, o *payment.Order, status string) error {
	current, err := lockedRefundOrder(ctx, client, o.ID)
	if err != nil {
		return err
	}
	if current.Status != o.Status || !current.UpdatedAt.Equal(o.UpdatedAt) {
		return refundConflict()
	}
	_, err = client.PaymentOrder.UpdateOneID(o.ID).SetStatus(status).Save(ctx)
	return err
}
func (s *RefundStore) PrepareRefund(ctx context.Context, p *payment.RefundPlan, apply payment.RefundMutation) (*payment.RefundReceipt, error) {
	switch p.Order.Status {
	case payment.OrderStatusCompleted, payment.OrderStatusRefundRequested, payment.OrderStatusRefundPending, payment.OrderStatusRefundFailed:
	default:
		return nil, refundConflict()
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = claimRefund(txctx, tx.Client(), p.Order, payment.OrderStatusRefunding); err != nil {
		return nil, err
	}
	// 原 rollback-failed 资料不能证明当前扣减额，拒绝猜测并要求先核实。
	previousFailure, err := tx.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(p.OrderID, 10)), paymentauditlog.ActionEQ("REFUND_ROLLBACK_FAILED")).Exist(txctx)
	if err != nil {
		return nil, err
	}
	if previousFailure {
		return nil, payment.RefundRecoveryRequired("legacy deduction rollback failure")
	}
	local := *p
	if err = apply(txctx, s.rights(tx), &local); err != nil {
		return nil, err
	}
	order, err := tx.PaymentOrder.UpdateOneID(p.OrderID).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).SetForceRefund(p.Force).Save(txctx)
	if err != nil {
		return nil, err
	}
	// 数据库时间精度是操作版本的权威来源；避免内存纳秒与 PostgreSQL 微秒不同。
	order, err = tx.PaymentOrder.Get(txctx, order.ID)
	if err != nil {
		return nil, err
	}
	receipt := &payment.RefundReceipt{
		Version:             1,
		OperationID:         uuid.NewString(),
		OrderID:             p.OrderID,
		OperationVersion:    order.UpdatedAt,
		PreviousStatus:      p.Order.Status,
		RefundAmount:        p.RefundAmount,
		GatewayAmount:       p.GatewayAmount,
		Reason:              p.Reason,
		Force:               p.Force,
		RefundPendingDetail: payment.RefundPendingDetail{DeductBalance: p.DeductBalance, DeductionType: p.DeductionType, BalanceDeducted: local.BalanceToDeduct, SubDaysDeducted: local.SubDaysToDeduct, SubscriptionID: p.SubscriptionID},
	}
	if !validRefundDeduction(receipt.RefundPendingDetail, false) {
		return nil, payment.RefundRecoveryRequired("preparation deduction is contradictory")
	}
	if err = refundAudit(txctx, tx.Client(), p.OrderID, "REFUND_PREPARED", receipt); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	local.OperationID = receipt.OperationID
	*p = local
	return receipt, nil
}
func (s *RefundStore) expected(ctx context.Context, client *dbent.Client, p *payment.RefundPlan, r *payment.RefundReceipt) error {
	expected := *p.Order
	if r != nil {
		expected.Status = payment.OrderStatusRefunding
		expected.UpdatedAt = r.OperationVersion
		current, err := readRefundReceipt(ctx, client, p.OrderID)
		if err != nil {
			return err
		}
		if current.OperationID != r.OperationID || !current.OperationVersion.Equal(r.OperationVersion) {
			return refundConflict()
		}
	} else if expected.Status != payment.OrderStatusRefundPending {
		return refundConflict()
	}
	if r == nil {
		if err := matchPendingRefundIdentity(ctx, client, p.OrderID, p.OperationID, p.ChannelRefundID); err != nil {
			return err
		}
	}
	return claimRefund(ctx, client, &expected, payment.OrderStatusRefunding)
}
func (s *RefundStore) CompleteRefund(ctx context.Context, p *payment.RefundPlan, r *payment.RefundReceipt, apply payment.RefundMutation) (*payment.RefundResult, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.expected(txctx, tx.Client(), p, r); err != nil {
		return nil, err
	}
	local := *p
	if apply != nil {
		if err = apply(txctx, s.rights(tx), &local); err != nil {
			return nil, err
		}
	}
	status := payment.OrderStatusRefunded
	if local.RefundAmount < local.Order.Amount {
		status = payment.OrderStatusPartiallyRefunded
	}
	_, err = tx.PaymentOrder.UpdateOneID(p.OrderID).SetStatus(status).SetRefundAmount(local.RefundAmount).SetRefundReason(local.Reason).SetRefundAt(time.Now()).SetForceRefund(local.Force).Save(txctx)
	if err != nil {
		return nil, fmt.Errorf("mark refund: %w", err)
	}
	detail := map[string]any{
		"refundAmount":    local.RefundAmount,
		"reason":          local.Reason,
		"balanceDeducted": local.BalanceToDeduct,
		"subDaysDeducted": local.SubDaysToDeduct,
		"force":           local.Force,
		"operationID":     local.OperationID,
	}
	if err = refundAudit(txctx, tx.Client(), p.OrderID, "REFUND_SUCCESS", detail); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit refund finalization: %w", err)
	}
	*p = local
	return &payment.RefundResult{Success: true, BalanceDeducted: local.BalanceToDeduct, SubDaysDeducted: local.SubDaysToDeduct}, nil
}
func (s *RefundStore) CompensateRefund(ctx context.Context, p *payment.RefundPlan, r *payment.RefundReceipt, refundID, failure string, apply payment.RefundMutation) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if err = s.expected(txctx, tx.Client(), p, r); err != nil {
		return err
	}
	local := *p
	if err = apply(txctx, s.rights(tx), &local); err != nil {
		return fmt.Errorf("refund compensation failed: %w", err)
	}
	update := tx.PaymentOrder.UpdateOneID(p.OrderID)
	var action string
	var detail any
	if failure != "" {
		status := payment.OrderStatusCompleted
		if r.PreviousStatus == payment.OrderStatusRefundRequested {
			status = payment.OrderStatusRefundRequested
		}
		update.SetStatus(status)
		action = "REFUND_GATEWAY_FAILED"
		detail = map[string]any{"detail": failure, "operationID": r.OperationID, "deductionRollbackOK": true}
	} else {
		update.SetStatus(payment.OrderStatusRefundPending).SetRefundAmount(p.RefundAmount).SetRefundReason(p.Reason).ClearRefundAt().SetForceRefund(p.Force).ClearFailedAt().ClearFailedReason()
		action = "REFUND_PENDING"
		detail = map[string]any{
			"refundID":            refundID,
			"refundAmount":        p.RefundAmount,
			"reason":              p.Reason,
			"force":               p.Force,
			"deductBalance":       p.DeductBalance,
			"deductionType":       p.DeductionType,
			"balanceDeducted":     p.BalanceToDeduct,
			"subDaysDeducted":     p.SubDaysToDeduct,
			"subscriptionID":      p.SubscriptionID,
			"balanceRolledBack":   p.BalanceToDeduct,
			"subDaysRolledBack":   p.SubDaysToDeduct,
			"deductionRollbackOK": true,
			"operationID":         r.OperationID,
			"version":             1,
		}
	}
	if _, err = update.Save(txctx); err != nil {
		return err
	}
	if err = refundAudit(txctx, tx.Client(), p.OrderID, action, detail); err != nil {
		return err
	}
	return tx.Commit()
}
func (s *RefundStore) FailPendingRefund(ctx context.Context, o *payment.Order, claim payment.RefundPendingDetail, failure string) (*payment.Order, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	txctx := dbent.NewTxContext(ctx, tx)
	if o.Status != payment.OrderStatusRefundPending {
		return nil, refundConflict()
	}
	current, err := lockedRefundOrder(txctx, tx.Client(), o.ID)
	if err != nil {
		return nil, err
	}
	if current.Status == o.Status && current.UpdatedAt.Equal(o.UpdatedAt) {
		if err = matchPendingRefundIdentity(txctx, tx.Client(), o.ID, claim.OperationID, claim.RefundID); err != nil {
			return nil, err
		}
		current, err = tx.PaymentOrder.UpdateOneID(o.ID).SetStatus(payment.OrderStatusRefundFailed).SetFailedAt(time.Now()).SetFailedReason(failure).Save(txctx)
		if err != nil {
			return nil, err
		}
		if err = refundAudit(txctx, tx.Client(), o.ID, "REFUND_FAILED", map[string]any{"detail": failure}); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return OrderFromEntity(current), nil
}
func readRefundReceipt(ctx context.Context, client *dbent.Client, id int64) (*payment.RefundReceipt, error) {
	log, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PREPARED")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	if err != nil {
		return nil, payment.RefundRecoveryRequired("preparation record unavailable")
	}
	var required map[string]json.RawMessage
	if json.Unmarshal([]byte(log.Detail), &required) != nil {
		return nil, payment.RefundRecoveryRequired("preparation record malformed")
	}
	for _, field := range []string{
		"version",
		"operationID",
		"orderID",
		"operationVersion",
		"previousStatus",
		"refundAmount",
		"gatewayAmount",
		"reason",
		"force",
		"deductBalance",
		"deductionType",
		"balanceDeducted",
		"subDaysDeducted",
		"subscriptionID",
	} {
		if raw, ok := required[field]; !ok || string(raw) == "null" {
			return nil, payment.RefundRecoveryRequired("preparation record incomplete: " + field)
		}
	}
	var receipt payment.RefundReceipt
	if err = json.Unmarshal([]byte(log.Detail), &receipt); err != nil {
		return nil, payment.RefundRecoveryRequired("preparation record malformed")
	}
	if receipt.Version != 1 || receipt.OperationID == "" || receipt.OrderID != id || receipt.OperationVersion.IsZero() || !validRefundAmount(receipt.RefundAmount) || !validRefundAmount(receipt.GatewayAmount) || !validRefundAmount(receipt.BalanceDeducted) || receipt.SubDaysDeducted < 0 || !validRefundDeduction(receipt.RefundPendingDetail, false) {
		return nil, payment.RefundRecoveryRequired("preparation record invalid")
	}
	return &receipt, nil
}
func (s *RefundStore) RefundRecovery(ctx context.Context, o *payment.Order) (*payment.RefundReceipt, error) {
	r, err := readRefundReceipt(ctx, s.client, o.ID)
	if err != nil {
		return nil, err
	}
	if o.Status != payment.OrderStatusRefunding || !o.UpdatedAt.Equal(r.OperationVersion) || o.RefundAmount != r.RefundAmount || o.ForceRefund != r.Force || o.RefundReason == nil || *o.RefundReason != r.Reason {
		return nil, payment.RefundRecoveryRequired("preparation record contradicts order")
	}
	return r, nil
}
func validRefundAmount(v float64) bool { return v >= 0 && !math.IsNaN(v) && !math.IsInf(v, 0) }
func (s *RefundStore) PendingDetail(ctx context.Context, id int64) (payment.RefundPendingDetail, error) {
	var d payment.RefundPendingDetail
	log, err := s.client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	if err != nil {
		return d, payment.RefundRecoveryRequired("pending record unavailable")
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal([]byte(log.Detail), &fields) != nil || (fields["deductBalance"] == nil || string(fields["deductBalance"]) == "null") {
		return d, payment.RefundRecoveryRequired("pending record malformed or incomplete")
	}
	d.DeductionRollbackOK = true
	if json.Unmarshal([]byte(log.Detail), &d) != nil || !validRefundAmount(d.BalanceDeducted) || d.SubDaysDeducted < 0 || !validRefundDeduction(d, true) {
		return d, payment.RefundRecoveryRequired("pending deduction invalid")
	}
	if d.DeductBalance && fields["balanceDeducted"] == nil && fields["subDaysDeducted"] == nil {
		return d, payment.RefundRecoveryRequired("pending deduction missing")
	}
	// 新格式必须携带版本和准备关联，不能在损坏后悄悄按旧格式解释。
	if raw, ok := fields["version"]; ok {
		var version int
		if json.Unmarshal(raw, &version) != nil || version != 1 || d.OperationID == "" || !validRefundDeduction(d, false) {
			return d, payment.RefundRecoveryRequired("pending record version or deduction invalid")
		}
	}
	// 新记录必须能关联原准备事实；旧记录保持原字段与缺省 rollback 兼容。
	if d.OperationID != "" {
		r, e := readRefundReceipt(ctx, s.client, id)
		if e != nil || r.OperationID != d.OperationID || r.DeductBalance != d.DeductBalance || r.DeductionType != d.DeductionType || r.BalanceDeducted != d.BalanceDeducted || r.SubDaysDeducted != d.SubDaysDeducted || r.SubscriptionID != d.SubscriptionID {
			return d, payment.RefundRecoveryRequired("pending preparation mismatch")
		}
	}
	return d, nil
}

// AppendObservation 仅供普通渠道过程审计，调用者保留原尽力错误处理。
func (s *RefundStore) AppendObservation(ctx context.Context, id int64, action, operator string, detail map[string]any) error {
	data, err := json.Marshal(detail)
	if err != nil {
		return err
	}
	_, err = s.client.PaymentAuditLog.Create().SetOrderID(strconv.FormatInt(id, 10)).SetAction(action).SetOperator(operator).SetDetail(string(data)).Save(ctx)
	return err
}

// RequestRefund 保留原申请状态条件和尽力审计边界，未执行任何资金操作。
func (s *RefundStore) RequestRefund(ctx context.Context, id, userID int64, amount float64, reason string, now time.Time, by string) (int, error) {
	return s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id), paymentorder.UserIDEQ(userID), paymentorder.StatusEQ(payment.OrderStatusCompleted), paymentorder.OrderTypeEQ(payment.OrderTypeBalance)).SetStatus(payment.OrderStatusRefundRequested).SetRefundRequestedAt(now).SetRefundRequestReason(reason).SetRefundRequestedBy(by).SetRefundAmount(amount).Save(ctx)
}

// 旧记录按渠道退款 ID 核对；新记录同时核对准备操作标识，均不改渠道幂等键。
func matchPendingRefundIdentity(ctx context.Context, client *dbent.Client, id int64, operationID, refundID string) error {
	row, err := client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ("REFUND_PENDING")).Order(paymentauditlog.ByCreatedAt(sql.OrderDesc()), paymentauditlog.ByID(sql.OrderDesc())).First(ctx)
	if err != nil {
		return payment.RefundRecoveryRequired("pending identity unavailable")
	}
	var actual payment.RefundPendingDetail
	if json.Unmarshal([]byte(row.Detail), &actual) != nil {
		return payment.RefundRecoveryRequired("pending identity malformed")
	}
	if actual.OperationID != operationID || actual.RefundID != refundID {
		return refundConflict()
	}
	return nil
}

// 恢复记录必须表达一致的扣减事实；旧 pending 可省略类型，但不能省略扣减选择。
// 旧格式显式不扣减时保留其优先级，旧金额字段可能仍保存请求值而非实际扣减。
func validRefundDeduction(d payment.RefundPendingDetail, legacy bool) bool {
	if d.SubscriptionID < 0 || !validRefundAmount(d.BalanceDeducted) || d.SubDaysDeducted < 0 {
		return false
	}
	if !legacy && !d.DeductBalance && (d.BalanceDeducted != 0 || d.SubDaysDeducted != 0) {
		return false
	}
	switch d.DeductionType {
	case payment.DeductionTypeNone:
		return d.BalanceDeducted == 0 && d.SubDaysDeducted == 0 && d.SubscriptionID == 0
	case payment.DeductionTypeBalance:
		return d.SubDaysDeducted == 0 && d.SubscriptionID == 0
	case payment.DeductionTypeSubscription:
		return d.BalanceDeducted == 0
	case "":
		return legacy
	default:
		return false
	}
}
