// 下单事务保持限额读取、订单写入及充值码生成的原顺序。
package postgres

import (
	"context"
	"fmt"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *OrderStore) CreateCheckout(ctx context.Context, draft payment.CheckoutDraft) (*payment.Order, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	count, err := tx.PaymentOrder.Query().Where(paymentorder.UserIDEQ(draft.Order.UserID), paymentorder.StatusIn(payment.OrderStatusPending, payment.OrderStatusProcessing)).Count(ctx)
	if err != nil {
		return nil, fmt.Errorf("count pending orders: %w", err)
	}
	if err = payment.ValidateCheckoutPending(count, draft.MaxPending); err != nil {
		return nil, err
	}
	if draft.DailyLimit > 0 {
		now := time.Now().UTC()
		start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
		rows, e := tx.PaymentOrder.Query().Where(paymentorder.UserIDEQ(draft.Order.UserID), paymentorder.StatusIn(payment.OrderStatusPaid, payment.OrderStatusRecharging, payment.OrderStatusCompleted), paymentorder.PaidAtGTE(start)).All(ctx)
		if e != nil {
			return nil, fmt.Errorf("query daily usage: %w", e)
		}
		if err = payment.ValidateCheckoutDaily(orderValues(rows), draft.LimitAmount, draft.DailyLimit); err != nil {
			return nil, err
		}
	}
	exp := time.Now().Add(time.Duration(draft.TimeoutMinutes) * time.Minute)
	outTradeNo, err := allocateCheckoutTradeNumber(ctx, tx)
	if err != nil {
		return nil, err
	}
	selectedInstanceID := payment.RefundStringValue(draft.Order.ProviderInstanceID)
	selectedProviderKey := payment.RefundStringValue(draft.Order.ProviderKey)
	providerSnapshot := draft.Order.ProviderSnapshot
	b := tx.PaymentOrder.Create().
		SetUserID(draft.Order.UserID).
		SetUserEmail(draft.Order.UserEmail).
		SetUserName(draft.Order.UserName).
		SetNillableUserNotes(draft.Order.UserNotes).
		SetAmount(draft.Order.Amount).
		SetPayAmount(draft.Order.PayAmount).
		SetFeeRate(draft.Order.FeeRate).
		SetFeeFixed(draft.Order.FeeFixed).
		SetFeeRateAmount(draft.Order.FeeRateAmount).
		SetFeeAmount(draft.Order.FeeAmount).
		SetRechargeCode("").
		SetOutTradeNo(outTradeNo).
		SetPaymentType(draft.Order.PaymentType).
		SetPaymentTradeNo("").
		SetOrderType(draft.Order.OrderType).
		SetStatus(payment.OrderStatusPending).
		SetExpiresAt(exp).
		SetClientIP(draft.Order.ClientIP).
		SetSrcHost(draft.Order.SrcHost)
	if draft.Order.SrcURL != nil {
		b.SetSrcURL(*draft.Order.SrcURL)
	}
	if selectedInstanceID != "" {
		b.SetProviderInstanceID(selectedInstanceID)
	}
	if selectedProviderKey != "" {
		b.SetProviderKey(selectedProviderKey)
	}
	if providerSnapshot != nil {
		b.SetProviderSnapshot(providerSnapshot)
	}
	if draft.Order.BillingSnapshot != nil {
		b.SetBillingSnapshot(draft.Order.BillingSnapshot)
	}
	if draft.Order.PlanID != nil {
		b.SetPlanID(*draft.Order.PlanID).SetPlanSnapshot(draft.Order.PlanSnapshot)
	}

	order, err := b.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("create order: %w", err)
	}
	code := fmt.Sprintf("PAY-%d-%d", order.ID, time.Now().UnixNano()%100000)
	order, err = tx.PaymentOrder.UpdateOneID(order.ID).SetRechargeCode(code).Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("set recharge code: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit order transaction: %w", err)
	}
	return OrderFromEntity(order), nil
}
func allocateCheckoutTradeNumber(ctx context.Context, tx *dbent.Tx) (string, error) {
	const maxAttempts = 5
	for attempt := 0; attempt < maxAttempts; attempt++ {
		candidate := payment.GenerateOutTradeNo()
		exists, err := tx.PaymentOrder.Query().Where(paymentorder.OutTradeNo(candidate)).Exist(ctx)
		if err != nil {
			return "", fmt.Errorf("check out_trade_no uniqueness: %w", err)
		}
		if !exists {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("generate unique out_trade_no: exhausted %d attempts", maxAttempts)
}

// PersistCheckoutResponse 将渠道实际返回的支付详情和有效期同步到本地订单。
func (s *OrderStore) PersistCheckoutResponse(ctx context.Context, orderID int64, sel *payment.InstanceSelection, pr *payment.CreatePaymentResponse) (*payment.Order, error) {
	if pr == nil {
		return nil, fmt.Errorf("payment provider returned an empty create response")
	}
	orderUpdate := s.client.PaymentOrder.UpdateOneID(orderID).
		SetNillablePaymentTradeNo(payment.NilIfEmpty(pr.TradeNo)).
		SetNillablePayURL(payment.NilIfEmpty(pr.PayURL)).
		SetNillableQrCode(payment.NilIfEmpty(pr.QRCode)).
		SetNillableProviderInstanceID(payment.NilIfEmpty(sel.InstanceID)).
		SetNillableProviderKey(payment.NilIfEmpty(sel.ProviderKey)).
		SetNillablePaymentCustomerID(payment.NilIfEmpty(pr.CustomerID)).
		SetNillablePaymentInvoiceID(payment.NilIfEmpty(pr.InvoiceID)).
		SetNillablePaymentInvoiceURL(payment.NilIfEmpty(pr.InvoiceURL)).
		SetNillablePaymentInvoicePdfURL(payment.NilIfEmpty(pr.InvoicePDF)).
		SetNillablePaymentInvoiceStatus(payment.NilIfEmpty(pr.InvoiceStatus))
	if !pr.ExpiresAt.IsZero() {
		orderUpdate = orderUpdate.SetExpiresAt(pr.ExpiresAt)
	}
	order, err := orderUpdate.Save(ctx)
	if err != nil {
		return nil, fmt.Errorf("update order with payment details: %w", err)
	}
	return OrderFromEntity(order), nil
}
func (s *OrderStore) FailCheckout(ctx context.Context, id int64) error {
	_, err := s.client.PaymentOrder.UpdateOneID(id).SetStatus(payment.OrderStatusFailed).Save(ctx)
	return err
}
func (s *OrderStore) CancelledCount(ctx context.Context, operator string, since time.Time) (int, error) {
	return s.client.PaymentAuditLog.Query().Where(paymentauditlog.ActionEQ("ORDER_CANCELLED"), paymentauditlog.OperatorEQ(operator), paymentauditlog.CreatedAtGTE(since)).Count(ctx)
}
