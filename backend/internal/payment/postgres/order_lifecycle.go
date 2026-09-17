// 后台批量查询与强制到期事务保留原 SQL 和审计边界。
package postgres

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *OrderStore) RecoverableFulfillmentIDs(ctx context.Context, now time.Time, retryDelay, leaseDuration time.Duration) ([]int64, error) {
	return s.client.PaymentOrder.Query().
		Where(
			paymentorder.PaidAtNotNil(),
			paymentorder.Or(
				paymentorder.And(
					paymentorder.StatusIn(payment.OrderStatusPaid, payment.OrderStatusFailed),
					paymentorder.UpdatedAtLTE(now.Add(-retryDelay)),
				),
				paymentorder.And(
					paymentorder.StatusEQ(payment.OrderStatusRecharging),
					paymentorder.UpdatedAtLTE(now.Add(-leaseDuration)),
				),
			),
		).
		Order(paymentorder.ByID()).
		IDs(ctx)
}
func (s *OrderStore) ProcessingIDs(ctx context.Context) ([]int64, error) {
	return s.client.PaymentOrder.Query().
		Where(paymentorder.StatusEQ(payment.OrderStatusProcessing)).
		Order(paymentorder.ByID()).
		IDs(ctx)
}
func (s *OrderStore) ProcessingOrders(ctx context.Context, pageIDs []int64) ([]*payment.Order, error) {
	rows, err := s.client.PaymentOrder.Query().
		Where(
			paymentorder.IDIn(pageIDs...),
			paymentorder.StatusEQ(payment.OrderStatusProcessing),
		).
		Order(paymentorder.ByID()).
		All(ctx)
	return orderValues(rows), err
}
func (s *OrderStore) ExpiredPending(ctx context.Context, now time.Time) ([]*payment.Order, error) {
	rows, err := s.client.PaymentOrder.Query().Where(paymentorder.StatusEQ(payment.OrderStatusPending), paymentorder.ExpiresAtLTE(now)).All(ctx)
	return orderValues(rows), err
}
func (s *OrderStore) PendingReconciliation(ctx context.Context, now time.Time, limit int) ([]*payment.Order, error) {
	rows, err := s.client.PaymentOrder.Query().
		Where(
			paymentorder.StatusEQ(payment.OrderStatusPending),
			paymentorder.ExpiresAtGT(now),
			paymentorder.Or(
				paymentorder.PaymentTypeEQ(payment.TypeWxpay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeWxpay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeWxpay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeWxpay+"_"),
				paymentorder.PaymentTypeEQ(payment.TypeAlipay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeAlipay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeAlipay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeAlipay+"_"),
			),
		).
		Order(dbent.Asc(paymentorder.FieldCreatedAt)).
		Limit(limit).
		All(ctx)
	return orderValues(rows), err
}
func (s *OrderStore) ForceExpire(ctx context.Context, orderID int64, reason string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin force expire transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	updated, err := tx.PaymentOrder.Update().
		Where(paymentorder.IDEQ(orderID), paymentorder.StatusEQ(payment.OrderStatusPending)).
		SetStatus(payment.OrderStatusExpired).
		Save(ctx)
	if err != nil {
		return fmt.Errorf("force expire payment order: %w", err)
	}
	if updated == 0 {
		if _, err := tx.PaymentOrder.Get(ctx, orderID); err != nil {
			if !dbent.IsNotFound(err) {
				return fmt.Errorf("reload payment order after force expiration race: %w", err)
			}
			return infraerrors.NotFound("NOT_FOUND", "order not found")
		}
		return infraerrors.Conflict("ORDER_STATUS_CHANGED", "order status changed before force expiration")
	}

	detail, err := json.Marshal(map[string]any{
		"previous_status":        payment.OrderStatusPending,
		"reason":                 reason,
		"upstream_check_skipped": true,
	})
	if err != nil {
		return fmt.Errorf("marshal force expiration audit detail: %w", err)
	}
	if _, err := tx.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction("ORDER_FORCE_EXPIRED").
		SetDetail(string(detail)).
		SetOperator("admin").
		Save(ctx); err != nil {
		return fmt.Errorf("write force expiration audit log: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit force expire transaction: %w", err)
	}
	return nil
}
func (s *OrderStore) TouchPending(ctx context.Context, id int64, now time.Time) error {
	_, err := s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id), paymentorder.StatusEQ(payment.OrderStatusPending)).SetUpdatedAt(now).Save(ctx)
	return err
}
func (s *OrderStore) SaveUpstreamTradeNumber(ctx context.Context, id int64, trade string) error {
	_, err := s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id)).SetPaymentTradeNo(trade).Save(ctx)
	return err
}
