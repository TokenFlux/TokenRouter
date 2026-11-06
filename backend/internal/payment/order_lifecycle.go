package payment

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// order_lifecycle.go reconciles provider payment state and coordinates
// cancellation, expiration, and unfinished fulfillment recovery.
// A pending local order can be paid upstream while cancellation or timeout is
// deciding its terminal state. Cancellation queries the bound merchant first,
// applies accepted payment progress, and requeries once after a failed channel
// cancellation before allowing local close. Unavailable upstream observations
// preserve local pending handling and record retry diagnostics; forced
// expiration has its separate administrator reason/audit transaction.
//
// Recovery scans check context between orders and count their actual outcome
// classes rather than assuming every attempt changed state. Processing and
// unfinished-fulfillment scans rotate copied ID pages under shared cursor
// coordination, advancing selection before later reads or work succeed.
// Pending reconciliation uses the store's bounded oldest unexpired
// Alipay/WeChat/GMPay candidates instead. Cancellation-failure history adds an
// updated_at cooldown, while stale processing is audited after a longer age.
// Provider references retain merchant/session/invoice/payment identities
// according to adapter query needs.

// Lifecycle result labels distinguish provider payment/cancellation
// observations, while limits and ages bound recovery page selection, retry
// eligibility and stale-processing diagnostics.
const (
	// queried paid outcome label.
	LifecycleCheckPaidResultAlreadyPaid = "already_paid"
	// locally closed outcome label.
	LifecycleCheckPaidResultCancelled = "cancelled"
	// provider processing outcome label.
	LifecycleCheckPaidResultProcessing = "processing"
	// queried terminal failure label.
	LifecycleCheckPaidResultFailed = "failed"
	// invalid paid observation label.
	LifecycleCheckPaidResultUncertain = "uncertain"
	// oldest pending query limit.
	LifecyclePendingPaymentReconcileLimit = 20
	// processing recovery page bound.
	LifecycleProcessingReconcileLimit = 20
	// fulfillment recovery page bound.
	LifecycleFulfillmentReconcileLimit = 20
	// paid failure retry age.
	LifecycleFulfillmentRetryDelay = time.Minute
	// processing stale audit age.
	LifecycleProcessingStaleAfter = 24 * time.Hour
	// cancellation failure cooldown age.
	LifecyclePaymentExpiryRetryDelay = 15 * time.Minute
)

// CancelOrder checks the numeric order belongs to the supplied user and is
// pending before delegating channel-aware cancellation. Any read error becomes
// NOT_FOUND; success returns CancelCore's outcome, which can report
// payment/processing progress rather than a newly cancelled state.
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

// AdminCancelOrder checks the numeric order is pending before delegating
// administrator channel-aware cancellation. Any read error becomes NOT_FOUND
// and no buyer ownership check is performed; outcome/error reflect the same
// upstream race handling as buyer cancellation.
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

// ForceExpireOrder validates a trimmed nonblank reason of at most five hundred
// runes and delegates pending expiration to the store's required-audit
// transaction. It performs no provider query or cancellation; verified late
// success can still recover EXPIRED state through fulfillment.
// @project-doc docs/domains/payments_and_entitlements.md#forced_expiration_recovery
func (s *OrderLifecycle) ForceExpireOrder(ctx context.Context, orderID int64, reason string) error {
	reason = strings.TrimSpace(reason)
	// Limit the trimmed administrator reason to five hundred Unicode runes.
	if reason == "" || len([]rune(reason)) > 500 {
		return infraerrors.BadRequest("INVALID_FORCE_EXPIRE_REASON", "force expiration reason is invalid")
	}

	return s.store.ForceExpire(ctx, orderID, reason)
}

// CancelCore resolves/query-checks a nonnil pending observation before
// conditionally closing it to fs with the supplied operator/detail. Callers
// choose the intended cancelled/expired final state and validate their access
// before invoking this core.
//
// Paid progress runs notification handling, processing selects
// user-versus-expiry policy, and queried failure can proceed to local close.
// An invalid paid report gets one corrective query; unresolved uncertainty
// here records failure and returns its plain error. A CancelableProvider is
// called for remaining progress, and cancellation errors trigger one requery
// before deciding paid, processing or failed handling. Other
// cancellation/query failures become PAYMENT_STATUS_UNAVAILABLE. Missing
// cancel capability, or both PaymentTradeNo and PaymentType empty, can still
// reach conditional close without a channel cancellation call.
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
				// A failed channel cancellation can coincide with payment progress.
				// Requery once before local close so a paid/processing observation can use
				// its recovery result instead of the cancellation error alone.
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

// ProcessingCancellationResult returns processing with nil error for timeout
// expiration, leaving its recovery path active. Other requested final states
// return processing plus INVALID_STATUS, so buyer/admin cancellation cannot
// claim a successful close after provider processing progress.
func (s *OrderLifecycle) ProcessingCancellationResult(finalStatus string) (string, error) {
	if finalStatus == OrderStatusExpired {
		return LifecycleCheckPaidResultProcessing, nil
	}
	return LifecycleCheckPaidResultProcessing, infraerrors.BadRequest("INVALID_STATUS", "payment is processing and cannot be cancelled")
}

// FinalizePendingOrder conditionally closes only pending state to the caller's
// fs and records its ordinary cancellation/expiration action when affected
// count is positive. Supply a nonnil order and suitable final
// status/operator/detail.
//
// Positive count returns the cancelled outcome for either cancelled or expired
// target. Zero count rereads payment/completion, processing or already-closed
// state to report the competing result; other states or read failures return
// errors. Those outcome labels therefore do not prove this invocation newly
// changed state. The store update and ordinary audit are separate operations,
// and verified late success remains eligible through fulfillment.
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

// RecordPaymentCancelFailure attempts a failure audit when its presence
// boolean is false, then refreshes pending updated_at after every invocation.
// Supply a nonnil order; absent provider uses system attribution and optional
// response adds reported status/trade data.
//
// Audit presence has no separate read-error channel, and the errorless runtime
// auditor cannot return persistence failure. TouchPending is conditional on
// current pending status and its error is logged rather than returned, so
// cooldown evidence depends on those successful observations/writes. A prior
// audit suppresses another event but not the timestamp refresh; this routine
// does not universally guarantee one record or a stored retry time.
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

	// Refresh retry time after every failure even when prior history suppressed
	// another audit. Touch only current pending state; a failed update is logged
	// and cannot establish the next expiry cooldown.
	if err := s.store.TouchPending(ctx, o.ID, s.runtime.Now()); err != nil {
		s.runtime.Log("warn", "record payment cancellation retry time failed", "orderID", o.ID, "error", err)
	}
}

// PaymentStatusUnavailableError returns a service-unavailable payment
// observation error with the supplied cause retained. Cancellation callers use
// its reason/message while the uncertain local order remains available for a
// later upstream decision.
func PaymentStatusUnavailableError(cause error) error {
	return infraerrors.ServiceUnavailable("PAYMENT_STATUS_UNAVAILABLE", "payment status is temporarily unavailable").WithCause(cause)
}

// CheckPaid queries the nonnil order's bound provider and applies an accepted
// response. Resolution/query failures are logged and converted to empty
// outcome with nil error, while application errors remain visible to
// verification/recovery callers.
func (s *OrderLifecycle) CheckPaid(ctx context.Context, o *Order) (string, error) {
	prov, queryRef, resp, err := s.QueryPaymentOrderProvider(ctx, o)
	// Keep the saved order usable when upstream resolution/query is unavailable.
	// Return empty outcome with nil error after diagnostics; cancellation uses
	// its separate status-unavailable policy instead of this compatibility
	// result.
	if err != nil {
		s.runtime.Log("warn", "query upstream failed", "orderID", o.ID, "error", err)
		return "", nil
	}
	return s.ApplyQueriedPaymentStatus(ctx, o, prov, queryRef, resp)
}

// QueryPaymentOrderProvider resolves the nonnil order's historical provider
// and derives its adapter-specific query reference. Missing provider/reference
// returns an error; a resolved provider/reference accompanies the delegated
// query result/error so cancellation can record useful diagnostics.
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

// QueryPaymentOrderWithProvider invokes QueryOrder with the supplied usable
// provider/reference and wraps query errors. The observation callback brackets
// the call; a nil successful response becomes an error so later state handling
// receives usable data. An explicitly absent EasyPay order is unpaid for
// polling and cancellation; callback confirmation still uses QueryOrder directly.
func (s *OrderLifecycle) QueryPaymentOrderWithProvider(ctx context.Context, prov Provider, queryRef string) (*QueryOrderResponse, error) {
	finishProviderCall := s.observe(ctx)
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	// Hosted EasyPay checkout can be abandoned before the channel creates an
	// order. Its explicit absence permits cancellation while transport and
	// credential failures remain errors.
	if GetBasePaymentType(prov.ProviderKey()) == TypeEasyPay && errors.Is(err, ErrProviderOrderNotFound) {
		return &QueryOrderResponse{Status: ProviderStatusPending}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query upstream payment %s: %w", queryRef, err)
	}
	if resp == nil {
		return nil, fmt.Errorf("query upstream payment %s returned no response", queryRef)
	}
	return resp, nil
}

// ApplyQueriedPaymentStatus translates a nonnil provider observation into
// lifecycle outcome and applicable notification handling for a nonnil order.
// Supply a usable provider; paid/processing outcomes can accompany local
// persistence/fulfillment errors, so callers check error before trusting the
// label.
//
// Paid reports with invalid money get one requery; unresolved invalidity
// returns uncertain,nil. A distinct upstream trade reference is saved before
// notification and written into the caller order only after that save
// succeeds. Fulfillment then validates amount/provider metadata separately.
// EasyPay records its queried reference with the paid transition after order
// validation, keeping concurrent queries from changing a fulfillment lease.
// Processing uses reported or saved trade reference and the normal progress
// notification, while queried failure returns its label without invoking
// failure handling here. Other statuses return empty,nil.
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
			// An invalid paid quantity cannot enter ordinary money confirmation. Try
			// one fresh paid query, then return uncertainty if it still lacks usable
			// money so cancellation can preserve its unresolved decision.
			retriedResp, retryOK := s.RequeryPaidOrderOnce(ctx, prov, queryRef)
			if !retryOK {
				return LifecycleCheckPaidResultUncertain, nil
			}
			resp = retriedResp
		}
		if err := validatePaymentTradeNumber(o, prov.ProviderKey(), resp.TradeNo); err != nil {
			return LifecycleCheckPaidResultUncertain, err
		}
		notificationTradeNo := o.PaymentTradeNo
		if GetBasePaymentType(prov.ProviderKey()) == TypeEasyPay {
			// Publish EasyPay's identity with the paid transition. A separate reference
			// update could change updated_at after a concurrent callback claimed fulfillment.
			notificationTradeNo = resp.TradeNo
		} else if upstreamTradeNo := strings.TrimSpace(resp.TradeNo); PaymentOrderShouldPersistUpstreamTradeNo(queryRef, upstreamTradeNo, notificationTradeNo) {
			// Save a newly revealed upstream resource reference before creating its
			// success notification. Only successful persistence changes the caller
			// observation, and a write error accompanies the already-paid query
			// outcome.
			updateErr := s.store.SaveUpstreamTradeNumber(ctx, o.ID, upstreamTradeNo)
			if updateErr != nil {
				return LifecycleCheckPaidResultAlreadyPaid, fmt.Errorf("persist upstream trade no during checkPaid: %w", updateErr)
			}
			o.PaymentTradeNo = upstreamTradeNo
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
		// Keep the saved reference when a processing query reports no trade ID.
		// Notification handling can still prefer explicit Stripe checkout-session
		// metadata before its conditional processing update.
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

// RequeryPaidOrderOnce makes one corrective query and accepts only a nonnil
// paid result with finite positive money. Nil provider, blank reference, read
// error or another invalid/status result returns false; errors are logged and
// no local amount/snapshot comparison is performed here.
func (s *OrderLifecycle) RequeryPaidOrderOnce(ctx context.Context, prov Provider, queryRef string) (*QueryOrderResponse, bool) {
	if prov == nil || strings.TrimSpace(queryRef) == "" {
		return nil, false
	}
	finishProviderCall := s.observe(ctx)
	resp, err := prov.QueryOrder(ctx, queryRef)
	finishProviderCall()
	// A failed corrective query cannot validate the earlier paid amount. Return
	// false after diagnostics so the caller retains its uncertain observation
	// rather than accepting an error as usable payment data.
	if err != nil {
		s.runtime.Log("warn", "query upstream retry failed", "queryRef", queryRef, "error", err)
		return nil, false
	}
	if resp == nil || resp.Status != ProviderStatusPaid || !IsValidProviderAmount(resp.Amount) {
		return nil, false
	}
	return resp, true
}

// PaymentOrderQueryReference selects the identifier the resolved adapter
// expects for the supplied order. Nil order returns empty, and provider
// identity prefers the usable adapter key before snapshot, column and method
// text.
//
// Alipay/EasyPay/WeChat use the merchant OutTradeNo. Stripe prefers a cs_
// hosted session, then stored invoice ID, then another trade reference and
// finally merchant number. Other adapters prefer stored trade before merchant
// number. This precedence keeps query resources distinct from the order's
// local numeric ID and refund identifier; selecting a reference does not
// itself validate the saved merchant or its current credentials.
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

	// Choose the query resource namespace expected by the resolved adapter.
	// Merchant numbers address direct/form orders, while Stripe
	// session/invoice/intent precedence preserves its hosted progress identity
	// across queries.
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

// PaymentOrderShouldPersistUpstreamTradeNo reports a nonblank returned
// reference differing case-insensitively from both current trade and query
// reference. It trims all compared values, allowing query handling to save a
// newly revealed resource identity before notification.
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

// VerifyOrderByOutTradeNo normalizes an exact merchant number, checks buyer
// ownership and queries payment only for pending/expired observations. Other
// observed states return as read; any initial lookup error becomes NOT_FOUND.
//
// CheckPaid can suppress query failure into unchanged saved state. Accepted
// paid/processing outcome with nil application error triggers a numeric row
// reread before return, reflecting the conditional transition rather than the
// initial observation. Processing recovery for already-processing rows remains
// with the background reconciler, and this lookup does not expand aliases or
// the callback legacy numeric form.
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
	// Query pending or expired observations for missed payment progress. Other
	// states retain the saved read here, while already-processing recovery uses
	// its separate bounded background scan.
	if o.Status == OrderStatusPending || o.Status == OrderStatusExpired {
		result, checkErr := s.CheckPaid(ctx, o)
		if checkErr != nil {
			return nil, checkErr
		}
		if result == LifecycleCheckPaidResultAlreadyPaid || result == LifecycleCheckPaidResultProcessing {
			// Read current persisted state after successful paid/processing handling
			// before returning to the buyer. The initial observation can be stale after
			// that conditional transition or concurrent completion.
			o, err = s.store.Order(ctx, o.ID)
			if err != nil {
				return nil, fmt.Errorf("reload order: %w", err)
			}
		}
	}
	return o, nil
}

// ReconcilePendingPaymentOrders queries the bounded pending candidates at the
// runtime clock and checks payment sequentially. Store errors abort and
// context is checked between rows, returning the count observed before
// cancellation.
//
// The PostgreSQL candidate query chooses oldest unexpired Alipay/WeChat/GMPay method
// or provider labels. Per-order application errors are logged and skipped;
// CheckPaid itself can convert query failures to empty,nil. Only already-paid
// outcomes with nil error increase the count, which is an outcome observation
// rather than proof of a newly completed grant. No rotating pending cursor is
// used here.
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
		// Skip this failed payment application and continue the remaining pending
		// observations. The counter includes only accepted already-paid outcomes,
		// while underlying query unavailability can already have become empty,nil.
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

// VerifyOrderPublic returns saved state for a normalized exact merchant number
// and does not query the provider. Any lookup error becomes NOT_FOUND; HTTP
// exposes its minimal compatibility shape, while signed-session lookup owns
// the separate permitted public recovery path.
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

// NormalizeOrderLookupOutTradeNo trims and accepts nonblank merchant text up
// to sixty-four bytes using ASCII letters, digits, underscore or hyphen. Other
// input returns INVALID_OUT_TRADE_NO; it does not rewrite case, infer a local
// ID or expand an alias.
func NormalizeOrderLookupOutTradeNo(raw string) (string, error) {
	outTradeNo := strings.TrimSpace(raw)
	if outTradeNo == "" {
		return "", infraerrors.BadRequest("INVALID_OUT_TRADE_NO", "out_trade_no is required")
	}
	// Limit merchant lookup input to sixty-four encoded bytes.
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

// ExpireTimedOutOrders delegates timeout recovery using the injected lifecycle
// clock observation. The At method reads expired pending candidates, checks
// applicable retry cooldown and queries/closes their upstream progress before
// deciding local expiration.
func (s *OrderLifecycle) ExpireTimedOutOrders(ctx context.Context) (int, error) {
	now := s.runtime.Now()
	return s.ExpireTimedOutOrdersAt(ctx, now)
}

// ExpireTimedOutOrdersAt reads expired pending observations and performs
// channel-aware timeout handling using the supplied now for cooldown
// decisions. Initial store errors abort, and context cancellation between rows
// returns the accumulated count.
//
// Prior cancellation failure can defer an order until its updated_at cooldown
// ends. CancelCore checks provider state and applicable cancel capability
// before conditional local close; paid or processing progress remains with its
// recovery path. Per-order errors are logged and skipped. Only
// cancellation-class nil-error outcomes increase the count, including a
// competing already-closed state, so the count does not guarantee every row
// was newly changed to expired by this invocation.
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
		// Use the same query/cancel/conditional-close decision as cancellation
		// before local expiration. Provider progress or unavailable status can keep
		// this row out of the expired outcome count.
		outcome, cancelErr := s.CancelCore(ctx, o, OrderStatusExpired, "system", "order expired")
		// A failed query/cancel decision leaves this row out of the timeout-close
		// count. Continue other expired candidates while recorded failure evidence
		// can control its later retry cooldown.
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

// ShouldRetryTimedOutOrder permits nil orders or no observed cancel-failure
// audit immediately. With that history, updated_at at or before now minus the
// fifteen-minute cooldown qualifies; the store presence boolean supplies no
// separate audit-read error.
func (s *OrderLifecycle) ShouldRetryTimedOutOrder(ctx context.Context, order *Order, now time.Time) bool {
	if order == nil || !s.HasAuditLog(ctx, order.ID, "PAYMENT_CANCEL_FAILED") {
		return true
	}
	return !order.UpdatedAt.After(now.Add(-LifecyclePaymentExpiryRetryDelay))
}

// ReconcileProcessingOrders delegates provider-processing recovery using the
// runtime clock observation. The At method rotates one bounded ID page,
// rereads still-processing rows and applies their queried payment progress or
// stale diagnostic policy.
func (s *OrderLifecycle) ReconcileProcessingOrders(ctx context.Context) (int, error) {
	return s.ReconcileProcessingOrdersAt(ctx, s.runtime.Now())
}

// ReconcileProcessingOrdersAt selects a rotating bounded page from current
// processing IDs, then rereads rows still processing before sequential
// provider queries. Initial ID/page reads abort on errors, and context is
// checked between rows.
//
// Page selection advances its shared cursor before row/provider work succeeds.
// Query errors are logged and can create stale diagnostics; application errors
// are logged and skipped. Paid outcomes increment the count, queried failure
// directly invokes bounded MarkPaymentFailed, and other outcomes can receive a
// stale audit. That direct failure call applies to the resolved provider
// observation independently of HandlePaymentNotification's non-Stripe callback
// gate.
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
		// Keep provider-query failure local to this selected row and consider its
		// stale diagnostic before continuing. The cursor already advanced, so
		// another page can be selected despite this failed observation.
		if queryErr != nil {
			s.runtime.Log("warn", "query processing payment order failed", "orderID", order.ID, "error", queryErr)
			s.MaybeAuditStaleProcessingOrder(ctx, order, now, "query_failed")
			continue
		}
		outcome, applyErr := s.ApplyQueriedPaymentStatus(ctx, order, prov, queryRef, resp)
		// A local payment application error can follow accepted channel progress.
		// Log and continue this page without increasing its recovery count, leaving
		// later webhook/query work to resume the row.
		if applyErr != nil {
			s.runtime.Log("error", "apply processing payment status failed", "orderID", order.ID, "error", applyErr)
			continue
		}
		switch outcome {
		case LifecycleCheckPaidResultAlreadyPaid:
			recovered++
		case LifecycleCheckPaidResultFailed:
			// Terminal provider failure is handled separately from paid counting. A
			// conditional transition error is logged and later rows continue, so this
			// outcome never increments recovered.
			if err := s.MarkPaymentFailed(ctx, order, resp.TradeNo, prov.ProviderKey()); err != nil {
				s.runtime.Log("error", "finalize failed processing payment failed", "orderID", order.ID, "error", err)
			}
		default:
			s.MaybeAuditStaleProcessingOrder(ctx, order, now, resp.Status)
		}
	}
	return recovered, nil
}

// ReconcilePaidFulfillmentOrders delegates unfinished paid fulfillment
// recovery at the runtime clock. The At method selects qualifying stored-time
// delays and one rotating ID page, then retries grant continuation and counts
// rows reread as completed.
func (s *OrderLifecycle) ReconcilePaidFulfillmentOrders(ctx context.Context) (int, error) {
	return s.ReconcilePaidFulfillmentOrdersAt(ctx, s.runtime.Now())
}

// ReconcilePaidFulfillmentOrdersAt selects one rotating page of
// paid-time-bearing recoverable IDs and resumes fulfillment sequentially. The
// store applies retry/stale-lease delays; initial read errors abort and
// context is checked between IDs.
//
// Cursor selection advances before any attempt, so an execution or reread
// error can still leave the next scan on another page. Such per-order errors
// are logged and skipped. A nil executor result is followed by a current row
// read, and only completed state increases the count. Earlier entitlement
// steps can survive a failed attempt under their code/source-order replay
// rules.
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
		// Grant continuation can fail after an earlier entitlement step persisted.
		// Skip this count and continue selected IDs, retaining their
		// order/code/source evidence for another recovery attempt.
		if err := s.ExecuteFulfillment(ctx, orderID); err != nil {
			s.runtime.Log("warn", "retry paid order fulfillment failed", "orderID", orderID, "error", err)
			continue
		}
		order, err := s.store.Order(ctx, orderID)
		// A successful executor result still needs a readable completed row for this
		// count. Discard the reread error after diagnostics and continue other IDs
		// rather than assuming completion.
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

// NextReconcilePageIDs serializes copied page selection with the caller's
// nonnil cursor under reconcileCursorMu. Callers keep the supplied ID slice
// stable while it is read; returned IDs have independent storage.
//
// The cursor increments when len(ids) exceeds limit, before later page reads
// or provider/grant work. With a nonpositive limit, ReconcilePageIDs returns
// nil but that comparison can still advance the cursor; production scans
// supply their positive constants. Shared locking coordinates processing and
// fulfillment cursor observations, while changes to candidate membership
// remain the store/caller's separate concern.
func (s *OrderLifecycle) NextReconcilePageIDs(ids []int64, cursor *uint64, limit int) []int64 {
	// Choose the page and advance its cursor in one critical section shared by
	// recovery scans. Returned ID storage is copied before later reads/work can
	// fail independently of this selection.
	s.reconcileCursorMu.Lock()
	defer s.reconcileCursorMu.Unlock()

	pageIDs := ReconcilePageIDs(ids, *cursor, limit)
	if len(ids) > limit {
		*cursor++
	}
	return pageIDs
}

// ReconcilePageIDs returns an independent bounded ID page selected by cursor
// modulo the ceiling page count. Empty input or nonpositive limit returns nil;
// an input no larger than limit is copied in full.
//
// For a stable larger ID list, increasing cursor rotates through every page,
// including the shorter final page, instead of repeatedly selecting its head.
// The caller advances cursor separately and can do so even if later work
// fails. This helper does not sort, deduplicate or query IDs; store order and
// changing candidate membership determine which observations each page
// contains.
func ReconcilePageIDs(ids []int64, cursor uint64, limit int) []int64 {
	if len(ids) == 0 || limit <= 0 {
		return nil
	}
	if len(ids) <= limit {
		return append([]int64(nil), ids...)
	}

	// Compute ceiling page count so the final partial page participates in
	// rotation for a stable ID set. The cursor owner advances before later
	// processing, and candidate changes can alter page membership.
	pageCount := (len(ids) + limit - 1) / limit
	page := int(cursor % uint64(pageCount))
	start := page * limit
	end := min(start+limit, len(ids))
	return append([]int64(nil), ids[start:end]...)
}

// MaybeAuditStaleProcessingOrder attempts a diagnostic action for a nonnil
// order whose updated_at is at least twenty-four hours old and has no observed
// stale audit. Nil/recent/already-observed inputs return; boolean read errors
// and runtime audit persistence failures are not separately returned.
func (s *OrderLifecycle) MaybeAuditStaleProcessingOrder(ctx context.Context, order *Order, now time.Time, providerStatus string) {
	if order == nil || order.UpdatedAt.After(now.Add(-LifecycleProcessingStaleAfter)) || s.HasAuditLog(ctx, order.ID, "PAYMENT_PROCESSING_STALE") {
		return
	}
	s.runtime.Audit(ctx, order.ID, "PAYMENT_PROCESSING_STALE", "system", map[string]any{
		"processing_since": order.UpdatedAt,
		"provider_status":  providerStatus,
	})
}
