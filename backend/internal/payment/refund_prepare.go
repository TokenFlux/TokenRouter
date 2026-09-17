// 退款权限、强制提示和原权益计划计算；实际扣减在存储闭合事务中复核。
package payment

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

func (s *RefundWorkflow) RequestRefund(ctx context.Context, oid, uid int64, reason string) error {
	o, err := s.ValidateRefundRequest(ctx, oid, uid)
	if err != nil {
		return err
	}
	u, err := s.runtime.User(ctx, o.UserID)
	if err != nil {
		return fmt.Errorf("get user: %w", err)
	}
	if u.Balance < o.Amount {
		return infraerrors.BadRequest("BALANCE_NOT_ENOUGH", "refund amount exceeds balance")
	}
	nr := strings.TrimSpace(reason)
	now := s.runtime.Now()
	by := fmt.Sprintf("%d", uid)
	c, err := s.store.RequestRefund(ctx, oid, uid, o.Amount, nr, now, by)
	if err != nil {
		return fmt.Errorf("update: %w", err)
	}
	if c == 0 {
		return infraerrors.Conflict("CONFLICT", "order status changed")
	}
	s.runtime.Audit(ctx, oid, "REFUND_REQUESTED", fmt.Sprintf("user:%d", uid), map[string]any{"amount": o.Amount, "reason": nr})
	return nil
}
func (s *RefundWorkflow) ValidateRefundRequest(ctx context.Context, oid, uid int64) (*Order, error) {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != uid {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission")
	}
	if o.OrderType != OrderTypeBalance {
		return nil, infraerrors.BadRequest("INVALID_ORDER_TYPE", "only balance orders can request refund")
	}
	if o.Status != OrderStatusCompleted {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "only completed orders can request refund")
	}
	// Check provider instance allows user refund
	inst, err := s.runtime.Instance(ctx, o)
	if err != nil || inst == nil {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.AllowUserRefund {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "user refund is not enabled for this provider")
	}
	return o, nil
}
func (s *RefundWorkflow) PrepareRefund(ctx context.Context, oid int64, amt float64, reason string, force, deduct bool) (*RefundPlan, *RefundResult, error) {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return nil, nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	ok := []string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundPending, OrderStatusRefundFailed}
	if !slices.Contains(ok, o.Status) {
		return nil, nil, infraerrors.BadRequest("INVALID_STATUS", "order status does not allow refund")
	}
	// Check provider instance allows admin refund
	inst, instErr := s.runtime.Instance(ctx, o)
	if instErr != nil {
		s.runtime.Warn("refund: provider instance lookup failed", "orderID", oid, "error", instErr)
		return nil, nil, infraerrors.InternalServer("PROVIDER_LOOKUP_FAILED", "failed to look up payment provider for this order")
	}
	if inst == nil {
		// Legacy order without provider_instance_id — block refund
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.RefundEnabled {
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not enabled for this provider")
	}
	if math.IsNaN(amt) || math.IsInf(amt, 0) {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid refund amount")
	}
	if amt <= 0 {
		amt = o.Amount
	}
	orderCurrency := PaymentOrderCurrency(o)
	if amt-o.Amount > PaymentAmountToleranceForCurrency(orderCurrency) {
		return nil, nil, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds recharge")
	}
	ga := CalculateGatewayRefundAmount(o.Amount, o.PayAmount, amt, orderCurrency)
	rr := strings.TrimSpace(reason)
	if rr == "" && o.RefundRequestReason != nil {
		rr = *o.RefundRequestReason
	}
	if rr == "" {
		rr = fmt.Sprintf("refund order:%d", o.ID)
	}
	p := &RefundPlan{
		OrderID:       oid,
		Order:         o,
		RefundAmount:  amt,
		GatewayAmount: ga,
		Reason:        rr,
		Force:         force,
		DeductBalance: deduct,
		DeductionType: DeductionTypeNone,
	}
	if deduct {
		if er := s.PrepDeduct(ctx, o, p, force); er != nil {
			return nil, er, nil
		}
	}
	return p, nil, nil
}
func (s *RefundWorkflow) PrepDeduct(ctx context.Context, o *Order, p *RefundPlan, force bool) *RefundResult {
	if o.OrderType == OrderTypeSubscription {
		p.DeductionType = DeductionTypeSubscription
		p.SubDaysToDeduct = PaymentOrderSubscriptionValidityDays(o)
		subs, err := s.runtime.Subscriptions(ctx, o.ID)
		if err == nil {
			for i := range subs {
				if subs[i].Status == billing.SubscriptionStatusActive || subs[i].Status == billing.SubscriptionStatusPending {
					p.SubscriptionID = subs[i].ID
					break
				}
			}
		}
		if p.SubscriptionID == 0 && !force {
			return &RefundResult{Success: false, Warning: "cannot find active subscription for deduction, use force", RequireForce: true}
		}
		return nil
	}
	u, err := s.runtime.User(ctx, o.UserID)
	if err != nil {
		if !force {
			return &RefundResult{Success: false, Warning: "cannot fetch user balance, use force", RequireForce: true}
		}
		return nil
	}
	p.DeductionType = DeductionTypeBalance
	// 非强制退款必须先确认余额足以完成权益回收，避免无提示地少扣余额。
	if u.Balance < p.RefundAmount && !force {
		return &RefundResult{Success: false, Warning: "user balance is insufficient for deduction, use force", RequireForce: true}
	}
	p.BalanceToDeduct = math.Max(0, math.Min(p.RefundAmount, u.Balance))
	return nil
}
