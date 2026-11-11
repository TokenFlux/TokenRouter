package payment

import (
	"context"
	"fmt"
	"math"
	"slices"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// refund_prepare.go validates refund eligibility and plans local entitlement
// recovery separately from gateway repayment.
// Refund requests and administrative execution have different eligibility
// checks: user requests need an owned completed balance order and
// AllowUserRefund, while execution planning reads RefundEnabled and additional
// retryable states. Planning reads wallet data or records subscription hold
// intent before the store attempts its conditional transaction claim.
//
// Requested local reversal quantity is separate from gateway money, which is
// derived from stored order amounts with currency precision. Missing or
// insufficient entitlement observations can return a force-decision result
// alongside nil error, so callers inspect that result before executing. Wallet
// planning chooses a bounded amount, while subscription planning retains
// the entitlement choice until the transactional grant lookup. These
// observations prepare intent; the later transaction supplies actual wallet
// deduction and persistent recovery evidence.

// RequestRefund records the owned completed balance order as refund-requested
// after eligibility and a current balance read. Its conditional write can
// return conflict if status changes; it deducts no rights and publishes
// ordinary request audit only after the state write succeeds.
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
	// Claim the still-owned completed balance state before publishing the
	// ordinary request audit. The earlier balance read is eligibility
	// information, not a wallet reservation in this update.
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

// ValidateRefundRequest returns an owned completed balance order whose
// historical instance permits user refund. Order-read errors become not-found,
// and unavailable instance resolution becomes user-refund-disabled; this check
// uses AllowUserRefund rather than independently checking RefundEnabled.
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
	inst, err := s.runtime.Instance(ctx, o)
	if err != nil || inst == nil {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.AllowUserRefund {
		return nil, infraerrors.Forbidden("USER_REFUND_DISABLED", "user refund is not enabled for this provider")
	}
	return o, nil
}

// PrepareRefund returns a plan, an early force-decision result, or an error
// before any refund transaction or gateway request. It accepts completed,
// requested, or failed refund states and requires a resolved instance
// with administrative refund enabled.
//
// Zero selects the order amount. Balance orders also retain the older negative
// amount default and compute a proportional gateway refund. Subscription
// orders reject negative amounts and refund the full stored charge. Reason
// text prefers the supplied trimmed reason, recorded request reason, then an
// order description. When deduction is requested, PrepDeduct can return a
// warning result with nil error; callers inspect that result before
// ExecuteRefund.
func (s *RefundWorkflow) PrepareRefund(ctx context.Context, oid int64, amt float64, reason string, force, deduct bool) (*RefundPlan, *RefundResult, error) {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return nil, nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusRefundPending || o.Status == OrderStatusRefunding {
		return nil, nil, RefundRecoveryRequired("a refund already awaits confirmation")
	}
	ok := []string{OrderStatusCompleted, OrderStatusRefundRequested, OrderStatusRefundFailed}
	if !slices.Contains(ok, o.Status) {
		return nil, nil, infraerrors.BadRequest("INVALID_STATUS", "order status does not allow refund")
	}
	inst, instErr := s.runtime.Instance(ctx, o)
	if instErr != nil {
		s.runtime.Warn("refund: provider instance lookup failed", "orderID", oid, "error", instErr)
		return nil, nil, infraerrors.InternalServer("PROVIDER_LOOKUP_FAILED", "failed to look up payment provider for this order")
	}
	if inst == nil {
		// Historical merchant resolution failed to identify an instance. Keep the
		// request disabled because refund submission cannot select another merchant.
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not available for this order")
	}
	if !inst.RefundEnabled {
		return nil, nil, infraerrors.Forbidden("REFUND_DISABLED", "refund is not enabled for this provider")
	}
	if math.IsNaN(amt) || math.IsInf(amt, 0) || (o.OrderType == OrderTypeSubscription && amt < 0) {
		return nil, nil, infraerrors.BadRequest("INVALID_AMOUNT", "invalid refund amount")
	}
	if amt <= 0 {
		amt = o.Amount
	}
	orderCurrency := PaymentOrderCurrency(o)
	if o.OrderType == OrderTypeSubscription {
		// An externally paid subscription cannot be refunded locally without
		// a channel transaction to submit and verify.
		if strings.TrimSpace(o.PaymentTradeNo) == "" {
			return nil, nil, infraerrors.BadRequest("REFUND_TRADE_REFERENCE_MISSING", "subscription order is missing its payment reference")
		}
		currency := PaymentOrderAmountCurrency(o)
		// Compare in order-price units, then use the entire actual charge in its
		// gateway currency so fees and cross-currency pricing are refunded fully.
		if FormatAmountForCurrency(amt, currency) != FormatAmountForCurrency(o.Amount, currency) {
			return nil, nil, infraerrors.BadRequest("SUBSCRIPTION_REFUND_FULL_ONLY", "subscription orders require a full refund")
		}
		amt = o.Amount
	}
	if amt-o.Amount > PaymentAmountToleranceForCurrency(orderCurrency) {
		return nil, nil, infraerrors.BadRequest("REFUND_AMOUNT_EXCEEDED", "refund amount exceeds recharge")
	}
	ga := o.PayAmount
	if o.OrderType != OrderTypeSubscription {
		ga = CalculateGatewayRefundAmount(o.Amount, o.PayAmount, amt, orderCurrency)
	}
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

// PrepDeduct records the subscription hold intent or plans a wallet deduction
// from current funds. The hold target is resolved in the preparation
// transaction so a later renewal or revocation cannot substitute another grant.
//
// Wallet lookup failure or insufficient funds requires explicit force consent.
// With force, a usable balance is capped at its current nonnegative amount,
// while a failed read leaves the existing deduction fields unchanged.
func (s *RefundWorkflow) PrepDeduct(ctx context.Context, o *Order, p *RefundPlan, force bool) *RefundResult {
	if o.OrderType == OrderTypeSubscription {
		p.DeductionType = DeductionTypeSubscription
		p.SubscriptionHold = true
		return nil
	}
	u, err := s.runtime.User(ctx, o.UserID)
	if err != nil {
		if !force {
			return &RefundResult{Status: RefundResultRequiresForce, Warning: "cannot fetch user balance, use force", RequireForce: true}
		}
		// Forced planning proceeds after this failed balance observation with its
		// existing deduction fields. Preparation receipt validation still checks the
		// resulting intent before any gateway call.
		return nil
	}
	p.DeductionType = DeductionTypeBalance
	// A non-forced attempt needs the observed full wallet quantity. Return the
	// force-decision warning before selecting a partial deduction from current
	// funds.
	if u.Balance < p.RefundAmount && !force {
		return &RefundResult{Status: RefundResultRequiresForce, Warning: "user balance is insufficient for deduction, use force", RequireForce: true}
	}
	p.BalanceToDeduct = math.Max(0, math.Min(p.RefundAmount, u.Balance))
	return nil
}
