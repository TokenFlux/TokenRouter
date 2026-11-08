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

// fulfillment.go confirms provider payment observations and coordinates
// repeatable balance or subscription delivery.
// Verified provider progress can arrive repeatedly or after a local pending
// order was closed. Notification handling first resolves merchant identity and
// checks saved provider metadata and EasyPay/EPUSDT EPay transaction identity, then
// conditionally records payment state and resumes entitlement work. Only Stripe failure
// notifications select the terminal payment-failure transition; other failure
// callbacks are ignored before lookup.
//
// Fulfillment claims paid/failed or stale recharging state and uses the reread
// stored timestamp to condition final completion/failure. That reread checks
// recharging state rather than comparing the originally requested claim time.
// Balance code grants, source-order subscriptions, rebates and completion are
// separate operations, so retries recognize completed grant evidence before
// finishing later steps. Notifications are scheduled after completion with a
// cloned order and independent timed context; audit presence influences
// sending, while audit-read errors are not exposed separately by the store
// boolean.

// Redeem actions choose missing-code creation, unused-code redemption or
// used-code continuation in balance fulfillment. FulfillmentLeaseDuration
// supplies the stale recharging takeover age for timestamp-conditioned
// finalization.
const (
	// RedeemActionCreate selects code creation before redemption when lookup
	// returned nil or any error.
	RedeemActionCreate RedeemAction = iota
	// RedeemActionRedeem selects redemption of the returned code without checking
	// its expiry or buyer ownership here.
	RedeemActionRedeem
	// RedeemActionSkipCompleted skips another redemption when the returned code
	// reports used, then continues rebate/completion work.
	RedeemActionSkipCompleted

	// FulfillmentLeaseDuration is the five-minute stale recharging age used by
	// conditional claim, independently of notification timing or provider state.
	FulfillmentLeaseDuration = 5 * time.Minute
)

// ErrOrderNotFound classifies a callback merchant/legacy ID whose permitted
// lookups report absence. HTTP webhook handling acknowledges this classified
// result after verification to stop its ordinary missing-order retry path.
var ErrOrderNotFound = errors.New("payment order not found")

// RedeemAction is the code-lookup decision used before balance entitlement
// continuation.
type RedeemAction int

// HandlePaymentNotification handles accepted success, paid, processing or
// Stripe failure observations after order/provider checks. Nil notifications,
// unknown states and non-Stripe failed status return nil before lookup.
//
// The accepted provider key comes from the caller's verifier/query; merchant
// lookup does not replace signature validation. Exact order number wins over
// supported legacy numeric identity, and applicable saved metadata is checked
// before state changes. Success validates reported money then resumes
// fulfillment, processing updates only pending, and failure uses its bounded
// conditional transition. Replayed or closed local states follow those
// downstream rules rather than creating another order.
func (s *Fulfillment) HandlePaymentNotification(ctx context.Context, n *PaymentNotification, pk string) error {
	if n == nil {
		return nil
	}
	// Non-Stripe failed callbacks are ignored before merchant lookup. Stripe
	// checkout failures use the terminal progress path, while accepted
	// success/processing reports still follow normal saved identity checks.
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

// FindPaymentNotificationOrder reads the exact supplied merchant number before
// considering a positive sub2_ numeric local ID. Storage errors other than
// classified absence return with context, and an exact hit returns
// immediately.
//
// Only a missed exact lookup enables the legacy form; parsing accepts the
// int64 syntax used by ParseLegacyPaymentOrderID and does not expand tr_
// numbers. A missing legacy row also becomes wrapped ErrOrderNotFound, while
// other numeric-row read errors remain visible. That distinction lets HTTP
// acknowledge unavailable local identity without treating a failed database
// read as a terminal missing-order callback.
func (s *Fulfillment) FindPaymentNotificationOrder(ctx context.Context, orderID string) (*Order, error) {
	// Try the complete merchant number before numeric compatibility so a
	// numeric-looking stored number cannot be reassociated with another local ID.
	// Only classified absence permits that second lookup.
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

// ParseLegacyPaymentOrderID returns a positive int64 suffix from trimmed sub2_
// text only when notFound permits compatibility lookup. Other prefixes,
// empty/malformed or nonpositive values return false; exact merchant lookup
// therefore remains authoritative even for a numeric-looking suffix.
func ParseLegacyPaymentOrderID(orderID string, notFound bool) (int64, bool) {
	// Only the retained sub2_ compatibility form can name a numeric row after an
	// exact lookup misses. Current tr_ merchant numbers remain exact identities
	// instead of expanding into local IDs.
	const legacyOrderIDPrefix = "sub2_"
	if !notFound {
		return 0, false
	}
	orderID = strings.TrimSpace(orderID)
	if !strings.HasPrefix(orderID, legacyOrderIDPrefix) {
		return 0, false
	}
	trimmed := strings.TrimPrefix(orderID, legacyOrderIDPrefix)
	if trimmed == "" || trimmed == orderID {
		return 0, false
	}
	oid, err := strconv.ParseInt(trimmed, 10, 64)
	if err != nil || oid <= 0 {
		return 0, false
	}
	return oid, true
}

// ValidatePaymentNotificationOrder checks expected provider key and applicable
// saved merchant metadata for a nonnil order. Binding lookup errors are
// suppressed, leaving the key helper's snapshot/column/registry choices
// available.
//
// Provider comparison runs only when both expected and actual key text are
// nonblank, using case-insensitive trimmed actual text. A mismatch records an
// ordinary audit before returning. Snapshot metadata validation separately has
// its own absent/unrecognized-key rules and is not implied by a successful key
// comparison. The caller retains signature verification ownership before this
// order association stage. EasyPay and EPUSDT EPay observations must retain a saved
// transaction number so another external payment cannot replace that binding.
func (s *Fulfillment) ValidatePaymentNotificationOrder(ctx context.Context, o *Order, pk, tradeNo string, metadata map[string]string) error {
	if err := validatePaymentTradeNumber(o, pk, tradeNo); err != nil {
		return err
	}
	instanceProviderKey := ""
	// Discard instance-resolution errors for expected-key selection and keep the
	// other saved/registry candidates available. Applicable snapshot metadata
	// still has a separate validation stage after that key comparison.
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

// validatePaymentTradeNumber checks EasyPay and EPUSDT EPay observations against a
// previously stored nonempty reference. Other adapters retain their own
// reference rules, including Stripe's session, invoice and intent transitions.
func validatePaymentTradeNumber(order *Order, providerKey, tradeNo string) error {
	if (GetBasePaymentType(providerKey) == TypeEasyPay || providerKey == TypeEPUSDT) && order.PaymentTradeNo != "" &&
		order.PaymentTradeNo != tradeNo {
		return fmt.Errorf("%s transaction number conflicts with the stored order", providerKey)
	}
	return nil
}

// ConfirmPayment requires finite positive reported gateway money and compares
// it with the nonnil order's stored PayAmount using its currency tolerance.
// Invalid or excessive difference records an audit and returns before state
// transition.
//
// The stored expected amount is assumed usable; this comparison does not
// independently reject a nonfinite stored value. An accepted report delegates
// ToPaid, which records the local observation time rather than consuming
// notification PaidAt text. Later fulfillment can recognize previously granted
// rights, so accepting money does not itself establish that every entitlement
// step is already complete.
func (s *Fulfillment) ConfirmPayment(ctx context.Context, o *Order, tradeNo string, paid float64, pk string, metadata map[string]string) error {
	if !IsValidProviderAmount(paid) {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_INVALID_AMOUNT", pk, map[string]any{
			"expected": o.PayAmount,
			"paid":     paid,
			"tradeNo":  tradeNo,
		})
		return fmt.Errorf("invalid paid amount from provider: %v", paid)
	}
	// Compare the reported gateway quantity at the order currency's tolerance
	// after checking the report is finite positive. That tolerance comes from
	// currency precision and does not validate the stored expected amount.
	if math.Abs(paid-o.PayAmount) > PaymentAmountToleranceForCurrency(PaymentOrderCurrency(o)) {
		s.runtime.Audit(ctx, o.ID, "PAYMENT_AMOUNT_MISMATCH", pk, map[string]any{"expected": o.PayAmount, "paid": paid, "tradeNo": tradeNo})
		return fmt.Errorf("amount mismatch: expected %s, got %s", strconv.FormatFloat(o.PayAmount, 'f', -1, 64), strconv.FormatFloat(paid, 'f', -1, 64))
	}
	return s.ToPaid(ctx, o, tradeNo, paid, pk, metadata)
}

// MarkPaymentProcessing conditionally changes the observed pending order to
// processing, clearing prior failure fields and applying available invoice
// metadata. Other observed states return nil without persistence.
//
// For Stripe, a nonblank cs_-prefixed checkout_session_id replaces the general
// trade reference so later processing queries address the hosted session. The
// store rechecks pending at update time; zero affected rows produces no
// processing audit, while success records an ordinary observation. No
// fulfillment grant or paid timestamp is performed by this progress step.
func (s *Fulfillment) MarkPaymentProcessing(ctx context.Context, o *Order, tradeNo, pk string, metadata map[string]string) error {
	if o.Status != OrderStatusPending {
		return nil
	}

	// Keep Stripe processing associated with its cs_ checkout session when
	// metadata supplies one. Query recovery needs that hosted-session identity
	// before a final payment-intent or invoice reference exists.
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

// MarkPaymentFailed attempts up to three conditional pending/processing
// transitions to expired with failure time/reason. It supplies a nonnil
// observed order and rereads after a zero affected count.
//
// A status outside those accepted states returns nil, so later
// completion/refund work is not overwritten by this payment-failure path.
// Successful update records PAYMENT_FAILED, storage/read errors return, and
// repeated races exhaust with an error. HandlePaymentNotification invokes this
// for Stripe failed observations, while direct callers remain responsible for
// choosing a valid reported failure.
func (s *Fulfillment) MarkPaymentFailed(ctx context.Context, o *Order, tradeNo, pk string) error {
	// Retry at most three observed status races, rereading only after a zero
	// affected count. This lets a concurrent terminal change end failure
	// recording before another pending/processing write is attempted.
	for range 3 {
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

// IsValidProviderAmount reports finite strictly positive gateway quantities
// for payment confirmation.
func IsValidProviderAmount(amount float64) bool {
	return amount > 0 && !math.IsNaN(amount) && !math.IsInf(amount, 0)
}

// ValidateProviderNotificationMetadata delegates saved merchant comparison to
// ValidateProviderSnapshotMetadata. Its nil/absent metadata and recognized-key
// rules remain with that helper; callers verify the provider observation
// before this association check.
func ValidateProviderNotificationMetadata(order *Order, providerKey string, metadata map[string]string) error {
	return ValidateProviderSnapshotMetadata(order, providerKey, metadata)
}

// ExpectedNotificationProviderKey selects nonblank instance key before saved
// order key, then registry method key and method text. It trims candidates and
// tolerates nil registry for notification provider comparison.
//
// Resolved instance identity is preferred to the supplied saved field, while a
// saved key wins before current method registration is considered. This helper
// does not validate existence or merchant metadata itself. Its order-aware
// caller supplies snapshot/column precedence separately, and the final method
// text remains a comparison candidate when no more specific identity is
// available.
func ExpectedNotificationProviderKey(registry *Registry, orderPaymentType string, orderProviderKey string, instanceProviderKey string) string {
	if key := strings.TrimSpace(instanceProviderKey); key != "" {
		return key
	}
	if key := strings.TrimSpace(orderProviderKey); key != "" {
		return key
	}
	if registry != nil {
		if key := strings.TrimSpace(registry.GetProviderKey(orderPaymentType)); key != "" {
			return key
		}
	}
	return strings.TrimSpace(orderPaymentType)
}

// ToPaid conditionally records accepted payment and resumes fulfillment for a
// nonnil order, trying at most five status observations. Callers validate
// reported money and applicable merchant identity before invoking it.
//
// Before retries, Stripe invoice progress retains the supplied observation's
// pi_ reference when incoming trade is in_. Pending, processing, expired and
// cancelled can transition to paid with the local observation time and cleared
// failure fields; zero affected count rereads before another attempt. Failed,
// paid and recharging delegate fulfillment directly, so that branch does not
// rewrite payment time/trade. Completed/refunded and other unhandled states
// return nil. Grants still use their own replay and completion conditions.
// EasyPay transaction identity is checked again after each order reread, so a
// concurrent paid transition cannot make a conflicting reference acceptable.
func (s *Fulfillment) ToPaid(ctx context.Context, o *Order, tradeNo string, paid float64, pk string, metadata map[string]string) error {
	// Keep the supplied observation's pi_ reference when the incoming Stripe
	// identifier is an invoice, because refund queries need the payment intent.
	// This preference runs before status retries rather than after each reread.
	if GetBasePaymentType(pk) == TypeStripe &&
		strings.HasPrefix(strings.TrimSpace(tradeNo), "in_") &&
		strings.HasPrefix(strings.TrimSpace(o.PaymentTradeNo), "pi_") {
		tradeNo = o.PaymentTradeNo
	}
	// Retry up to five conditional payment-state observations before reporting
	// repeated races. Closed expired/cancelled rows remain eligible for verified
	// late success, while already paid/granted states follow their continuation
	// path.
	for range 5 {
		if err := validatePaymentTradeNumber(o, pk, tradeNo); err != nil {
			return err
		}
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
			// The expected status lost its update race. Reread current state before
			// another attempt so completion/refund or another accepted progress
			// decision can win without overwriting that newer observation.
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

// ExecuteFulfillment reads the numeric order and delegates subscription
// fulfillment only for subscription order type. Other types use balance
// fulfillment; initial read errors retain context, and the selected executor
// rereads current state before claiming.
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

// ExecuteBalanceFulfillment rereads order state and runs the balance grant
// sequence under a stored-time fulfillment lease. Any read error becomes
// NOT_FOUND; completed returns nil, and refund/unaccepted states reject the
// attempt.
//
// Paid, failed or recharging can attempt claim; nil lease means a completed
// observation after a lost claim. DoBalance performs separate redeem, rebate
// and completion operations, so its failure can follow an already persisted
// grant. That error triggers best-effort lease-conditioned MarkFailed and is
// returned unchanged. This executor does not itself require PaidAt, unlike
// administrator RetryFulfillment.
func (s *Fulfillment) ExecuteBalanceFulfillment(ctx context.Context, oid int64) error {
	o, err := s.loadFulfillableOrder(ctx, oid)
	if err != nil {
		return err
	}
	if o == nil {
		return nil
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

// AcquirePaymentFulfillmentLease conditionally claims a nonnil paid/failed or
// stale recharging order and returns its reread stored timestamp. Claim time
// is UTC truncated to microseconds, with stale takeover age from
// FulfillmentLeaseDuration.
//
// Zero affected count rereads: completed yields nil,nil, while recharging or
// other changed state returns conflict. A successful update also rereads and
// requires recharging, using the returned database time for later
// completion/failure equality. It does not compare that reread time to the
// originally requested claim time, so the lease protects those final writes
// through its observed version rather than proving exclusive ownership of
// every intervening entitlement call.
func (s *Fulfillment) AcquirePaymentFulfillmentLease(ctx context.Context, o *Order) (*FulfillmentLease, error) {
	if o == nil {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "nil payment order")
	}

	// Use UTC microseconds for the proposed claim timestamp so database precision
	// can represent it. Reread the actual stored version after claim before
	// conditioning later completion/failure updates.
	now := s.runtime.Now().UTC().Truncate(time.Microsecond)
	staleBefore := now.Add(-FulfillmentLeaseDuration)
	updated, err := s.store.ClaimFulfillment(ctx, o.ID, now, staleBefore)
	if err != nil {
		return nil, fmt.Errorf("acquire fulfillment lease: %w", err)
	}
	// A claim affected no row, so do not start entitlement work from the stale
	// observation. Reread completion or active processing to choose the nil
	// result or conflict.
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

	// Use the reread persisted timestamp for final-write equality and require
	// recharging state. This checks the returned state but does not compare its
	// time to the originally requested claim timestamp.
	claimed, err := s.store.Order(ctx, o.ID)
	if err != nil {
		return nil, fmt.Errorf("reload acquired fulfillment lease: %w", err)
	}
	if claimed.Status != OrderStatusRecharging {
		return nil, infraerrors.Conflict("CONFLICT", "fulfillment lease was lost")
	}
	return &FulfillmentLease{Version: claimed.UpdatedAt}, nil
}

// ResolveRedeemAction selects code creation for nil result or any lookup
// error, including an error accompanying an existing code. A used code selects
// completed-grant continuation, while other returned codes select redemption;
// expiry and buyer ownership are not checked by this helper.
func ResolveRedeemAction(existing *billing.RedeemCode, lookupErr error) RedeemAction {
	if existing == nil || lookupErr != nil {
		return RedeemActionCreate
	}
	if existing.IsUsed() {
		return RedeemActionSkipCompleted
	}
	return RedeemActionRedeem
}

// DoBalance resumes order-owned balance delivery from code lookup, then
// applies order rebate and lease-conditioned completion. Supply a nonnil
// order/lease and usable redeemer; lookup errors select creation and later
// creation failures remain visible.
//
// A code reporting used skips another redemption but still runs remaining
// rebate/completion, without checking its recorded buyer here. Missing code is
// created with stored Amount; an existing unused code is redeemed under
// ContextSkipRedeemAffiliate so the separate order rebate owns that promotion
// step. These operations do not share one enclosing transaction, so a later
// error can leave the grant available for the next attempt to recognize.
func (s *Fulfillment) DoBalance(ctx context.Context, o *Order, lease *FulfillmentLease) error {
	// A prior grant step can survive a later rebate/completion failure. Read its
	// code status before choosing create, redeem or used-code continuation,
	// retaining creation errors when the lookup itself failed.
	existing, lookupErr := s.redeemService.GetByCode(ctx, o.RechargeCode)
	action := ResolveRedeemAction(existing, lookupErr)

	switch action {
	case RedeemActionSkipCompleted:
		if err := s.ApplyAffiliateRebateForOrder(ctx, o); err != nil {
			return err
		}
		// The lookup code reports used, so skip another redemption and finish
		// remaining completion after rebate. This decision does not independently
		// verify which buyer consumed that code.
		return s.MarkCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
	case RedeemActionCreate:
		rc := &billing.RedeemCode{Code: o.RechargeCode, Type: billing.RedeemTypeBalance, Value: o.Amount, Status: billing.StatusUnused}
		if err := s.redeemService.CreateCode(ctx, rc); err != nil {
			return fmt.Errorf("create redeem code: %w", err)
		}
	case RedeemActionRedeem:
	}
	// Redeem with billing's affiliate step suppressed, then apply the separately
	// claimed order rebate before completion. A later failure leaves the already
	// persisted code usage for the next fulfillment attempt.
	if _, err := s.redeemService.Redeem(billing.ContextSkipRedeemAffiliate(ctx), o.UserID, o.RechargeCode); err != nil {
		return fmt.Errorf("redeem balance: %w", err)
	}
	if err := s.ApplyAffiliateRebateForOrder(ctx, o); err != nil {
		return err
	}
	return s.MarkCompleted(ctx, o, lease, "RECHARGE_SUCCESS")
}

// MarkCompleted writes completion only for recharging state with the supplied
// lease version, then considers audit and notification. A missing lease is an
// error; a nonnil order and usable runtime callbacks are required.
//
// A zero update rereads and returns nil if already completed, otherwise
// conflict even when that read failed. Successful update checks audit
// presence; an absent signal records the supplied success action and schedules
// notification. The boolean has no separate read-error result, and
// notification failure occurs after completion remains stored. This method
// does not combine earlier grant/rebate operations with its final state update
// or guarantee one delivery attempt across every audit failure.
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
	// A lost completion condition can already represent finished work. Accept a
	// successful reread showing completed; failed or other-state rereads become
	// conflict and do not trigger another notification here.
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

// DispatchPaymentFulfillmentNotification clones a nonnil order observation and
// schedules supported success email through the injected Background callback.
// MarkCompleted calls it after persistence; this method does not independently
// check order status. Nil service, absent Notify or nil order skips dispatch,
// and configured Notify requires a usable scheduler.
//
// The scheduled callback uses a fresh Background context with thirty-second
// timeout, independent of the original payment request cancellation. It
// selects balance/subscription notice from audit action and logs returned
// sender errors. Along the completion caller path, dispatch errors do not
// compensate persisted completion or grants. Clone's supported-copy rules
// isolate captured values while unknown mutable shapes retain its documented
// limitations.
func (s *Fulfillment) DispatchPaymentFulfillmentNotification(o *Order, auditAction string) {
	if s == nil || s.runtime.Notify == nil || o == nil {
		return
	}
	// Detach supported mutable order data before asynchronous scheduling so later
	// caller mutations do not change those captured fields. The worker uses
	// independent notification context after completion is persisted.
	o = o.Clone()
	s.runtime.Background("service/payment_fulfillment.go:dispatchPaymentFulfillmentNotification", func() {
		// Give notification its own thirty-second Background budget after payment
		// completion. Deferred cancellation releases that child even when action
		// selection or sending returns early, independently of the original request.
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

// SendBalanceRechargeSuccessNotification builds a balance success notice from
// a nonnil order and calls Notify using the supplied context. A
// missing/failed/nil user read leaves current_balance empty; credited amount
// and a successful balance observation are displayed to two decimals, and
// saved display_locale supplies language.
func (s *Fulfillment) SendBalanceRechargeSuccessNotification(ctx context.Context, o *Order) error {
	currentBalance := ""
	if s.runtime.User != nil {
		// A failed or nil current-user observation leaves this enrichment empty
		// while the notice still uses stored recipient/order data. Successful
		// balance display uses two decimals.
		if user, err := s.runtime.User(ctx, o.UserID); err == nil && user != nil {
			// Display the current balance with two fractional digits.
			currentBalance = fmt.Sprintf("%.2f", user.Balance)
		}
	}
	language, _ := o.ProviderSnapshot["display_locale"].(string)
	return s.runtime.Notify(ctx, PaymentNotice{
		Locale:         language,
		Event:          "balance.recharge_success",
		RecipientEmail: o.UserEmail,
		RecipientName:  FirstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables: map[string]string{
			// Display credited recharge quantity with two fractional digits.
			"recharge_amount": fmt.Sprintf("%.2f", o.Amount),
			"current_balance": currentBalance,
			"order_id":        strconv.FormatInt(o.ID, 10),
		},
	})
}

// SendSubscriptionPurchaseSuccessNotification builds a subscription success
// notice from the nonnil order snapshot and optional active-grant read. It
// requires Notify; absent/failed/nil subscription lookup leaves expiry empty,
// while positive snapshot validity supplies its recorded day quantity and
// saved display locale supplies language.
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
			// Subscription enrichment is optional after committed grant work. Suppress
			// missing/failed/nil active-grant reads into empty expiry while still
			// sending the stored purchase notice.
			if sub, err := s.subscriptionSvc.GetActiveSubscription(ctx, o.UserID, *o.PlanID); err == nil && sub != nil {
				// Display grant expiration only through minute precision here.
				variables["expiry_time"] = sub.ExpiresAt.Format("2006-01-02 15:04")
			}
		}
	}
	language, _ := o.ProviderSnapshot["display_locale"].(string)
	return s.runtime.Notify(ctx, PaymentNotice{
		Locale:         language,
		Event:          "subscription.purchase_success",
		RecipientEmail: o.UserEmail,
		RecipientName:  FirstNonEmpty(o.UserName, o.UserEmail),
		UserID:         o.UserID,
		SourceType:     "payment_order",
		SourceID:       strconv.FormatInt(o.ID, 10),
		Variables:      variables,
	})
}

// ExecuteSubscriptionFulfillment rereads the order and grants subscription
// work under a stored-time fulfillment lease. Any order read failure becomes
// NOT_FOUND, completed returns nil, and refund/unaccepted state or missing
// positive plan rejects the attempt.
//
// Paid, failed or recharging can claim; nil lease means current completion
// after a lost claim. DoSub can persist a grant before rebate/completion
// error, so failure invokes best-effort lease-conditioned MarkFailed and
// retains the original error. This executor does not independently require
// PaidAt, while administrator retry has that additional eligibility predicate.
func (s *Fulfillment) ExecuteSubscriptionFulfillment(ctx context.Context, oid int64) error {
	o, err := s.loadFulfillableOrder(ctx, oid)
	if err != nil {
		return err
	}
	if o == nil {
		return nil
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

// loadFulfillableOrder reads the order and admits paid, failed, or recharging
// state for either fulfillment entry. A nil order with no error means completed
// work; callers return without claiming a lease. Any read error becomes NOT_FOUND,
// and refund or other unaccepted states return INVALID_STATUS. Subscription
// fulfillment validates its plan after this shared status check.
func (s *Fulfillment) loadFulfillableOrder(ctx context.Context, oid int64) (*Order, error) {
	o, err := s.store.Order(ctx, oid)
	if err != nil {
		// Preserve the fulfillment entries' missing-order response for every read
		// failure, including storage outages; callers do not expose the underlying
		// lookup error.
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if o.Status == OrderStatusCompleted {
		return nil, nil
	}
	if IsRefundStatus(o.Status) {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "refund-related order cannot fulfill")
	}
	if o.Status != OrderStatusPaid && o.Status != OrderStatusFailed && o.Status != OrderStatusRecharging {
		return nil, infraerrors.BadRequest("INVALID_STATUS", "order cannot fulfill in status "+o.Status)
	}
	return o, nil
}

// DoSub resumes the nonnil order's positive-plan subscription, then order
// rebate and lease-conditioned completion. Supply a usable lease for
// finalization; missing plan/service returns an error, and audit presence can
// skip another assignment attempt.
//
// Without that audit signal, billing receives SourceOrderID for its persistent
// replay rules and any recorded positive duration or quota template.
// Assignment completes before its ordinary SUBSCRIPTION_ASSIGNED audit, which
// can fail silently through the injected callback. Billing's source-order
// handling remains the replay mechanism when audit evidence is unavailable.
// Rebate and final completion then run separately, allowing a later attempt to
// finish work after an earlier grant.
func (s *Fulfillment) DoSub(ctx context.Context, o *Order, lease *FulfillmentLease) error {
	if o.PlanID == nil || *o.PlanID <= 0 {
		return fmt.Errorf("order %d missing plan id", o.ID)
	}
	if s.subscriptionSvc == nil {
		return errors.New("subscription service is unavailable")
	}
	// Audit presence can recognize an earlier assignment, but its read errors are
	// not exposed. Billing also receives SourceOrderID when assignment runs so
	// replay does not depend solely on this observation.
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
		// Persist billing assignment before its ordinary audit, rebate and
		// completion steps. A later failure can therefore leave the source-order
		// grant available for billing to recognize on another attempt.
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

// HasAuditLog delegates order-action presence to the store's boolean
// observation. PostgreSQL discards count errors and tests the returned count;
// callers get no separate error to distinguish absence from an unsuccessful
// history read.
func (s *Fulfillment) HasAuditLog(ctx context.Context, id int64, action string) bool {
	return s.store.HasAudit(ctx, id, action)
}

// ApplyAffiliateRebateForOrder delegates one order rebate only when a usable
// positive point base and enabled runtime callback qualify. Nil/invalid base
// or absent/disabled callback returns nil; the store owns the transaction
// joining promotion participation and its replay claim.
func (s *Fulfillment) ApplyAffiliateRebateForOrder(ctx context.Context, o *Order) error {
	base := AffiliateRebateBasePoints(o)
	if o == nil || base <= 0 || s.runtime.RebateEnabled == nil || !s.runtime.RebateEnabled(ctx) {
		return nil
	}
	return s.store.ApplyOrderRebate(ctx, o, base)
}

// AffiliateRebateBasePoints returns the purchased reasoning-point base or zero
// when the order helper rejects it. OrderPurchasedReasoningPoints uses its
// stored balance amount or selected positive finite quota according to
// purchase type.
func AffiliateRebateBasePoints(o *Order) float64 {
	points, ok := OrderPurchasedReasoningPoints(o)
	if !ok {
		return 0
	}
	return points
}

// MarkFailed attempts a best-effort failure update for recharging state with
// the supplied stored lease version. Missing lease logs and returns;
// OrderErrorMessage uses cause.Error() or empty text for nil.
//
// The conditional version prevents this write from changing a row whose
// status/time has advanced. Persistence errors are logged and not returned to
// the grant caller, while positive affected count records FULFILLMENT_FAILED.
// Earlier grant/rebate operations remain independent, so marking failure
// preserves their recovery evidence rather than undoing entitlement work. The
// original executor error remains the caller's result.
func (s *Fulfillment) MarkFailed(ctx context.Context, oid int64, lease *FulfillmentLease, cause error) {
	if lease == nil {
		s.runtime.Log("error", "mark FAILED without fulfillment lease", "orderID", oid)
		return
	}
	now := s.runtime.Now()
	r := OrderErrorMessage(cause)
	// Condition failure on the reread lease timestamp and recharging state so a
	// mismatched stored version or state rejects this write. Prior entitlement
	// operations remain persisted independently of this best-effort failure
	// observation.
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

// RetryFulfillment validates administrator recovery eligibility before
// recording a retry observation and delegating current fulfillment. Any read
// error becomes NOT_FOUND; PaidAt is required, refund/completed states reject,
// and only failed, paid or recharging are accepted. This adds a paid-time
// predicate beyond the executors' claim paths.
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
