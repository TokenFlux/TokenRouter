// 取消、主动查单与后台对账保持原状态、预算及分页游标语义。
package payment

import (
	"context"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror" // Cancel rate limit configuration constants.
)

const (
	LifecycleRateLimitUnitDay             = "day"
	LifecycleRateLimitUnitMinute          = "minute"
	LifecycleRateLimitUnitHour            = "hour"
	LifecycleRateLimitModeFixed           = "fixed"
	LifecycleCheckPaidResultAlreadyPaid   = "already_paid"
	LifecycleCheckPaidResultCancelled     = "cancelled"
	LifecycleCheckPaidResultProcessing    = "processing"
	LifecycleCheckPaidResultFailed        = "failed"
	LifecycleCheckPaidResultUncertain     = "uncertain"
	LifecyclePendingPaymentReconcileLimit = 20
	LifecycleProcessingReconcileLimit     = 20
	LifecycleFulfillmentReconcileLimit    = 20
	LifecycleFulfillmentRetryDelay        = time.Minute
	LifecycleProcessingStaleAfter         = 24 * time.Hour
	LifecyclePaymentExpiryRetryDelay      = 15 * time.Minute
)

func (s *OrderLifecycle) CancelOrder(ctx context.Context, orderID, userID int64) (string, error) {
	o, err := s.store.Order(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return "", infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.CancelCore(ctx, o, OrderStatusCancelled, fmt.Sprintf("user:%d", userID), "user cancelled order")
}
func (s *OrderLifecycle) AdminCancelOrder(ctx context.Context, orderID int64) (string, error) {
	o, err := s.store.Order(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.CancelCore(ctx, o, OrderStatusCancelled, "admin", "admin cancelled order")
}

// @project-doc docs/domains/payments_and_entitlements.md#forced_expiration_recovery
// ForceExpireOrder 由管理员显式确认后终结无法确认上游状态的待支付订单。
// 保留 EXPIRED 状态使验签通过的迟到付款仍能进入既有恢复和履约流程。
func (s *OrderLifecycle) ForceExpireOrder(ctx context.Context, orderID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 500 {
		return infraerrors.BadRequest("INVALID_FORCE_EXPIRE_REASON", "force expiration reason is invalid")
	}

	return s.store.ForceExpire(ctx, orderID, reason)
}
func (s *OrderLifecycle) CancelCore(ctx context.Context, o *Order, fs, op, ad string) (string, error) {
	if o.PaymentTradeNo != "" || o.PaymentType != "" {
		prov, queryRef, resp, err := s.QueryPaymentOrderProvider(ctx, o)
		if err != nil {
			s.RecordPaymentCancelFailure(ctx, o, prov, queryRef, err, nil)
			return "", PaymentStatusUnavailableError(err)
		}
		outcome, err := s.ApplyQueriedPaymentStatus(ctx, o, prov, queryRef, resp)
		if err != nil {
			return outcome, err
		}
		switch outcome {
		case LifecycleCheckPaidResultAlreadyPaid:
			return outcome, nil
		case LifecycleCheckPaidResultProcessing:
			return s.ProcessingCancellationResult(fs)
		case LifecycleCheckPaidResultUncertain:
			err := fmt.Errorf("provider returned an invalid paid response")
			s.RecordPaymentCancelFailure(ctx, o, prov, queryRef, err, resp)
			return "", err
		case LifecycleCheckPaidResultFailed:
			return s.FinalizePendingOrder(ctx, o, fs, op, ad)
		}

		if cp, ok := prov.(CancelableProvider); ok {
			finishProviderCall := s.observe(ctx)
			cancelErr := cp.CancelPayment(ctx, queryRef)
			finishProviderCall()
			if cancelErr != nil {
				// 关闭请求可能与付款完成并发，必须二次查单后才能决定本地终态。
				retryResp, retryErr := s.QueryPaymentOrderWithProvider(ctx, prov, queryRef)
				if retryErr == nil {
					retryOutcome, applyErr := s.ApplyQueriedPaymentStatus(ctx, o, prov, queryRef, retryResp)
					if applyErr != nil {
						return retryOutcome, applyErr
					}
					switch retryOutcome {
					case LifecycleCheckPaidResultAlreadyPaid:
						return retryOutcome, nil
					case LifecycleCheckPaidResultProcessing:
						return s.ProcessingCancellationResult(fs)
					case LifecycleCheckPaidResultFailed:
						return s.FinalizePendingOrder(ctx, o, fs, op, ad)
					}
				}
				s.RecordPaymentCancelFailure(ctx, o, prov, queryRef, cancelErr, retryResp)
				if retryErr != nil {
					return "", PaymentStatusUnavailableError(fmt.Errorf("cancel upstream payment: %w; requery: %v", cancelErr, retryErr))
				}
				return "", PaymentStatusUnavailableError(fmt.Errorf("cancel upstream payment: %w", cancelErr))
			}
		}
	}
	return s.FinalizePendingOrder(ctx, o, fs, op, ad)
}
func (s *OrderLifecycle) ProcessingCancellationResult(finalStatus string) (string, error) {
	if finalStatus == OrderStatusExpired {
		return LifecycleCheckPaidResultProcessing, nil
	}
	return LifecycleCheckPaidResultProcessing, infraerrors.BadRequest("INVALID_STATUS", "payment is processing and cannot be cancelled")
}
func (s *OrderLifecycle) FinalizePendingOrder(ctx context.Context, o *Order, fs, op, ad string) (string, error) {
	c, err := s.store.TransitionOrder(ctx, OrderTransition{ID: o.ID, From: []string{OrderStatusPending}, Status: fs})
	if err != nil {
		return "", fmt.Errorf("update order status: %w", err)
	}
	if c > 0 {
		auditAction := "ORDER_CANCELLED"
		if fs == OrderStatusExpired {
			auditAction = "ORDER_EXPIRED"
		}
		s.runtime.Audit(ctx, o.ID, auditAction, op, map[string]any{"detail": ad})
	}
	if c > 0 {
		return LifecycleCheckPaidResultCancelled, nil
	}
	current, err := s.store.Order(ctx, o.ID)
	if err != nil {
		return "", fmt.Errorf("reload order after cancellation race: %w", err)
	}
	switch current.Status {
	case OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted:
		return LifecycleCheckPaidResultAlreadyPaid, nil
	case OrderStatusProcessing:
		return s.ProcessingCancellationResult(fs)
	case OrderStatusCancelled, OrderStatusExpired:
		return LifecycleCheckPaidResultCancelled, nil
	default:
		return "", fmt.Errorf("order status changed to %s while cancelling", current.Status)
	}
}
func (s *OrderLifecycle) RecordPaymentCancelFailure(ctx context.Context, o *Order, prov Provider, queryRef string, cancelErr error, resp *QueryOrderResponse) {
	if !s.HasAuditLog(ctx, o.ID, "PAYMENT_CANCEL_FAILED") {
		providerKey := "system"
		if prov != nil {
			providerKey = prov.ProviderKey()
		}
		detail := map[string]any{
			"queryRef": queryRef,
			"error":    OrderErrorMessage(cancelErr),
		}
		if resp != nil {
			detail["providerStatus"] = resp.Status
			detail["tradeNo"] = resp.TradeNo
		}
		s.runtime.Audit(ctx, o.ID, "PAYMENT_CANCEL_FAILED", providerKey, detail)
	}

	// 每次失败都刷新时间戳，以便超时任务按固定冷却窗口重试而不是每分钟重复请求。
	if err := s.store.TouchPending(ctx, o.ID, s.runtime.Now()); err != nil {
		s.runtime.Log("warn", "record payment cancellation retry time failed", "orderID", o.ID, "error", err)
	}
}
func PaymentStatusUnavailableError(cause error) error {
	return infraerrors.ServiceUnavailable("PAYMENT_STATUS_UNAVAILABLE", "payment status is temporarily unavailable").WithCause(cause)
}
func (s *OrderLifecycle) CheckPaid(ctx context.Context, o *Order) (string, error) {
	prov, queryRef, resp, err := s.QueryPaymentOrderProvider(ctx, o)
	if err != nil {
		s.runtime.Log("warn", "query upstream failed", "orderID", o.ID, "error", err)
		return "", nil
	}
	return s.ApplyQueriedPaymentStatus(ctx, o, prov, queryRef, resp)
}
func (s *OrderLifecycle) QueryPaymentOrderProvider(ctx context.Context, o *Order) (Provider, string, *QueryOrderResponse, error) {
	prov, err := s.bindings.GetOrderProvider(ctx, o)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolve order provider: %w", err)
	}
	queryRef := PaymentOrderQueryReference(o, prov)
	if queryRef == "" {
		return prov, "", nil, fmt.Errorf("payment order %d has no upstream query reference", o.ID)
	}
	resp, err := s.QueryPaymentOrderWithProvider(ctx, prov, queryRef)
	return prov, queryRef, resp, err
}
func (s *OrderLifecycle) QueryPaymentOrderWithProvider(ctx context.Context, prov Provider, queryRef string) (*QueryOrderResponse, error) {
	finishProviderCall := s.observe(ctx)
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	if err != nil {
		return nil, fmt.Errorf("query upstream payment %s: %w", queryRef, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("query upstream payment %s returned no response", queryRef)
	}
	return resp, nil
}
func (s *OrderLifecycle) ApplyQueriedPaymentStatus(ctx context.Context, o *Order, prov Provider, queryRef string, resp *QueryOrderResponse) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("missing provider query response")
	}
	if resp.Status == ProviderStatusPaid {
		if !IsValidProviderAmount(resp.Amount) {
			s.runtime.Audit(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", prov.ProviderKey(), map[string]any{
				"expected": o.PayAmount,
				"paid":     resp.Amount,
				"tradeNo":  resp.TradeNo,
				"queryRef": queryRef,
			})
			s.runtime.Log("warn", "query upstream returned invalid paid amount", "orderID", o.ID, "queryRef", queryRef, "paid", resp.Amount)
			retriedResp, retryOK := s.RequeryPaidOrderOnce(ctx, prov, queryRef)
			if !retryOK {
				return LifecycleCheckPaidResultUncertain, nil
			}
			resp = retriedResp
		}
		notificationTradeNo := o.PaymentTradeNo
		if upstreamTradeNo := strings.TrimSpace(resp.TradeNo); PaymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, notificationTradeNo) {
			if updateErr := s.store.SaveUpstreamTradeNumber(ctx, o.ID, upstreamTradeNo); updateErr != nil {
				return LifecycleCheckPaidResultAlreadyPaid, fmt.Errorf("persist upstream trade no during checkPaid: %w", updateErr)
			} else {
				o.PaymentTradeNo = upstreamTradeNo
			}
			notificationTradeNo = upstreamTradeNo
		}
		if err := s.HandlePaymentNotification(ctx, &PaymentNotification{
			TradeNo:  notificationTradeNo,
			OrderID:  o.OutTradeNo,
			Amount:   resp.Amount,
			Status:   ProviderStatusSuccess,
			Metadata: resp.Metadata,
		}, prov.ProviderKey()); err != nil {
			return LifecycleCheckPaidResultAlreadyPaid, err
		}
		return LifecycleCheckPaidResultAlreadyPaid, nil
	}
	if resp.Status == ProviderStatusProcessing {
		tradeNo := strings.TrimSpace(resp.TradeNo)
		if tradeNo == "" {
			tradeNo = strings.TrimSpace(o.PaymentTradeNo)
		}
		err := s.HandlePaymentNotification(ctx, &PaymentNotification{
			TradeNo:  tradeNo,
			OrderID:  o.OutTradeNo,
			Amount:   resp.Amount,
			Status:   ProviderStatusProcessing,
			Metadata: resp.Metadata,
		}, prov.ProviderKey())
		return LifecycleCheckPaidResultProcessing, err
	}
	if resp.Status == ProviderStatusFailed {
		return LifecycleCheckPaidResultFailed, nil
	}
	return "", nil
}
func (s *OrderLifecycle) RequeryPaidOrderOnce(ctx context.Context, prov Provider, queryRef string) (*QueryOrderResponse, bool) {
	if prov == nil || strings.TrimSpace(queryRef) == "" {
		return nil, false
	}
	finishProviderCall := s.observe(ctx)
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	if err != nil {
		s.runtime.Log("warn", "query upstream retry failed", "queryRef", queryRef, "error", err)
		return nil, false
	}
	if resp == nil || resp.Status != ProviderStatusPaid || !IsValidProviderAmount(resp.Amount) {
		return nil, false
	}
	return resp, true
}
func PaymentOrderQueryReference(order *Order, prov Provider) string {
	if order == nil {
		return ""
	}

	providerKey := ""
	if prov != nil {
		providerKey = strings.TrimSpace(prov.ProviderKey())
	}
	if providerKey == "" {
		if snapshot := PsOrderProviderSnapshot(order); snapshot != nil {
			providerKey = strings.TrimSpace(snapshot.ProviderKey)
		}
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(refundStringValue(order.ProviderKey))
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(order.PaymentType)
	}

	switch GetBasePaymentType(providerKey) {
	case TypeAlipay, TypeEasyPay, TypeWxpay:
		return strings.TrimSpace(order.OutTradeNo)
	case TypeStripe:
		if tradeNo := strings.TrimSpace(order.PaymentTradeNo); strings.HasPrefix(tradeNo, "cs_") {
			return tradeNo
		}
		if invoiceID := strings.TrimSpace(refundStringValue(order.PaymentInvoiceID)); invoiceID != "" {
			return invoiceID
		}
		if tradeNo := strings.TrimSpace(order.PaymentTradeNo); tradeNo != "" {
			return tradeNo
		}
		return strings.TrimSpace(order.OutTradeNo)
	default:
		if tradeNo := strings.TrimSpace(order.PaymentTradeNo); tradeNo != "" {
			return tradeNo
		}
		return strings.TrimSpace(order.OutTradeNo)
	}
}
func PaymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, currentTradeNo string) bool {
	upstreamTradeNo = strings.TrimSpace(upstreamTradeNo)
	if upstreamTradeNo == "" {
		return false
	}
	if strings.EqualFold(upstreamTradeNo, strings.TrimSpace(currentTradeNo)) {
		return false
	}
	if strings.EqualFold(upstreamTradeNo, strings.TrimSpace(queryRef)) {
		return false
	}
	return true
}

// VerifyOrderByOutTradeNo actively queries the upstream provider to check
// if a payment was made, and processes it if so. This handles the case where
// the provider's notify callback was missed (e.g. EasyPay popup mode).
func (s *OrderLifecycle) VerifyOrderByOutTradeNo(ctx context.Context, outTradeNo string, userID int64) (*Order, error) {
	outTradeNo, err := NormalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.store.OrderByTradeNumber(ctx, outTradeNo)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	// 待支付和已过期订单允许主动补查；处理中订单交给低频后台对账。
	if o.Status == OrderStatusPending || o.Status == OrderStatusExpired {
		result, checkErr := s.CheckPaid(ctx, o)
		if checkErr != nil {
			return nil, checkErr
		}
		if result == LifecycleCheckPaidResultAlreadyPaid || result == LifecycleCheckPaidResultProcessing {
			// 重新读取以返回原子状态转换后的结果。
			o, err = s.store.Order(ctx, o.ID)
			if err != nil {
				return nil, fmt.Errorf("reload order: %w", err)
			}
		}
	}
	return o, nil
}

// ReconcilePendingPaymentOrders 主动补偿未收到回调的支付宝和微信待支付订单，避免等到过期才发现已支付。
func (s *OrderLifecycle) ReconcilePendingPaymentOrders(ctx context.Context) (int, error) {
	now := s.runtime.Now()
	orders, err := s.store.PendingReconciliation(ctx, now, LifecyclePendingPaymentReconcileLimit)
	if err != nil {
		return 0, fmt.Errorf("query pending payment orders: %w", err)
	}

	recovered := 0
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		outcome, checkErr := s.CheckPaid(ctx, order)
		if checkErr != nil {
			s.runtime.Log("warn", "reconcile pending payment order failed", "orderID", order.ID, "error", checkErr)
			continue
		}
		if outcome == LifecycleCheckPaidResultAlreadyPaid {
			recovered++
		}
	}
	return recovered, nil
}

// VerifyOrderPublic returns the currently persisted public order state without
// triggering any upstream reconciliation. Signed resume-token recovery is the
// only public recovery path allowed to query upstream state.
func (s *OrderLifecycle) VerifyOrderPublic(ctx context.Context, outTradeNo string) (*Order, error) {
	outTradeNo, err := NormalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.store.OrderByTradeNumber(ctx, outTradeNo)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}
func NormalizeOrderLookupOutTradeNo(raw string) (string, error) {
	outTradeNo := strings.TrimSpace(raw)
	if outTradeNo == "" {
		return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is required")
	}
	if len(outTradeNo) > 64 {
		return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is invalid")
	}
	for _, ch := range outTradeNo {
		switch {
		case ch >= 'a' && ch <= 'z':
		case ch >= 'A' && ch <= 'Z':
		case ch >= '0' && ch <= '9':
		case ch == '_' || ch == '-':
		default:
			return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is invalid")
		}
	}
	return outTradeNo, nil
}
func (s *OrderLifecycle) ExpireTimedOutOrders(ctx context.Context) (int, error) {
	now := s.runtime.Now()
	return s.ExpireTimedOutOrdersAt(ctx, now)
}
func (s *OrderLifecycle) ExpireTimedOutOrdersAt(ctx context.Context, now time.Time) (int, error) {
	orders, err := s.store.ExpiredPending(ctx, now)
	if err != nil {
		return 0, fmt.Errorf("query expired: %w", err)
	}
	n := 0
	for _, o := range orders {
		if err := ctx.Err(); err != nil {
			return n, err
		}
		if !s.ShouldRetryTimedOutOrder(ctx, o, now) {
			continue
		}
		// 到期决策必须先确认上游状态并成功关闭待支付单据。
		outcome, cancelErr := s.CancelCore(ctx, o, OrderStatusExpired, "system", "order expired")
		if cancelErr != nil {
			s.runtime.Log("warn", "keep timed-out order pending after upstream cancellation failure", "orderID", o.ID, "error", cancelErr)
			continue
		}
		if outcome == LifecycleCheckPaidResultAlreadyPaid {
			s.runtime.Log("info", "order was paid during expiry", "orderID", o.ID)
			continue
		}
		if outcome == LifecycleCheckPaidResultCancelled {
			n++
		}
	}
	return n, nil
}
func (s *OrderLifecycle) ShouldRetryTimedOutOrder(ctx context.Context, order *Order, now time.Time) bool {
	if order == nil || !s.HasAuditLog(ctx, order.ID, "PAYMENT_CANCEL_FAILED") {
		return true
	}
	return !order.UpdatedAt.After(now.Add(-LifecyclePaymentExpiryRetryDelay))
}

// ReconcileProcessingOrders 低频补偿可能漏掉 Webhook 的渠道处理中订单。
func (s *OrderLifecycle) ReconcileProcessingOrders(ctx context.Context) (int, error) {
	return s.ReconcileProcessingOrdersAt(ctx, s.runtime.Now())
}
func (s *OrderLifecycle) ReconcileProcessingOrdersAt(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.store.ProcessingIDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("query processing payment order ids: %w", err)
	}
	pageIDs := s.NextReconcilePageIDs(ids, &s.processingReconcileCursor, LifecycleProcessingReconcileLimit)
	if len(pageIDs) == 0 {
		return 0, nil
	}
	orders, err := s.store.ProcessingOrders(ctx, pageIDs)
	if err != nil {
		return 0, fmt.Errorf("query processing payment orders: %w", err)
	}

	recovered := 0
	for _, order := range orders {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		prov, queryRef, resp, queryErr := s.QueryPaymentOrderProvider(ctx, order)
		if queryErr != nil {
			s.runtime.Log("warn", "query processing payment order failed", "orderID", order.ID, "error", queryErr)
			s.MaybeAuditStaleProcessingOrder(ctx, order, now, "query_failed")
			continue
		}
		outcome, applyErr := s.ApplyQueriedPaymentStatus(ctx, order, prov, queryRef, resp)
		if applyErr != nil {
			s.runtime.Log("error", "apply processing payment status failed", "orderID", order.ID, "error", applyErr)
			continue
		}
		switch outcome {
		case LifecycleCheckPaidResultAlreadyPaid:
			recovered++
		case LifecycleCheckPaidResultFailed:
			if err := s.MarkPaymentFailed(ctx, order, resp.TradeNo, prov.ProviderKey()); err != nil {
				s.runtime.Log("error", "finalize failed processing payment failed", "orderID", order.ID, "error", err)
			}
		default:
			s.MaybeAuditStaleProcessingOrder(ctx, order, now, resp.Status)
		}
	}
	return recovered, nil
}

// ReconcilePaidFulfillmentOrders 重试已收款但尚未完成的幂等履约。
func (s *OrderLifecycle) ReconcilePaidFulfillmentOrders(ctx context.Context) (int, error) {
	return s.ReconcilePaidFulfillmentOrdersAt(ctx, s.runtime.Now())
}
func (s *OrderLifecycle) ReconcilePaidFulfillmentOrdersAt(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.store.RecoverableFulfillmentIDs(ctx, now, LifecycleFulfillmentRetryDelay, FulfillmentLeaseDuration)
	if err != nil {
		return 0, fmt.Errorf("query paid fulfillment order ids: %w", err)
	}
	pageIDs := s.NextReconcilePageIDs(ids, &s.fulfillmentReconcileCursor, LifecycleFulfillmentReconcileLimit)
	if len(pageIDs) == 0 {
		return 0, nil
	}

	recovered := 0
	for _, orderID := range pageIDs {
		if err := ctx.Err(); err != nil {
			return recovered, err
		}
		if err := s.ExecuteFulfillment(ctx, orderID); err != nil {
			s.runtime.Log("warn", "retry paid order fulfillment failed", "orderID", orderID, "error", err)
			continue
		}
		order, err := s.store.Order(ctx, orderID)
		if err != nil {
			s.runtime.Log("warn", "reload reconciled fulfillment order failed", "orderID", orderID, "error", err)
			continue
		}
		if order.Status == OrderStatusCompleted {
			recovered++
		}
	}
	return recovered, nil
}
func (s *OrderLifecycle) NextReconcilePageIDs(ids []int64, cursor *uint64, limit int) []int64 {
	s.reconcileCursorMu.Lock()
	defer s.reconcileCursorMu.Unlock()

	pageIDs := ReconcilePageIDs(ids, *cursor, limit)
	if len(ids) > limit {
		*cursor = *cursor + 1
	}
	return pageIDs
}
func ReconcilePageIDs(ids []int64, cursor uint64, limit int) []int64 {
	if len(ids) == 0 || limit <= 0 {
		return nil
	}
	if len(ids) <= limit {
		return append([]int64(nil), ids...)
	}

	// 每轮推进一页，使长期不变的处理中订单也能覆盖整个待处理集合。
	pageCount := (len(ids) + limit - 1) / limit
	page := int(cursor % uint64(pageCount))
	start := page * limit
	end := start + limit
	if end > len(ids) {
		end = len(ids)
	}
	return append([]int64(nil), ids[start:end]...)
}
func (s *OrderLifecycle) MaybeAuditStaleProcessingOrder(ctx context.Context, order *Order, now time.Time, providerStatus string) {
	if order == nil || order.UpdatedAt.After(now.Add(-LifecycleProcessingStaleAfter)) || s.HasAuditLog(ctx, order.ID, "PAYMENT_PROCESSING_STALE") {
		return
	}
	s.runtime.Audit(ctx, order.ID, "PAYMENT_PROCESSING_STALE", "system", map[string]any{
		"processing_since": order.UpdatedAt,
		"provider_status":  providerStatus,
	})
}
