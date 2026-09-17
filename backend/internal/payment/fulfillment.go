// 支付状态推进、权益发放和通知保持原多阶段边界。
package payment

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// ErrOrderNotFound 表示支付回调引用的 out_trade_no 在本地订单表中不存在。
// Webhook 处理器会把它当成终态错误处理，返回 2xx 避免支付平台持续重试。
var ErrOrderNotFound = errors.New("payment order not found")

const FulfillmentLeaseDuration = 5 * time.Minute

func (s *Fulfillment) HandlePaymentNotification(ctx context.Context, n *PaymentNotification, pk string) error {
	if n == nil {
		return nil
	}
	// 本次只让 Stripe 的异步失败事件改变订单状态，其他渠道维持原有忽略语义。
	if n.Status == ProviderStatusFailed && GetBasePaymentType(pk) != TypeStripe {
		return nil
	}
	switch n.Status {
	case NotificationStatusSuccess, NotificationStatusPaid, ProviderStatusProcessing, ProviderStatusFailed:
	default:
		return nil
	}

	order, err := s.FindPaymentNotificationOrder(ctx, n.OrderID)
	if err != nil {
		return err
	}
	if err := s.ValidatePaymentNotificationOrder(ctx, order, pk, n.TradeNo, n.Metadata); err != nil {
		return err
	}

	switch n.Status {
	case NotificationStatusSuccess, NotificationStatusPaid:
		return s.ConfirmPayment(ctx, order, n.TradeNo, n.Amount, pk, n.Metadata)
	case ProviderStatusProcessing:
		return s.MarkPaymentProcessing(ctx, order, n.TradeNo, pk, n.Metadata)
	case ProviderStatusFailed:
		return s.MarkPaymentFailed(ctx, order, n.TradeNo, pk)
	default:
		return nil
	}
}
func (s *Fulfillment) FindPaymentNotificationOrder(ctx context.Context, orderID string) (*Order, error) {
	// 优先按发送给渠道的外部订单号查询，旧版 sub2_N 载荷仅在确实未命中时回退。
	order, err := s.store.OrderByTradeNumber(ctx, orderID)
	if err == nil {
		return order, nil
	}
	if !s.store.IsNotFound(err) {
		return nil, fmt.Errorf("lookup order failed for out_trade_no %s: %w", orderID, err)
	}
	if oid, ok := ParseLegacyPaymentOrderID(orderID, s.store.IsNotFound(err)); ok {
		order, getErr := s.store.Order(ctx, oid)
		if getErr == nil {
			return order, nil
		}
		if !s.store.IsNotFound(getErr) {
			return nil, fmt.Errorf("lookup legacy payment order %d: %w", oid, getErr)
		}
	}
	return nil, fmt.Errorf("%w: out_trade_no=%s", ErrOrderNotFound, orderID)
}
func ParseLegacyPaymentOrderID(orderID string, notFound bool) (int64, bool) {
	if !notFound {
		return 0, false
	}
	orderID = strings.TrimSpace(orderID)
	if !strings.HasPrefix(orderID, OrderIDPrefix) {
		return 0, false
	}
	trimmed := strings.TrimPrefix(orderID, OrderIDPrefix)
	if trimmed == "" || trimmed == orderID {
		return 0, false
	}
	oid, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || oid <= 0 {
		return 0, false
	}
	return oid, true
}
func (s *Fulfillment) ValidatePaymentNotificationOrder(ctx context.Context, o *Order, pk, tradeNo string, metadata map[string]string) error {
	instanceProviderKey := ""
	if inst, instErr := s.bindings.GetOrderProviderInstance(ctx, o); instErr == nil && inst != nil {
		instanceProviderKey = inst.ProviderKey
	}
	expectedProviderKey := ExpectedNotificationProviderKeyForOrder(s.registry, o, instanceProviderKey)
	if expectedProviderKey != "" && strings.TrimSpace(pk) != "" && !strings.EqualFold(expectedProviderKey, strings.TrimSpace(pk)) {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_PROVIDER_MISMATCH", pk, map[string]any{
			"expectedProvider": expectedProviderKey,
			"actualProvider":   pk,
			"tradeNo":          tradeNo,
		})
		return fmt.Errorf("provider mismatch: expected %s, got %s", expectedProviderKey, pk)
	}
	if err := ValidateProviderNotificationMetadata(o, pk, metadata); err != nil {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_PROVIDER_METADATA_MISMATCH", pk, map[string]any{
			"detail":  err.Error(),
			"tradeNo": tradeNo,
		})
		return err
	}
	return nil
}
func (s *Fulfillment) ConfirmPayment(ctx context.Context, o *Order, tradeNo string, paid float64, pk string, metadata map[string]string) error {
	if !IsValidProviderAmount(paid) {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", pk, map[string]any{
			"expected": o.PayAmount,
			"paid":     paid,
			"tradeNo":  tradeNo,
		})
		return fmt.Errorf("invalid paid amount from provider: %v", paid)
	}
	if math.Abs(paid-o.PayAmount) > PaymentAmountToleranceForCurrency(PaymentOrderCurrency(o)) {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_AMOUNT_MISMATCH", pk, map[string]any{"expected": o.PayAmount, "paid": paid, "tradeNo": tradeNo})
		return fmt.Errorf("amount mismatch: expected %s, got %s", strconv.FormatFloat(o.PayAmount, 'f', -1, 64), strconv.FormatFloat(paid, 'f', -1, 64))
	}
	return s.ToPaid(ctx, o, tradeNo, paid, pk, metadata)
}
func (s *Fulfillment) MarkPaymentProcessing(ctx context.Context, o *Order, tradeNo, pk string, metadata map[string]string) error {
	if o.Status != OrderStatusPending {
		return nil
	}

	processingTradeNo := strings.TrimSpace(tradeNo)
	if GetBasePaymentType(pk) == TypeStripe && metadata != nil {
		if sessionID := strings.TrimSpace(metadata["checkout_session_id"]); strings.HasPrefix(sessionID, "cs_") {
			processingTradeNo = sessionID
		}
	}
	change := OrderTransition{
		ID:           o.ID,
		From:         []string{OrderStatusPending},
		Status:       OrderStatusProcessing,
		ClearFailure: true,
		TradeNo:      processingTradeNo,
	}
	ApplyInvoiceMetadata(&change, metadata)
	updated, err := s.store.TransitionOrder(ctx, change)
	if err != nil {
		return fmt.Errorf("update to PROCESSING: %w", err)
	}
	if updated > 0 {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_PROCESSING", pk, map[string]any{
			"previous_status": o.Status,
			"tradeNo":         processingTradeNo,
		})
	}
	return nil
}
func (s *Fulfillment) MarkPaymentFailed(ctx context.Context, o *Order, tradeNo, pk string) error {
	for attempts := 0; attempts < 3; attempts++ {
		previousStatus := o.Status
		if previousStatus != OrderStatusPending && previousStatus != OrderStatusProcessing {
			return nil
		}
		now := s.runtime.Now()
		reason := "payment provider reported failure"
		updated, err := s.store.TransitionOrder(ctx, OrderTransition{
			ID:           o.ID,
			From:         []string{previousStatus},
			Status:       OrderStatusExpired,
			FailedAt:     &now,
			FailedReason: &reason,
			TradeNo:      strings.TrimSpace(tradeNo),
		})
		if err != nil {
			return fmt.Errorf("update payment failure: %w", err)
		}
		if updated > 0 {
			s.runtime.Audit(ctx, o.ID, "PAYMENT_FAILED", pk, map[string]any{
				"previous_status": previousStatus,
				"tradeNo":         tradeNo,
			})
			return nil
		}
		o, err = s.store.Order(ctx, o.ID)
		if err != nil {
			return fmt.Errorf("reload payment order after failure race: %w", err)
		}
	}
	return fmt.Errorf("payment order %d status kept changing while recording failure", o.ID)
}
func IsValidProviderAmount(amount float64) bool {
	return amount > 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}
func ValidateProviderNotificationMetadata(order *Order, providerKey string, metadata map[string]string) error {
	return ValidateProviderSnapshotMetadata(order, providerKey, metadata)
}
func ExpectedNotificationProviderKey(registry *Registry, orderPaymentType string, orderProviderKey string, instanceProviderKey string) string {
	if key := strings.TrimSpace(instanceProviderKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(orderProviderKey); key != "" {
		return key
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(PaymentType(orderPaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(orderPaymentType)
}
func (s *Fulfillment) ToPaid(ctx context.Context, o *Order, tradeNo string, paid float64, pk string, metadata map[string]string) error {
	if GetBasePaymentType(pk) == TypeStripe &&
		strings.HasPrefix(strings.TrimSpace(tradeNo), "in_") &&
		strings.HasPrefix(strings.TrimSpace(o.PaymentTradeNo), "pi_") {
		tradeNo = o.PaymentTradeNo
	}
	for attempts := 0; attempts < 5; attempts++ {
		previousStatus := o.Status
		switch previousStatus {
		case OrderStatusPending, OrderStatusProcessing, OrderStatusExpired, OrderStatusCancelled:
			now := s.runtime.Now()
			change := OrderTransition{
				ID:           o.ID,
				From:         []string{previousStatus},
				Status:       OrderStatusPaid,
				PayAmount:    &paid,
				PaidAt:       &now,
				ClearFailure: true,
				TradeNo:      strings.TrimSpace(tradeNo),
			}
			ApplyInvoiceMetadata(&change, metadata)
			updated, err := s.store.TransitionOrder(ctx, change)
			if err != nil {
				return fmt.Errorf("update to PAID: %w", err)
			}
			if updated == 0 {
				o, err = s.store.Order(ctx, o.ID)
				if err != nil {
					return fmt.Errorf("reload payment order after success race: %w", err)
				}
				continue
			}
			if previousStatus != OrderStatusPending {
				s.runtime.Log("info", "order recovered from payment success",
					"orderID", o.ID,
					"previousStatus", previousStatus,
					"tradeNo", tradeNo,
					"provider", pk,
				)
				s.runtime.Audit(ctx, o.ID, "ORDER_RECOVERED", pk, map[string]any{
					"previous_status": previousStatus,
					"tradeNo":         tradeNo,
					"paidAmount":      paid,
					"reason":          "payment success recovered order from " + previousStatus,
				})
			}
			s.runtime.Audit(ctx, o.ID, "ORDER_PAID", pk, map[string]any{"tradeNo": tradeNo, "paidAmount": paid})
			return s.ExecuteFulfillment(ctx, o.ID)
		case OrderStatusFailed, OrderStatusPaid, OrderStatusRecharging:
			return s.ExecuteFulfillment(ctx, o.ID)
		case OrderStatusCompleted, OrderStatusRefunded:
			return nil
		default:
			return nil
		}
	}
	return fmt.Errorf("payment order %d status kept changing while recording success", o.ID)
}
func (s *Fulfillment) ExecuteFulfillment(ctx context.Context, oid int64) error {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return fmt.Errorf("get order: %w", err)
	}
	if o.OrderType == OrderTypeSubscription {
		return s.ExecuteSubscriptionFulfillment(ctx, oid)
	}
	return s.ExecuteBalanceFulfillment(ctx, oid)
}
func (s *Fulfillment) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if IsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	lease, err := s.AcquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.DoBalance(ctx, o, lease); err != nil {
		s.MarkFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}
func (s *Fulfillment) AcquirePaymentFulfillmentLease(ctx context.Context, o *Order) (*FulfillmentLease, error) {
	if o == nil {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}

	now := s.runtime.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-FulfillmentLeaseDuration)
	updated, err := s.store.ClaimFulfillment(ctx, o.ID, now, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("acquire fulfillment lease: %w", err)
	}
	if updated == 0 {
		current, getErr := s.store.Order(ctx, o.ID)
		if getErr != nil {
			return nil, fmt.Errorf("reload fulfillment lease: %w", getErr)
		}
		if current.Status == OrderStatusCompleted {
			return nil, nil
		}
		if current.Status == OrderStatusRecharging {
			return nil, infraerrors.Conflict("CONFLICT", "order is being processed")
		}
		return nil, infraerrors.Conflict("CONFLICT", "order status changed while acquiring fulfillment lease")
	}

	// 重新读取数据库时间戳，避免依赖应用时钟精度。
	claimed, err := s.store.Order(ctx, o.ID)
	if err != nil {
		return nil, fmt.Errorf("reload acquired fulfillment lease: %w", err)
	}
	if claimed.Status != OrderStatusRecharging {
		return nil, infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	return &FulfillmentLease{Version: claimed.UpdatedAt}, nil
}

// RedeemAction represents the idempotency decision for balance fulfillment.
type RedeemAction int

const (
	// RedeemActionCreate: code does not exist — create it, then redeem.
	RedeemActionCreate RedeemAction = iota
	// RedeemActionRedeem: code exists but is unused — skip creation, redeem only.
	RedeemActionRedeem
	// RedeemActionSkipCompleted: code exists and is already used — skip to mark completed.
	RedeemActionSkipCompleted
)

// ResolveRedeemAction decides the idempotency action based on an existing redeem code lookup.
// existing is the result of GetByCode; lookupErr is the error from that call.
func ResolveRedeemAction(existing *billing.RedeemCode, lookupErr error) RedeemAction {
	if existing == nil || lookupErr != nil {
		return RedeemActionCreate
	}
	if existing.IsUsed() {
		return RedeemActionSkipCompleted
	}
	return RedeemActionRedeem
}
func (s *Fulfillment) DoBalance(ctx context.Context, o *Order, lease *FulfillmentLease) error {
	// Idempotency: check if redeem code already exists (from a previous partial run)
	existing, lookupErr := s.redeemService.GetByCode(ctx, o.RechargeCode)
	action := ResolveRedeemAction(existing, lookupErr)

	switch action {
	case RedeemActionSkipCompleted:
		if err := s.ApplyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		// Code already created and redeemed — just mark completed
		return s.MarkCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
	case RedeemActionCreate:
		rc := &billing.RedeemCode{Code: o.RechargeCode, Type: billing.RedeemTypeBalance, Value: o.Amount, Status: billing.StatusUnused}
		if err := s.redeemService.CreateCode(ctx, rc); err != nil {
			return fmt.Errorf("create redeem code: %w", err)
		}
	case RedeemActionRedeem:
		// Code exists but unused — skip creation, proceed to redeem
	}
	if _, err := s.redeemService.Redeem(billing.ContextSkipRedeemAffiliate(ctx), o.UserID, o.RechargeCode); err != nil {
		return fmt.Errorf("redeem balance: %w", err)
	}
	if err := s.ApplyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.MarkCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
}
func (s *Fulfillment) MarkCompleted(ctx context.Context, o *Order, lease *FulfillmentLease, auditAction string) error {
	if lease == nil {
		return errors.New("missing payment fulfillment lease")
	}
	now := s.runtime.Now()
	updated, err := s.store.TransitionOrder(ctx, OrderTransition{
		ID:          o.ID,
		From:        []string{OrderStatusRecharging},
		Version:     &lease.Version,
		Status:      OrderStatusCompleted,
		CompletedAt: &now,
	})
	if err != nil {
		return fmt.Errorf("mark completed: %w", err)
	}
	if updated == 0 {
		current, getErr := s.store.Order(ctx, o.ID)
		if getErr == nil && current.Status == OrderStatusCompleted {
			return nil
		}
		return infraerrors.Conflict("CONFLICT", "fulfillment lease was lost before completion")
	}
	if !s.HasAuditLog(ctx, o.ID, auditAction) {
		s.runtime.Audit(ctx, o.ID, auditAction, "system", map[string]any{
			"rechargeCode":   o.RechargeCode,
			"creditedAmount": o.Amount,
			"payAmount":      o.PayAmount,
		})
		s.DispatchPaymentFulfillmentNotification(o, auditAction)
	}
	return nil
}
func (s *Fulfillment) DispatchPaymentFulfillmentNotification(o *Order, auditAction string) {
	if s == nil || s.runtime.Notify == nil || o == nil {
		return
	}
	o = o.Clone()
	s.runtime.Background("service/payment_fulfillment.go:dispatchPaymentFulfillmentNotification", func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		var err error
		switch auditAction {
		case "RECHARGE_SUCCESS":
			err = s.SendBalanceRechargeSuccessNotification(ctx, o)
		case "SUBSCRIPTION_SUCCESS":
			err = s.SendSubscriptionPurchaseSuccessNotification(ctx, o)
		default:
			return
		}
		if err != nil {
			s.runtime.Log("warn", "payment fulfillment notification email failed", "order_id", o.ID, "action", auditAction, "err", err.Error())
		}
	})
}
func (s *Fulfillment) SendBalanceRechargeSuccessNotification(ctx context.Context, o *Order) error {
	currentBalance := ""
	if s.runtime.User != nil {
		if user, err := s.runtime.User(ctx, o.UserID); err == nil && user != nil {
			currentBalance = fmt.Sprintf("%.2f", user.Balance)
		}
	}
	return s.runtime.Notify(ctx, PaymentNotice{
		Event:          "balance.recharge_success",
		RecipientEmail: o.UserEmail,
		RecipientName:  FirstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables: map[string]string{
			"recharge_amount": fmt.Sprintf("%.2f", o.Amount),
			"current_balance": currentBalance,
			"order_id":        strconv.FormatInt(o.ID, 10),
		},
	})
}
func (s *Fulfillment) SendSubscriptionPurchaseSuccessNotification(ctx context.Context, o *Order) error {
	subscriptionGroup := FirstNonEmpty(o.PlanSnapshot.Name, "Subscription")
	subscriptionDays := ""
	if o.PlanSnapshot.ValidityDays > 0 {
		subscriptionDays = strconv.Itoa(o.PlanSnapshot.ValidityDays)
	}
	variables := map[string]string{
		"subscription_group": subscriptionGroup,
		"subscription_days":  subscriptionDays,
		"expiry_time":        "",
		"order_id":           strconv.FormatInt(o.ID, 10),
	}
	if o.PlanID != nil {
		if s.subscriptionSvc != nil {
			if sub, err := s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.PlanID); err == nil && sub != nil {
				variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
			}
		}
	}
	return s.runtime.Notify(ctx, PaymentNotice{
		Event:          "subscription.purchase_success",
		RecipientEmail: o.UserEmail,
		RecipientName:  FirstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables:      variables,
	})
}
func (s *Fulfillment) ExecuteSubscriptionFulfillment(ctx context.Context, oid int64) error {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil
	}
	if IsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	if o.PlanID == nil || *o.PlanID <= 0 {
		return infraerrors.BadRequest("INVALID_STATUS", "missing subscription info")
	}
	lease, err := s.AcquirePaymentFulfillmentLease(ctx, o)
	if err != nil {
		return err
	}
	if lease == nil {
		return nil
	}
	if err := s.DoSub(ctx, o, lease); err != nil {
		s.MarkFailed(ctx, oid, lease, err)
		return err
	}
	return nil
}
func (s *Fulfillment) DoSub(ctx context.Context, o *Order, lease *FulfillmentLease) error {
	if o.PlanID == nil || *o.PlanID <= 0 {
		return fmt.Errorf("order %d missing plan id", o.ID)
	}
	if s.subscriptionSvc == nil {
		return errors.New("subscription service is unavailable")
	}
	assigned := s.HasAuditLog(ctx, o.ID, "SUBSCRIPTION_ASSIGNED") || s.HasAuditLog(ctx, o.ID, "SUBSCRIPTION_SUCCESS")
	if !assigned {
		orderNote := fmt.Sprintf("payment order %d", o.ID)
		input := &billing.AssignSubscriptionInput{
			UserID:        o.UserID,
			PlanID:        *o.PlanID,
			AssignedBy:    0,
			Notes:         orderNote,
			SourceOrderID: &o.ID,
		}
		if snapshot := o.PlanSnapshot; snapshot.ValidityDays > 0 || snapshot.DailyLimitUSD != nil || snapshot.WeeklyLimitUSD != nil || snapshot.MonthlyLimitUSD != nil {
			input.ValidityDays = snapshot.ValidityDays
			input.DailyLimitUSD = snapshot.DailyLimitUSD
			input.WeeklyLimitUSD = snapshot.WeeklyLimitUSD
			input.MonthlyLimitUSD = snapshot.MonthlyLimitUSD
			input.UseProvidedTemplate = true
		}
		_, _, err := s.subscriptionSvc.AssignOrExtendSubscription(ctx, input)
		if err != nil {
			return fmt.Errorf("assign subscription: %w", err)
		}
		s.runtime.Audit(ctx, o.ID, "SUBSCRIPTION_ASSIGNED", "system", map[string]any{
			"planID": *o.PlanID,
		})
	} else {
		s.runtime.Log("info", "subscription already assigned for order, skipping", "orderID", o.ID, "planID", *o.PlanID)
	}
	if err := s.ApplyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.MarkCompleted(ctx, o, lease, "SUBSCRIPTION_SUCCESS")
}
func (s *Fulfillment) HasAuditLog(ctx context.Context, id int64, action string) bool {
	return s.store.HasAudit(ctx, id, action)
}
func (s *Fulfillment) ApplyAffiliateRebateForOrder(ctx context.Context, o *Order) error {
	base := AffiliateRebateBasePoints(o)
	if o == nil || base <= 0 || s.runtime.RebateEnabled == nil || !s.runtime.RebateEnabled(ctx) {
		return nil
	}
	return s.store.ApplyOrderRebate(ctx, o, base)
}

// AffiliateRebateBasePoints 只返回订单实际购买的推理积分，绝不使用支付金额兜底。
func AffiliateRebateBasePoints(o *Order) float64 {
	points, ok := OrderPurchasedReasoningPoints(o)
	if !ok {
		return 0
	}
	return points
}
func (s *Fulfillment) MarkFailed(ctx context.Context, oid int64, lease *FulfillmentLease, cause error) {
	if lease == nil {
		s.runtime.Log("error", "mark FAILED without fulfillment lease", "orderID", oid)
		return
	}
	now := s.runtime.Now()
	r := OrderErrorMessage(cause)
	// 租约版本可阻止旧工作协程覆盖新的履约所有者。
	c, e := s.store.TransitionOrder(ctx, OrderTransition{
		ID:           oid,
		From:         []string{OrderStatusRecharging},
		Version:      &lease.Version,
		Status:       OrderStatusFailed,
		FailedAt:     &now,
		FailedReason: &r,
	})
	if e != nil {
		s.runtime.Log("error", "mark FAILED", "orderID", oid, "error", e)
	}
	if c > 0 {
		s.runtime.Audit(ctx, oid, "FULFILLMENT_FAILED", "system", map[string]any{"reason": r})
	}
}
func (s *Fulfillment) RetryFulfillment(ctx context.Context, oid int64) error {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		return infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.PaidAt == nil {
		return infraerrors.BadRequest("INVALID_STATUS", "order is not paid")
	}
	if IsRefundStatus(o.Status) {
		return infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot retry")
	}
	if o.Status == OrderStatusCompleted {
		return infraerrors.BadRequest("INVALID_STATUS", "order already completed")
	}
	if o.Status != OrderStatusFailed && o.Status != OrderStatusPaid && o.Status != OrderStatusRecharging {
		return infraerrors.BadRequest("INVALID_STATUS", "only paid, failed, and recoverable recharging orders can retry")
	}
	s.runtime.Audit(ctx, oid, "RECHARGE_RETRY", "admin", map[string]any{"detail": "admin manual retry"})
	return s.ExecuteFulfillment(ctx, oid)
}
