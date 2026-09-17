// 状态条件与租约版本保持原 CAS；核心决定下一状态，存储仅写显式字段。
package postgres

import (
	"context"
	"strconv"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/ent/paymentauditlog"
	"github.com/TokenFlux/TokenRouter/ent/paymentorder"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *OrderStore) IsNotFound(err error) bool { return dbent.IsNotFound(err) }
func (s *OrderStore) OrderByTradeNumber(ctx context.Context, no string) (*payment.Order, error) {
	o, e := s.client.PaymentOrder.Query().Where(paymentorder.OutTradeNo(no)).Only(ctx)
	return OrderFromEntity(o), e
}
func (s *OrderStore) TransitionOrder(ctx context.Context, c payment.OrderTransition) (int, error) {
	u := s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(c.ID), paymentorder.StatusIn(c.From...)).SetStatus(c.Status)
	if c.Version != nil {
		u.Where(paymentorder.UpdatedAtEQ(*c.Version))
	}
	if c.ClearFailure {
		u.ClearFailedAt().ClearFailedReason()
	}
	if c.TradeNo != "" {
		u.SetPaymentTradeNo(c.TradeNo)
	}
	if c.PayAmount != nil {
		u.SetPayAmount(*c.PayAmount)
	}
	if c.PaidAt != nil {
		u.SetPaidAt(*c.PaidAt)
	}
	if c.CompletedAt != nil {
		u.SetCompletedAt(*c.CompletedAt)
	}
	if c.FailedAt != nil {
		u.SetFailedAt(*c.FailedAt)
	}
	if c.FailedReason != nil {
		u.SetFailedReason(*c.FailedReason)
	}
	if c.InvoiceID != "" {
		u.SetPaymentInvoiceID(c.InvoiceID)
	}
	if c.InvoiceURL != "" {
		u.SetPaymentInvoiceURL(c.InvoiceURL)
	}
	if c.InvoicePDF != "" {
		u.SetPaymentInvoicePdfURL(c.InvoicePDF)
	}
	if c.InvoiceStatus != "" {
		u.SetPaymentInvoiceStatus(c.InvoiceStatus)
	}
	return u.Save(ctx)
}
func (s *OrderStore) ClaimFulfillment(ctx context.Context, id int64, now, stale time.Time) (int, error) {
	return s.client.PaymentOrder.Update().Where(paymentorder.IDEQ(id), paymentorder.Or(paymentorder.StatusIn(payment.OrderStatusPaid, payment.OrderStatusFailed), paymentorder.And(paymentorder.StatusEQ(payment.OrderStatusRecharging), paymentorder.UpdatedAtLTE(stale)))).SetStatus(payment.OrderStatusRecharging).SetUpdatedAt(now).ClearFailedAt().ClearFailedReason().Save(ctx)
}
func (s *OrderStore) HasAudit(ctx context.Context, id int64, action string) bool {
	count, _ := s.client.PaymentAuditLog.Query().Where(paymentauditlog.OrderIDEQ(strconv.FormatInt(id, 10)), paymentauditlog.ActionEQ(action)).Limit(1).Count(ctx)
	return count > 0
}
