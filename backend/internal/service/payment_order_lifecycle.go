package service

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/ent/paymentauditlog"
	"github.com/BrandonVee/TokenRouter/ent/paymentorder"
	"github.com/BrandonVee/TokenRouter/internal/payment"
	"github.com/BrandonVee/TokenRouter/internal/payment/provider"
	infraerrors "github.com/BrandonVee/TokenRouter/internal/pkg/errors"
	"github.com/BrandonVee/TokenRouter/internal/pkg/servertiming"
)

// --- Cancel & Expire ---

// Cancel rate limit configuration constants.
const (
	rateLimitUnitDay           = "day"
	rateLimitUnitMinute        = "minute"
	rateLimitUnitHour          = "hour"
	rateLimitModeFixed         = "fixed"
	checkPaidResultAlreadyPaid = "already_paid"
	checkPaidResultCancelled   = "cancelled"
	checkPaidResultProcessing  = "processing"
	checkPaidResultFailed      = "failed"
	checkPaidResultUncertain   = "uncertain"
	pendingWxpayReconcileLimit = 20
	processingReconcileLimit   = 20
	fulfillmentReconcileLimit  = 20
	fulfillmentRetryDelay      = time.Minute
	processingStaleAfter       = 24 * time.Hour
)

var createPaymentProviderFromInstance = provider.CreateProvider

func (s *PaymentService) checkCancelRateLimit(ctx context.Context, userID int64, cfg *PaymentConfig) error {
	if !cfg.CancelRateLimitEnabled || cfg.CancelRateLimitMax <= 0 {
		return nil
	}
	windowStart := cancelRateLimitWindowStart(cfg)
	operator := fmt.Sprintf("user:%d", userID)
	count, err := s.entClient.PaymentAuditLog.Query().
		Where(
			paymentauditlog.ActionEQ("ORDER_CANCELLED"),
			paymentauditlog.OperatorEQ(operator),
			paymentauditlog.CreatedAtGTE(windowStart),
		).Count(ctx)
	if err != nil {
		slog.Error("check cancel rate limit failed", "userID", userID, "error", err)
		return nil // fail open
	}
	if count >= cfg.CancelRateLimitMax {
		return infraerrors.TooManyRequests("CANCEL_RATE_LIMITED", "cancel rate limited").
			WithMetadata(map[string]string{
				"max":    strconv.Itoa(cfg.CancelRateLimitMax),
				"window": strconv.Itoa(cfg.CancelRateLimitWindow),
				"unit":   cfg.CancelRateLimitUnit,
			})
	}
	return nil
}

func cancelRateLimitWindowStart(cfg *PaymentConfig) time.Time {
	now := time.Now()
	w := cfg.CancelRateLimitWindow
	if w <= 0 {
		w = 1
	}
	unit := cfg.CancelRateLimitUnit
	if unit == "" {
		unit = rateLimitUnitDay
	}
	if cfg.CancelRateLimitMode == rateLimitModeFixed {
		switch unit {
		case rateLimitUnitMinute:
			t := now.Truncate(time.Minute)
			return t.Add(-time.Duration(w-1) * time.Minute)
		case rateLimitUnitDay:
			y, m, d := now.Date()
			t := time.Date(y, m, d, 0, 0, 0, 0, now.Location())
			return t.AddDate(0, 0, -(w - 1))
		default: // hour
			t := now.Truncate(time.Hour)
			return t.Add(-time.Duration(w-1) * time.Hour)
		}
	}
	// rolling window
	switch unit {
	case rateLimitUnitMinute:
		return now.Add(-time.Duration(w) * time.Minute)
	case rateLimitUnitDay:
		return now.AddDate(0, 0, -w)
	default: // hour
		return now.Add(-time.Duration(w) * time.Hour)
	}
}

func (s *PaymentService) CancelOrder(ctx context.Context, orderID, userID int64) (string, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return "", infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.cancelCore(ctx, o, OrderStatusCancelled, fmt.Sprintf("user:%d", userID), "user cancelled order")
}

func (s *PaymentService) AdminCancelOrder(ctx context.Context, orderID int64) (string, error) {
	o, err := s.entClient.PaymentOrder.Get(ctx, orderID)
	if err != nil {
		return "", infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status != OrderStatusPending {
		return "", infraerrors.BadRequest("INVALID_STATUS", "order cannot be cancelled in current status")
	}
	return s.cancelCore(ctx, o, OrderStatusCancelled, "admin", "admin cancelled order")
}

func (s *PaymentService) cancelCore(ctx context.Context, o *dbent.PaymentOrder, fs, op, ad string) (string, error) {
	if o.PaymentTradeNo != "" || o.PaymentType != "" {
		prov, queryRef, resp, err := s.queryPaymentOrderProvider(ctx, o)
		if err != nil {
			s.recordPaymentCancelFailure(ctx, o, prov, queryRef, err, nil)
			return "", err
		}
		outcome, err := s.applyQueriedPaymentStatus(ctx, o, prov, queryRef, resp)
		if err != nil {
			return outcome, err
		}
		switch outcome {
		case checkPaidResultAlreadyPaid:
			return outcome, nil
		case checkPaidResultProcessing:
			return s.processingCancellationResult(fs)
		case checkPaidResultUncertain:
			err := fmt.Errorf("provider returned an invalid paid response")
			s.recordPaymentCancelFailure(ctx, o, prov, queryRef, err, resp)
			return "", err
		case checkPaidResultFailed:
			return s.finalizePendingOrder(ctx, o, fs, op, ad)
		}

		if cp, ok := prov.(payment.CancelableProvider); ok {
			finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
			cancelErr := cp.CancelPayment(ctx, queryRef)
			finishProviderCall()
			if cancelErr != nil {
				// 关闭请求可能与付款完成并发，必须二次查单后才能决定本地终态。
				retryResp, retryErr := s.queryPaymentOrderWithProvider(ctx, prov, queryRef)
				if retryErr == nil {
					retryOutcome, applyErr := s.applyQueriedPaymentStatus(ctx, o, prov, queryRef, retryResp)
					if applyErr != nil {
						return retryOutcome, applyErr
					}
					switch retryOutcome {
					case checkPaidResultAlreadyPaid:
						return retryOutcome, nil
					case checkPaidResultProcessing:
						return s.processingCancellationResult(fs)
					case checkPaidResultFailed:
						return s.finalizePendingOrder(ctx, o, fs, op, ad)
					}
				}
				s.recordPaymentCancelFailure(ctx, o, prov, queryRef, cancelErr, retryResp)
				if retryErr != nil {
					return "", fmt.Errorf("cancel upstream payment: %w; requery: %v", cancelErr, retryErr)
				}
				return "", fmt.Errorf("cancel upstream payment: %w", cancelErr)
			}
		}
	}
	return s.finalizePendingOrder(ctx, o, fs, op, ad)
}

func (s *PaymentService) processingCancellationResult(finalStatus string) (string, error) {
	if finalStatus == OrderStatusExpired {
		return checkPaidResultProcessing, nil
	}
	return checkPaidResultProcessing, infraerrors.BadRequest("INVALID_STATUS", "payment is processing and cannot be cancelled")
}

func (s *PaymentService) finalizePendingOrder(ctx context.Context, o *dbent.PaymentOrder, fs, op, ad string) (string, error) {
	c, err := s.entClient.PaymentOrder.Update().
		Where(paymentorder.IDEQ(o.ID), paymentorder.StatusEQ(OrderStatusPending)).
		SetStatus(fs).
		Save(ctx)
	if err != nil {
		return "", fmt.Errorf("update order status: %w", err)
	}
	if c > 0 {
		auditAction := "ORDER_CANCELLED"
		if fs == OrderStatusExpired {
			auditAction = "ORDER_EXPIRED"
		}
		s.writeAuditLog(ctx, o.ID, auditAction, op, map[string]any{"detail": ad})
	}
	if c > 0 {
		return checkPaidResultCancelled, nil
	}
	current, err := s.entClient.PaymentOrder.Get(ctx, o.ID)
	if err != nil {
		return "", fmt.Errorf("reload order after cancellation race: %w", err)
	}
	switch current.Status {
	case OrderStatusPaid, OrderStatusRecharging, OrderStatusCompleted:
		return checkPaidResultAlreadyPaid, nil
	case OrderStatusProcessing:
		return s.processingCancellationResult(fs)
	case OrderStatusCancelled, OrderStatusExpired:
		return checkPaidResultCancelled, nil
	default:
		return "", fmt.Errorf("order status changed to %s while cancelling", current.Status)
	}
}

func (s *PaymentService) recordPaymentCancelFailure(ctx context.Context, o *dbent.PaymentOrder, prov payment.Provider, queryRef string, cancelErr error, resp *payment.QueryOrderResponse) {
	if s.hasAuditLog(ctx, o.ID, "PAYMENT_CANCEL_FAILED") {
		return
	}
	providerKey := "system"
	if prov != nil {
		providerKey = prov.ProviderKey()
	}
	detail := map[string]any{
		"queryRef": queryRef,
		"error":    psErrMsg(cancelErr),
	}
	if resp != nil {
		detail["providerStatus"] = resp.Status
		detail["tradeNo"] = resp.TradeNo
	}
	s.writeAuditLog(ctx, o.ID, "PAYMENT_CANCEL_FAILED", providerKey, detail)
}

func (s *PaymentService) checkPaid(ctx context.Context, o *dbent.PaymentOrder) (string, error) {
	prov, queryRef, resp, err := s.queryPaymentOrderProvider(ctx, o)
	if err != nil {
		slog.Warn("query upstream failed", "orderID", o.ID, "error", err)
		return "", nil
	}
	return s.applyQueriedPaymentStatus(ctx, o, prov, queryRef, resp)
}

func (s *PaymentService) queryPaymentOrderProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, string, *payment.QueryOrderResponse, error) {
	prov, err := s.getOrderProvider(ctx, o)
	if err != nil {
		return nil, "", nil, fmt.Errorf("resolve order provider: %w", err)
	}
	queryRef := paymentOrderQueryReference(o, prov)
	if queryRef == "" {
		return prov, "", nil, fmt.Errorf("payment order %d has no upstream query reference", o.ID)
	}
	resp, err := s.queryPaymentOrderWithProvider(ctx, prov, queryRef)
	return prov, queryRef, resp, err
}

func (s *PaymentService) queryPaymentOrderWithProvider(ctx context.Context, prov payment.Provider, queryRef string) (*payment.QueryOrderResponse, error) {
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
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

func (s *PaymentService) applyQueriedPaymentStatus(ctx context.Context, o *dbent.PaymentOrder, prov payment.Provider, queryRef string, resp *payment.QueryOrderResponse) (string, error) {
	if resp == nil {
		return "", fmt.Errorf("missing provider query response")
	}
	if resp.Status == payment.ProviderStatusPaid {
		if !isValidProviderAmount(resp.Amount) {
			s.writeAuditLog(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", prov.ProviderKey(), map[string]any{
				"expected": o.PayAmount,
				"paid":     resp.Amount,
				"tradeNo":  resp.TradeNo,
				"queryRef": queryRef,
			})
			slog.Warn("query upstream returned invalid paid amount", "orderID", o.ID, "queryRef", queryRef, "paid", resp.Amount)
			retriedResp, retryOK := requeryPaidOrderOnce(ctx, prov, queryRef)
			if !retryOK {
				return checkPaidResultUncertain, nil
			}
			resp = retriedResp
		}
		notificationTradeNo := o.PaymentTradeNo
		if upstreamTradeNo := strings.TrimSpace(resp.TradeNo); paymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, notificationTradeNo) {
			if _, updateErr := s.entClient.PaymentOrder.Update().
				Where(paymentorder.IDEQ(o.ID)).
				SetPaymentTradeNo(upstreamTradeNo).
				Save(ctx); updateErr != nil {
				return checkPaidResultAlreadyPaid, fmt.Errorf("persist upstream trade no during checkPaid: %w", updateErr)
			} else {
				o.PaymentTradeNo = upstreamTradeNo
			}
			notificationTradeNo = upstreamTradeNo
		}
		if err := s.HandlePaymentNotification(ctx, &payment.PaymentNotification{TradeNo: notificationTradeNo, OrderID: o.OutTradeNo, Amount: resp.Amount, Status: payment.ProviderStatusSuccess, Metadata: resp.Metadata}, prov.ProviderKey()); err != nil {
			return checkPaidResultAlreadyPaid, err
		}
		return checkPaidResultAlreadyPaid, nil
	}
	if resp.Status == payment.ProviderStatusProcessing {
		tradeNo := strings.TrimSpace(resp.TradeNo)
		if tradeNo == "" {
			tradeNo = strings.TrimSpace(o.PaymentTradeNo)
		}
		err := s.HandlePaymentNotification(ctx, &payment.PaymentNotification{
			TradeNo:  tradeNo,
			OrderID:  o.OutTradeNo,
			Amount:   resp.Amount,
			Status:   payment.ProviderStatusProcessing,
			Metadata: resp.Metadata,
		}, prov.ProviderKey())
		return checkPaidResultProcessing, err
	}
	if resp.Status == payment.ProviderStatusFailed {
		return checkPaidResultFailed, nil
	}
	return "", nil
}

func requeryPaidOrderOnce(ctx context.Context, prov payment.Provider, queryRef string) (*payment.QueryOrderResponse, bool) {
	if prov == nil || strings.TrimSpace(queryRef) == "" {
		return nil, false
	}
	finishProviderCall := servertiming.ObserveDependency(ctx, "payment")
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	if err != nil {
		slog.Warn("query upstream retry failed", "queryRef", queryRef, "error", err)
		return nil, false
	}
	if resp == nil || resp.Status != payment.ProviderStatusPaid || !isValidProviderAmount(resp.Amount) {
		return nil, false
	}
	return resp, true
}

func paymentOrderQueryReference(order *dbent.PaymentOrder, prov payment.Provider) string {
	if order == nil {
		return ""
	}

	providerKey := ""
	if prov != nil {
		providerKey = strings.TrimSpace(prov.ProviderKey())
	}
	if providerKey == "" {
		if snapshot := psOrderProviderSnapshot(order); snapshot != nil {
			providerKey = strings.TrimSpace(snapshot.ProviderKey)
		}
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(psStringValue(order.ProviderKey))
	}
	if providerKey == "" {
		providerKey = strings.TrimSpace(order.PaymentType)
	}

	switch payment.GetBasePaymentType(providerKey) {
	case payment.TypeAlipay, payment.TypeEasyPay, payment.TypeWxpay:
		return strings.TrimSpace(order.OutTradeNo)
	case payment.TypeStripe:
		if tradeNo := strings.TrimSpace(order.PaymentTradeNo); strings.HasPrefix(tradeNo, "cs_") {
			return tradeNo
		}
		if invoiceID := strings.TrimSpace(psStringValue(order.PaymentInvoiceID)); invoiceID != "" {
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

func paymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, currentTradeNo string) bool {
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
func (s *PaymentService) VerifyOrderByOutTradeNo(ctx context.Context, outTradeNo string, userID int64) (*dbent.PaymentOrder, error) {
	outTradeNo, err := normalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNo(outTradeNo)).
		Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.UserID != userID {
		return nil, infraerrors.Forbidden("FORBIDDEN", "no permission for this order")
	}
	// 待支付和已过期订单允许主动补查；处理中订单交给低频后台对账。
	if o.Status == OrderStatusPending || o.Status == OrderStatusExpired {
		result, checkErr := s.checkPaid(ctx, o)
		if checkErr != nil {
			return nil, checkErr
		}
		if result == checkPaidResultAlreadyPaid || result == checkPaidResultProcessing {
			// 重新读取以返回原子状态转换后的结果。
			o, err = s.entClient.PaymentOrder.Get(ctx, o.ID)
			if err != nil {
				return nil, fmt.Errorf("reload order: %w", err)
			}
		}
	}
	return o, nil
}

// ReconcilePendingWxpayOrders 主动补偿未收到回调的微信待支付订单，避免等到过期才发现已支付。
func (s *PaymentService) ReconcilePendingWxpayOrders(ctx context.Context) (int, error) {
	now := time.Now()
	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.StatusEQ(OrderStatusPending),
			paymentorder.ExpiresAtGT(now),
			paymentorder.Or(
				paymentorder.PaymentTypeEQ(payment.TypeWxpay),
				paymentorder.PaymentTypeHasPrefix(payment.TypeWxpay+"_"),
				paymentorder.ProviderKeyEQ(payment.TypeWxpay),
				paymentorder.ProviderKeyHasPrefix(payment.TypeWxpay+"_"),
			),
		).
		Order(dbent.Asc(paymentorder.FieldCreatedAt)).
		Limit(pendingWxpayReconcileLimit).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query pending wxpay orders: %w", err)
	}

	recovered := 0
	for _, order := range orders {
		outcome, checkErr := s.checkPaid(ctx, order)
		if checkErr != nil {
			slog.Warn("reconcile pending wxpay order failed", "orderID", order.ID, "error", checkErr)
			continue
		}
		if outcome == checkPaidResultAlreadyPaid {
			recovered++
		}
	}
	return recovered, nil
}

// VerifyOrderPublic returns the currently persisted public order state without
// triggering any upstream reconciliation. Signed resume-token recovery is the
// only public recovery path allowed to query upstream state.
func (s *PaymentService) VerifyOrderPublic(ctx context.Context, outTradeNo string) (*dbent.PaymentOrder, error) {
	outTradeNo, err := normalizeOrderLookupOutTradeNo(outTradeNo)
	if err != nil {
		return nil, err
	}
	o, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.OutTradeNo(outTradeNo)).
		Only(ctx)
	if err != nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	return o, nil
}

func normalizeOrderLookupOutTradeNo(raw string) (string, error) {
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

func (s *PaymentService) ExpireTimedOutOrders(ctx context.Context) (int, error) {
	now := time.Now()
	orders, err := s.entClient.PaymentOrder.Query().Where(paymentorder.StatusEQ(OrderStatusPending), paymentorder.ExpiresAtLTE(now)).All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query expired: %w", err)
	}
	n := 0
	for _, o := range orders {
		// 到期决策必须先确认上游状态并成功关闭待支付单据。
		outcome, cancelErr := s.cancelCore(ctx, o, OrderStatusExpired, "system", "order expired")
		if cancelErr != nil {
			slog.Warn("keep timed-out order pending after upstream cancellation failure", "orderID", o.ID, "error", cancelErr)
			continue
		}
		if outcome == checkPaidResultAlreadyPaid {
			slog.Info("order was paid during expiry", "orderID", o.ID)
			continue
		}
		if outcome == checkPaidResultCancelled {
			n++
		}
	}
	return n, nil
}

// ReconcileProcessingOrders 低频补偿可能漏掉 Webhook 的渠道处理中订单。
func (s *PaymentService) ReconcileProcessingOrders(ctx context.Context) (int, error) {
	return s.reconcileProcessingOrdersAt(ctx, time.Now())
}

func (s *PaymentService) reconcileProcessingOrdersAt(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.entClient.PaymentOrder.Query().
		Where(paymentorder.StatusEQ(OrderStatusProcessing)).
		Order(paymentorder.ByID()).
		IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("query processing payment order ids: %w", err)
	}
	pageIDs := s.nextReconcilePageIDs(ids, &s.processingReconcileCursor, processingReconcileLimit)
	if len(pageIDs) == 0 {
		return 0, nil
	}
	orders, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.IDIn(pageIDs...),
			paymentorder.StatusEQ(OrderStatusProcessing),
		).
		Order(paymentorder.ByID()).
		All(ctx)
	if err != nil {
		return 0, fmt.Errorf("query processing payment orders: %w", err)
	}

	recovered := 0
	for _, order := range orders {
		prov, queryRef, resp, queryErr := s.queryPaymentOrderProvider(ctx, order)
		if queryErr != nil {
			slog.Warn("query processing payment order failed", "orderID", order.ID, "error", queryErr)
			s.maybeAuditStaleProcessingOrder(ctx, order, now, "query_failed")
			continue
		}
		outcome, applyErr := s.applyQueriedPaymentStatus(ctx, order, prov, queryRef, resp)
		if applyErr != nil {
			slog.Error("apply processing payment status failed", "orderID", order.ID, "error", applyErr)
			continue
		}
		switch outcome {
		case checkPaidResultAlreadyPaid:
			recovered++
		case checkPaidResultFailed:
			if err := s.markPaymentFailed(ctx, order, resp.TradeNo, prov.ProviderKey()); err != nil {
				slog.Error("finalize failed processing payment failed", "orderID", order.ID, "error", err)
			}
		default:
			s.maybeAuditStaleProcessingOrder(ctx, order, now, resp.Status)
		}
	}
	return recovered, nil
}

// ReconcilePaidFulfillmentOrders 重试已收款但尚未完成的幂等履约。
func (s *PaymentService) ReconcilePaidFulfillmentOrders(ctx context.Context) (int, error) {
	return s.reconcilePaidFulfillmentOrdersAt(ctx, time.Now())
}

func (s *PaymentService) reconcilePaidFulfillmentOrdersAt(ctx context.Context, now time.Time) (int, error) {
	ids, err := s.entClient.PaymentOrder.Query().
		Where(
			paymentorder.PaidAtNotNil(),
			paymentorder.Or(
				paymentorder.And(
					paymentorder.StatusIn(OrderStatusPaid, OrderStatusFailed),
					paymentorder.UpdatedAtLTE(now.Add(-fulfillmentRetryDelay)),
				),
				paymentorder.And(
					paymentorder.StatusEQ(OrderStatusRecharging),
					paymentorder.UpdatedAtLTE(now.Add(-paymentFulfillmentLeaseDuration)),
				),
			),
		).
		Order(paymentorder.ByID()).
		IDs(ctx)
	if err != nil {
		return 0, fmt.Errorf("query paid fulfillment order ids: %w", err)
	}
	pageIDs := s.nextReconcilePageIDs(ids, &s.fulfillmentReconcileCursor, fulfillmentReconcileLimit)
	if len(pageIDs) == 0 {
		return 0, nil
	}

	recovered := 0
	for _, orderID := range pageIDs {
		if err := s.executeFulfillment(ctx, orderID); err != nil {
			slog.Warn("retry paid order fulfillment failed", "orderID", orderID, "error", err)
			continue
		}
		order, err := s.entClient.PaymentOrder.Get(ctx, orderID)
		if err != nil {
			slog.Warn("reload reconciled fulfillment order failed", "orderID", orderID, "error", err)
			continue
		}
		if order.Status == OrderStatusCompleted {
			recovered++
		}
	}
	return recovered, nil
}

func (s *PaymentService) nextReconcilePageIDs(ids []int64, cursor *uint64, limit int) []int64 {
	s.reconcileCursorMu.Lock()
	defer s.reconcileCursorMu.Unlock()

	pageIDs := reconcilePageIDs(ids, *cursor, limit)
	if len(ids) > limit {
		*cursor = *cursor + 1
	}
	return pageIDs
}

func reconcilePageIDs(ids []int64, cursor uint64, limit int) []int64 {
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

func (s *PaymentService) maybeAuditStaleProcessingOrder(ctx context.Context, order *dbent.PaymentOrder, now time.Time, providerStatus string) {
	if order == nil || order.UpdatedAt.After(now.Add(-processingStaleAfter)) || s.hasAuditLog(ctx, order.ID, "PAYMENT_PROCESSING_STALE") {
		return
	}
	s.writeAuditLog(ctx, order.ID, "PAYMENT_PROCESSING_STALE", "system", map[string]any{
		"processing_since": order.UpdatedAt,
		"provider_status":  providerStatus,
	})
}

// getOrderProvider creates a provider using the order's original instance config.
// Falls back to registry lookup if instance ID is missing (legacy orders).
func (s *PaymentService) getOrderProvider(ctx context.Context, o *dbent.PaymentOrder) (payment.Provider, error) {
	inst, err := s.getOrderProviderInstance(ctx, o)
	if err != nil {
		return nil, fmt.Errorf("load order provider instance: %w", err)
	}
	if inst != nil {
		return s.createProviderFromInstance(ctx, inst)
	}
	if !paymentOrderAllowsRegistryFallback(o) {
		return nil, fmt.Errorf("order %d provider instance is unresolved", o.ID)
	}
	providerKey := paymentOrderFallbackProviderKey(s.registry, o)
	if providerKey == "" {
		return nil, fmt.Errorf("order %d provider fallback key is missing", o.ID)
	}
	if !s.webhookRegistryFallbackAllowed(ctx, providerKey) {
		return nil, fmt.Errorf("order %d provider fallback is ambiguous for %s", o.ID, providerKey)
	}
	s.EnsureProviders(ctx)
	return s.registry.GetProvider(o.PaymentType)
}

func paymentOrderAllowsRegistryFallback(order *dbent.PaymentOrder) bool {
	if order == nil {
		return false
	}
	if psOrderProviderSnapshot(order) != nil {
		return false
	}
	if strings.TrimSpace(psStringValue(order.ProviderInstanceID)) != "" {
		return false
	}
	if strings.TrimSpace(psStringValue(order.ProviderKey)) != "" {
		return false
	}
	return true
}

func paymentOrderFallbackProviderKey(registry *payment.Registry, order *dbent.PaymentOrder) string {
	if order == nil {
		return ""
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(payment.PaymentType(order.PaymentType))); key != "" {
			return key
		}
	}
	return strings.TrimSpace(payment.GetBasePaymentType(strings.TrimSpace(order.PaymentType)))
}

func (s *PaymentService) createProviderFromInstance(ctx context.Context, inst *dbent.PaymentProviderInstance) (payment.Provider, error) {
	if inst == nil {
		return nil, fmt.Errorf("payment provider instance is missing")
	}

	cfg, err := s.loadBalancer.GetInstanceConfig(ctx, int64(inst.ID))
	if err != nil {
		return nil, fmt.Errorf("load provider instance config: %w", err)
	}
	if inst.PaymentMode != "" {
		cfg["paymentMode"] = inst.PaymentMode
	}

	instID := strconv.FormatInt(int64(inst.ID), 10)
	prov, err := createPaymentProviderFromInstance(inst.ProviderKey, instID, cfg)
	if err != nil {
		return nil, fmt.Errorf("create provider from instance: %w", err)
	}
	return prov, nil
}

func psStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
