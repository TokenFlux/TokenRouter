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

// order_lifecycle.go selects orders for recovery and expiration and commits
// forced expiration together with its audit record.
// Background recovery first reads order IDs and later rereads selected
// processing rows, so status changes can remove candidates before provider
// queries. Fulfillment candidates require payment confirmation time and an
// elapsed retry or stale-lease delay, while pending reconciliation chooses
// older unexpired Alipay/WeChat/GMPay references.
//
// Ordinary timeout handling reads expired pending rows and lets the lifecycle
// query upstream before closing. Administrator force expiration instead owns a
// short conditional update and mandatory reason audit transaction, with no
// provider request. Losing pending status is a conflict, and audit failure
// returns before commit. TouchPending records cancellation retry time only for
// current pending rows; upstream reference saving has its separate
// unconditioned update and is used before notification processing.

// RecoverableFulfillmentIDs returns ascending paid_at-bearing order IDs whose
// update time reaches the delay selected by their current state. Paid/failed
// states use retryDelay, while recharging uses leaseDuration; both time
// comparisons are inclusive.
//
// This query observes candidates before fulfillment claims them. The later
// ClaimFulfillment update checks paid/failed state or recharging staleness, but
// does not repeat this query's paid_at or paid/failed retry-delay predicates.
// A candidate changed after this read is therefore handled by that narrower
// claim and the fulfillment executor's current-state reread.
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

// ProcessingIDs returns all currently provider-processing order IDs in
// ascending order.
func (s *OrderStore) ProcessingIDs(ctx context.Context) ([]int64, error) {
	return s.client.PaymentOrder.Query().
		Where(paymentorder.StatusEQ(payment.OrderStatusProcessing)).
		Order(paymentorder.ByID()).
		IDs(ctx)
}

// ProcessingOrders returns selected IDs still in processing state, sorted by
// local ID. The status predicate rereads current membership after the
// lifecycle's earlier ID page selection, and conversion uses OrderFromEntity.
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

// ExpiredPending reads pending orders whose expiration is at or before now. It
// applies no row bound or sort order here; lifecycle still checks retry
// cooldown and provider state before closing them.
func (s *OrderStore) ExpiredPending(ctx context.Context, now time.Time) ([]*payment.Order, error) {
	rows, err := s.client.PaymentOrder.Query().Where(paymentorder.StatusEQ(payment.OrderStatusPending), paymentorder.ExpiresAtLTE(now)).All(ctx)
	return orderValues(rows), err
}

// PendingReconciliation reads unexpired pending Alipay/WeChat/GMPay method or
// provider rows, oldest creation first and bounded by the supplied limit.
// Canonical or underscore-prefixed labels qualify; lifecycle queries each
// candidate before deciding payment progress.
func (s *OrderStore) PendingReconciliation(ctx context.Context, now time.Time, limit int) ([]*payment.Order, error) {
	rows, err := s.client.PaymentOrder.Query().
		Where(
			paymentorder.StatusEQ(payment.OrderStatusPending),
			paymentorder.ExpiresAtGT(now),
			paymentorder.Or(
				paymentorder.ProviderKeyEQ(payment.TypeEPUSDT),
				paymentorder.PaymentTypeEQ(payment.TypeEPUSDT),
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

// ForceExpire conditionally marks a pending order expired and persists its
// administrator audit in one owned transaction. A missing row returns
// not-found; an existing row that lost pending state returns
// ORDER_STATUS_CHANGED after reread.
//
// The operation makes no provider call and relies on the caller's reason
// validation. It writes the audit after the conditional state update and
// commits only after both succeed, retaining rollback cleanup for earlier
// failure paths. Verified late payment can still recover the expired order
// through normal fulfillment. A commit error is returned, and the caller
// retains that error rather than assuming the external provider was cancelled.
func (s *OrderStore) ForceExpire(ctx context.Context, orderID int64, reason string) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin force expire transaction: %w", err)
	}
	// This store owns state and mandatory administrator audit in one transaction.
	// Keep rollback cleanup for early returns so an audit failure cannot publish
	// the preceding expiration update.
	defer func() { _ = tx.Rollback() }()

	// Claim the still-pending state before writing its force-expiration audit. A
	// lost condition rereads the row only to distinguish absence from an
	// intervening status change.
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
	// Persist the reason with conditional expiration before committing.
	// CountForcedExpiredByProvider reads this text-keyed action to retain the
	// instance for late-payment recovery during ConfigService deletion.
	if _, err := tx.PaymentAuditLog.Create().
		SetOrderID(strconv.FormatInt(orderID, 10)).
		SetAction("ORDER_FORCE_EXPIRED").
		SetDetail(string(detail)).
		SetOperator("admin").
		Save(ctx); err != nil {
		return fmt.Errorf("write force expiration audit log: %w", err)
	}
	// Publish expiration and its required audit together after both writes
	// succeed. Commit errors propagate while deferred cleanup attempts to finish
	// the owned transaction.
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit force expire transaction: %w", err)
	}
	return nil
}

// TouchPending updates the supplied timestamp only while the addressed order
// remains pending. The affected count is discarded, so a changed or absent row
// can yield nil error while cancellation recovery proceeds.
func (s *OrderStore) TouchPending(ctx context.Context, id int64, now time.Time) error {
	_, err := s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id), paymentorder.StatusEQ(payment.OrderStatusPending)).SetUpdatedAt(now).Save(ctx)
	return err
}

// SaveUpstreamTradeNumber writes the supplied provider reference for the
// addressed order without a status condition. Its affected count is discarded;
// reconciliation uses the error result before passing an accepted observation
// to notification handling.
func (s *OrderStore) SaveUpstreamTradeNumber(ctx context.Context, id int64, trade string) error {
	_, err := s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id)).SetPaymentTradeNo(trade).Save(ctx)
	return err
}
