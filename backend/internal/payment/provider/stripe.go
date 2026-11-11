package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/stripe/stripe-go/v85"
	"github.com/stripe/stripe-go/v85/webhook"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// stripe.go creates hosted Stripe checkout sessions and resolves payment,
// refund, notification, and document references.
// Stripe checkout creates a customer before a hosted session and carries the
// merchant order through metadata on customer, invoice and payment intent. The
// two creations use separate order-derived idempotency keys, so a session
// failure can leave the customer already created. Local order and response
// persistence remain with the checkout workflow.
//
// Stored trade references can identify a session, invoice or payment intent.
// Queries translate those representations while combining expanded channel
// states, whereas signed events select actionable outcomes and ignore
// retryable payment-attempt failures. Refund creation resolves an intent and
// distinguishes pending, failed, and unknown outcomes. Refund recovery uses
// the returned refund identity or a uniquely matching PaymentIntent refund. Invoice documents take precedence according to
// the supplied reference, with charge receipts used when a session has no
// usable invoice link or for intent-based retrieval.

// Stripe event labels select actionable payment observations and distinguish
// retryable attempt failures from session or invoice outcomes.
const (
	// successful payment intent event.
	stripeEventPaymentSuccess = "payment_intent.succeeded"
	// ignored retryable intent failure.
	stripeEventPaymentFailed = "payment_intent.payment_failed"
	// completed checkout state event.
	stripeEventCheckoutDone = "checkout.session.completed"
	// failed expired checkout event.
	stripeEventCheckoutExpired = "checkout.session.expired"
	// successful asynchronous checkout event.
	stripeEventCheckoutAsyncSucceeded = "checkout.session.async_payment_succeeded"
	// failed asynchronous checkout event.
	stripeEventCheckoutAsyncFailed = "checkout.session.async_payment_failed"
	// successful paid invoice event.
	stripeEventInvoicePaid = "invoice.paid"
	// ignored retryable invoice failure.
	stripeEventInvoiceFailed = "invoice.payment_failed"
)

// Stripe compile-time assignments require checkout, cancellation, document and
// merchant-metadata capabilities used by the payment registry and workflows.
var (
	_ payment.Provider                 = (*Stripe)(nil)
	_ payment.CancelableProvider       = (*Stripe)(nil)
	_ payment.DocumentProvider         = (*Stripe)(nil)
	_ payment.MerchantIdentityProvider = (*Stripe)(nil)
)

// Stripe implements hosted checkout, identity translation, refund recovery and
// document retrieval for one instance.
type Stripe struct {
	instanceID string // configured provider instance identity
	// config owns a constructor copy read as immutable by SDK initialization,
	// webhook verification and currency helpers.
	config map[string]string

	// mu protects initialized and sc during one-time SDK client publication;
	// individual API operations run after this initialization lock is released.
	mu          sync.Mutex     // client publication serialization lock
	initialized bool           // SDK client publication flag
	sc          *stripe.Client // published Stripe SDK client
}

// NewStripe requires a literally nonempty secret key, copies string
// configuration and normalizes currency before returning an uninitialized
// adapter. SDK client construction is lazy, and webhook secret presence is
// checked during notification verification.
func NewStripe(instanceID string, config map[string]string) (*Stripe, error) {
	if config["secretKey"] == "" {
		return nil, fmt.Errorf("stripe config missing required key: secretKey")
	}
	cfg := cloneStringMap(config)
	currency, err := payment.NormalizePaymentCurrency(cfg["currency"])
	if err != nil {
		return nil, fmt.Errorf("stripe config currency: %w", err)
	}
	cfg["currency"] = currency
	return &Stripe{
		instanceID: instanceID,
		config:     cfg,
	}, nil
}

// ensureInit publishes one SDK client under the instance mutex and records
// that initialization is complete. Client construction precedes setting
// initialized, so later invocations skip construction after sc has been
// assigned.
//
// The mutex covers this publication sequence, while individual API requests
// run after it is released and receive their own caller contexts.
// Configuration is a constructor-owned string map read as immutable, and
// client initialization itself passes no request context or payment data to
// the SDK constructor.
func (s *Stripe) ensureInit() {
	// Build sc before setting initialized while holding one mutex, preventing
	// another initializer from skipping construction before its client pointer is
	// published.
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.initialized {
		s.sc = stripe.NewClient(s.config["secretKey"])
		s.initialized = true
	}
}

// Name returns the fixed display label for this Stripe adapter.
func (s *Stripe) Name() string { return "Stripe" }

// ProviderKey returns the registry key identifying Stripe provider instances.
func (s *Stripe) ProviderKey() string { return payment.TypeStripe }

// SupportedTypes returns a fresh list containing the single Stripe checkout
// method.
func (s *Stripe) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeStripe}
}

// MerchantIdentityMetadata returns configured currency in a fresh map, or nil
// for a nil adapter. It supplies currency confirmation metadata rather than a
// Stripe account identifier for merchant-account comparison.
func (s *Stripe) MerchantIdentityMetadata() map[string]string {
	if s == nil {
		return nil
	}
	return map[string]string{"currency": s.currency()}
}

// currency returns normalized configured currency, selecting the payment
// default for a nil adapter or invalid setting. NewStripe normally validates
// configuration first, while direct fixtures can still exercise this read-time
// default.
func (s *Stripe) currency() string {
	if s == nil {
		return payment.DefaultPaymentCurrency
	}
	currency, err := payment.NormalizePaymentCurrency(s.config["currency"])
	// Use the payment default for an invalid direct-fixture setting, allowing
	// merchant metadata and amount conversion callers to receive a currency.
	if err != nil {
		return payment.DefaultPaymentCurrency
	}
	return currency
}

// CreatePayment converts money for configured currency, requires a nonblank
// return destination and creates a customer followed by a hosted checkout
// session. Customer and session calls use separate order-derived idempotency
// keys, and ctx controls both SDK requests.
//
// A failed session creation leaves the successful customer call uncompensated
// here. Normalized billing scalars are copied, while its shared address can be
// trimmed in place. The returned session ID is the initial trade reference,
// with any expanded invoice document and expiry attached for checkout
// persistence; subsequent observations can replace that trade reference with
// its payment intent.
func (s *Stripe) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	s.ensureInit()

	currency := s.currency()
	amountInMinorUnit, err := payment.AmountToMinorUnit(req.Amount, currency)
	if err != nil {
		return nil, fmt.Errorf("stripe create payment: %w", err)
	}
	if strings.TrimSpace(req.ReturnURL) == "" {
		return nil, fmt.Errorf("stripe checkout requires return_url")
	}

	billing := normalizeStripeBillingInfo(req)
	customerParams := buildStripeCustomerCreateParams(req, billing, s.instanceID)
	// Customer and session calls have separate order keys; a later session
	// failure leaves this customer creation available to a retry.
	customerParams.SetIdempotencyKey(fmt.Sprintf("cus-%s", req.OrderID))
	customer, err := s.sc.V1Customers.Create(ctx, customerParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create customer: %w", err)
	}

	checkoutParams := buildStripeCheckoutSessionCreateParams(customer.ID, req, amountInMinorUnit, s.instanceID, currency)
	// Associate session retries with the merchant order independently of customer
	// creation; local response persistence happens after the returned session.
	checkoutParams.SetIdempotencyKey(fmt.Sprintf("cs-%s", req.OrderID))
	checkoutSession, err := s.sc.V1CheckoutSessions.Create(ctx, checkoutParams)
	if err != nil {
		return nil, fmt.Errorf("stripe create checkout session: %w", err)
	}
	doc := stripeCheckoutInvoiceDocument(checkoutSession)

	return &payment.CreatePaymentResponse{
		TradeNo:       strings.TrimSpace(checkoutSession.ID),
		PayURL:        strings.TrimSpace(checkoutSession.URL),
		CustomerID:    customer.ID,
		InvoiceID:     doc.InvoiceID,
		InvoiceURL:    doc.HostedInvoiceURL,
		InvoicePDF:    doc.InvoicePDF,
		InvoiceStatus: doc.InvoiceStatus,
		Currency:      currency,
		ExpiresAt:     stripeCheckoutSessionExpiresAt(checkoutSession),
	}, nil
}

// buildStripeCheckoutSessionCreateParams assembles a payment-mode session for
// the already created customer and one quantity-one line item. Invalid
// supplied currency uses the payment default, and the caller provides the
// minor-unit amount for that currency.
//
// Success and cancellation destinations replace their status query value,
// while order and instance metadata also accompany invoice and intent
// creation. CustomerUpdate is omitted so checkout has no configured update
// instruction for the customer form data. PaymentMethodTypes is omitted for
// Dashboard selection, related objects are expanded, and session expiry is
// clamped using the adapter time policy.
func buildStripeCheckoutSessionCreateParams(customerID string, req payment.CreatePaymentRequest, amountInMinorUnit int64, instanceID string, currency string) *stripe.CheckoutSessionCreateParams {
	normalizedCurrency, err := payment.NormalizePaymentCurrency(currency)
	if err != nil {
		// Use the payment default when supplied currency is invalid; line-item
		// encoding continues with the already supplied minor-unit amount.
		normalizedCurrency = payment.DefaultPaymentCurrency
	}
	metadata := stripePaymentMetadata(req.OrderID, instanceID)
	// Use the customer built from local billing data; omitted CustomerUpdate
	// leaves this session with no instruction to replace that customer form.
	params := &stripe.CheckoutSessionCreateParams{
		Mode:              stripe.String(string(stripe.CheckoutSessionModePayment)),
		Customer:          stripe.String(customerID),
		ClientReferenceID: stripe.String(req.OrderID),
		SuccessURL:        stripe.String(stripeCheckoutReturnURL(req.ReturnURL, "success")),
		CancelURL:         stripe.String(stripeCheckoutReturnURL(req.ReturnURL, "cancelled")),
		InvoiceCreation:   buildStripeCheckoutInvoiceCreationParams(req, instanceID),
		LineItems:         buildStripeCheckoutLineItems(req, amountInMinorUnit, normalizedCurrency),
		Metadata:          metadata,
		PaymentIntentData: buildStripeCheckoutPaymentIntentDataParams(req, instanceID),
		BillingAddressCollection: stripe.String(
			string(stripe.CheckoutSessionBillingAddressCollectionAuto),
		),
	}
	params.ExpiresAt = stripe.Int64(stripeCheckoutExpiresAt(req.ExpiresAt))
	// Leave payment method types absent so Dashboard settings select checkout
	// methods, while expanded objects supply creation response and later
	// confirmation data.
	params.AddExpand("customer")
	params.AddExpand("invoice")
	params.AddExpand("payment_intent")
	return params
}

// buildStripeCheckoutInvoiceCreationParams enables invoice creation with the
// request subject and order/instance metadata. The resulting SDK parameters
// are newly allocated and accompany the hosted session rather than making a
// separate invoice request here.
func buildStripeCheckoutInvoiceCreationParams(req payment.CreatePaymentRequest, instanceID string) *stripe.CheckoutSessionCreateInvoiceCreationParams {
	return &stripe.CheckoutSessionCreateInvoiceCreationParams{
		Enabled: stripe.Bool(true),
		InvoiceData: &stripe.CheckoutSessionCreateInvoiceCreationInvoiceDataParams{
			Description: stripe.String(req.Subject),
			Metadata:    stripePaymentMetadata(req.OrderID, instanceID),
		},
	}
}

// buildStripeCheckoutPaymentIntentDataParams returns description and
// order/instance metadata plus a nonempty normalized receipt email. Billing
// normalization can also mutate the request address through its shared
// pointer, even though this helper consumes the email.
func buildStripeCheckoutPaymentIntentDataParams(req payment.CreatePaymentRequest, instanceID string) *stripe.CheckoutSessionCreatePaymentIntentDataParams {
	billing := normalizeStripeBillingInfo(req)
	params := &stripe.CheckoutSessionCreatePaymentIntentDataParams{
		Description: stripe.String(req.Subject),
		Metadata:    stripePaymentMetadata(req.OrderID, instanceID),
	}
	if billing.Email != "" {
		params.ReceiptEmail = stripe.String(billing.Email)
	}
	return params
}

// buildStripeCheckoutLineItems returns one quantity-one product using the
// supplied minor-unit amount and lowercase currency text. A blank trimmed
// subject receives the TokenRouter payment label; product metadata carries the
// merchant order with an empty instance label.
func buildStripeCheckoutLineItems(req payment.CreatePaymentRequest, amountInMinorUnit int64, currency string) []*stripe.CheckoutSessionCreateLineItemParams {
	productName := strings.TrimSpace(req.Subject)
	if productName == "" {
		productName = "TokenRouter payment"
	}
	return []*stripe.CheckoutSessionCreateLineItemParams{{
		Quantity: stripe.Int64(1),
		PriceData: &stripe.CheckoutSessionCreateLineItemPriceDataParams{
			Currency:   stripe.String(strings.ToLower(currency)),
			UnitAmount: stripe.Int64(amountInMinorUnit),
			ProductData: &stripe.CheckoutSessionCreateLineItemPriceDataProductDataParams{
				Name:     stripe.String(productName),
				Metadata: stripePaymentMetadata(req.OrderID, ""),
			},
		},
	}}
}

// stripeCheckoutReturnURL trims the destination and replaces its status query
// value for success or cancellation navigation. It retains other query values
// and fragment text, so the selected outcome is added to an existing URL
// rather than rebuilding a canonical checkout destination.
//
// Empty destination or status and parse failure return the trimmed input
// unchanged. The helper accepts relative URLs and uses URL query decoding and
// encoding for usable input. Checkout callers therefore approve the
// destination separately before this status decoration is passed into Stripe
// session parameters.
func stripeCheckoutReturnURL(returnURL string, status string) string {
	returnURL = strings.TrimSpace(returnURL)
	if returnURL == "" || strings.TrimSpace(status) == "" {
		return returnURL
	}
	parsed, err := url.Parse(returnURL)
	// Return the trimmed input on URL parse error; upstream checkout validation
	// remains responsible for approving the browser destination.
	if err != nil {
		return returnURL
	}
	query := parsed.Query()
	query.Set("status", strings.TrimSpace(status))
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

// stripeCheckoutExpiresAt returns the bounded expiration as Unix seconds using
// the current instant. It delegates zero, early and late input handling to
// stripeCheckoutExpiresAtAt, which provides a fixed-time form for tests.
func stripeCheckoutExpiresAt(expiresAt time.Time) int64 {
	return stripeCheckoutExpiresAtAt(expiresAt, time.Now())
}

// stripeCheckoutExpiresAtAt clamps a requested expiry between thirty-one
// minutes and twenty-four hours after now. Zero or earlier input selects the
// lower bound, adding one minute beyond the pinned SDK documented
// thirty-minute session minimum; output has second precision.
func stripeCheckoutExpiresAtAt(expiresAt time.Time, now time.Time) int64 {
	// Add one minute beyond the pinned SDK thirty-minute minimum, providing the
	// chosen margin for elapsed work before session creation.
	minExpiresAt := now.Add(31 * time.Minute)
	if expiresAt.IsZero() || expiresAt.Before(minExpiresAt) {
		return minExpiresAt.Unix()
	}
	// Cap the request expiry at the pinned SDK twenty-four-hour session maximum.
	maxExpiresAt := now.Add(24 * time.Hour)
	if expiresAt.After(maxExpiresAt) {
		return maxExpiresAt.Unix()
	}
	// Encode the selected expiration with Unix second precision for Stripe.
	return expiresAt.Unix()
}

// stripeCheckoutSessionExpiresAt converts a positive session expiry from Unix
// seconds to UTC time. Nil sessions and nonpositive expiry values return zero,
// letting checkout omit an unavailable expiration value.
func stripeCheckoutSessionExpiresAt(session *stripe.CheckoutSession) time.Time {
	if session == nil || session.ExpiresAt <= 0 {
		return time.Time{}
	}
	// Interpret positive session expiry as Unix seconds and return UTC.
	return time.Unix(session.ExpiresAt, 0).UTC()
}

// stripePaymentMetadata returns a fresh map with the supplied merchant order
// and trimmed provider instance label. Empty values are retained, including
// the empty instance label used for product metadata.
func stripePaymentMetadata(orderID string, instanceID string) map[string]string {
	return map[string]string{
		"orderId":            orderID,
		"providerInstanceId": strings.TrimSpace(instanceID),
	}
}

// QueryOrder dispatches trimmed session or invoice labels to their readers and
// treats other references as payment intents. Intent queries map succeeded,
// processing and cancelled states and convert money using reported currency
// with configured fallback; channel errors propagate.
func (s *Stripe) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	s.ensureInit()

	tradeNo = strings.TrimSpace(tradeNo)
	if strings.HasPrefix(tradeNo, "cs_") {
		return s.queryCheckoutSession(ctx, tradeNo)
	}
	if strings.HasPrefix(tradeNo, "in_") {
		return s.queryInvoice(ctx, tradeNo)
	}

	pi, err := s.sc.V1PaymentIntents.Retrieve(ctx, tradeNo, nil)
	if err != nil {
		return nil, fmt.Errorf("stripe query order: %w", err)
	}

	status := payment.ProviderStatusPending
	switch pi.Status {
	case stripe.PaymentIntentStatusSucceeded:
		status = payment.ProviderStatusPaid
	case stripe.PaymentIntentStatusProcessing:
		status = payment.ProviderStatusProcessing
	case stripe.PaymentIntentStatusCanceled:
		status = payment.ProviderStatusFailed
	}

	currency := stripeIntentCurrency(pi.Currency, s.currency())
	return &payment.QueryOrderResponse{
		TradeNo: pi.ID,
		Status:  status,
		// Intent money follows reported currency precision, with instance currency
		// used when the channel currency is absent or invalid.
		Amount: payment.MinorUnitToAmount(pi.Amount, currency),
		Metadata: map[string]string{
			"currency": currency,
		},
	}, nil
}

// queryCheckoutSession retrieves expanded customer, invoice and payment intent
// data for combined session-state confirmation. It selects reported currency
// with instance fallback and returns a preferred intent reference, retaining
// sessionID if the response offers no usable identity.
//
// Metadata separately retains the session and invoice labels so changing the
// primary trade reference does not lose those document associations.
// AmountTotal is converted from minor units using the selected currency. The
// caller initializes sc before this helper, and ctx controls retrieval;
// expanded paid observations participate before expiry or completed-unpaid
// failure decisions.
func (s *Stripe) queryCheckoutSession(ctx context.Context, sessionID string) (*payment.QueryOrderResponse, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("customer")
	params.AddExpand("invoice")
	params.AddExpand("payment_intent")
	session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe query checkout session: %w", err)
	}

	status := stripeCheckoutProviderStatus(session)

	currency := stripeIntentCurrency(session.Currency, s.currency())
	tradeNo := stripeCheckoutTradeNo(session)
	if tradeNo == "" {
		// Keep the requested session reference when expanded identity is empty,
		// allowing lifecycle recovery to query the same channel object again.
		tradeNo = sessionID
	}
	return &payment.QueryOrderResponse{
		TradeNo: tradeNo,
		Status:  status,
		// AmountTotal follows session currency precision, with configured currency
		// filling an absent or invalid channel value for core confirmation.
		Amount:   payment.MinorUnitToAmount(session.AmountTotal, currency),
		Metadata: stripeCheckoutMetadata(session, currency),
	}, nil
}

// stripeCheckoutProviderStatus combines session payment, intent and invoice
// state into a core query status. Paid session state, a succeeded expanded
// intent or a paid expanded invoice wins before the session expiry check,
// including contradictory expanded observations.
//
// An expired session otherwise fails. A complete unpaid session fails only
// when stripeCheckoutCompletedPaymentFailed identifies an expanded terminal
// condition; remaining complete unpaid sessions are processing. Nil and all
// other sessions are pending, so a generic completed state alone cannot claim
// payment success for fulfillment.
func stripeCheckoutProviderStatus(session *stripe.CheckoutSession) string {
	if session == nil {
		return payment.ProviderStatusPending
	}
	// Accept paid session, intent or invoice observations before expiry; this
	// ordering retains paid classification for contradictory expanded response
	// states.
	if session.PaymentStatus == stripe.CheckoutSessionPaymentStatusPaid {
		return payment.ProviderStatusPaid
	}
	if session.PaymentIntent != nil && session.PaymentIntent.Status == stripe.PaymentIntentStatusSucceeded {
		return payment.ProviderStatusPaid
	}
	if session.Invoice != nil && session.Invoice.Status == stripe.InvoiceStatusPaid {
		return payment.ProviderStatusPaid
	}
	if session.Status == stripe.CheckoutSessionStatusExpired {
		return payment.ProviderStatusFailed
	}
	if session.Status == stripe.CheckoutSessionStatusComplete && session.PaymentStatus == stripe.CheckoutSessionPaymentStatusUnpaid {
		if stripeCheckoutCompletedPaymentFailed(session) {
			return payment.ProviderStatusFailed
		}
		return payment.ProviderStatusProcessing
	}
	return payment.ProviderStatusPending
}

// stripeCheckoutCompletedPaymentFailed reports terminal expanded conditions
// only for a complete session. Cancelled or requires-payment-method intent
// state and void or uncollectible invoice state each count as failure, while
// missing expansions add no failure evidence.
//
// stripeCheckoutProviderStatus invokes it after its paid and expired checks
// for a complete unpaid session. Thus a true result turns that remaining
// session into failed, while false leaves it processing. A nil or noncomplete
// session also returns false when this helper is called directly; session
// completion alone does not provide terminal failure evidence.
func stripeCheckoutCompletedPaymentFailed(session *stripe.CheckoutSession) bool {
	if session == nil || session.Status != stripe.CheckoutSessionStatusComplete {
		return false
	}
	if session.PaymentIntent != nil {
		switch session.PaymentIntent.Status {
		case stripe.PaymentIntentStatusCanceled, stripe.PaymentIntentStatusRequiresPaymentMethod:
			return true
		}
	}
	if session.Invoice != nil {
		switch session.Invoice.Status {
		case stripe.InvoiceStatusVoid, stripe.InvoiceStatusUncollectible:
			return true
		}
	}
	return false
}

// queryInvoice retrieves invoice confirmation-secret and payment-intent
// expansions, then reports invoice state and document metadata. Paid amount is
// preferred when positive; otherwise amount due is used, and conversion uses
// the configured instance currency rather than the reported invoice currency.
//
// Trade identity prefers the first expanded payment intent, then an intent
// label extracted from confirmation-secret text, then invoice ID. This lets
// recovery associate several invoice representations with a payment reference
// while retaining invoice metadata. The caller initializes sc first, and
// retrieval errors return before any local persistence or fulfillment
// decision.
func (s *Stripe) queryInvoice(ctx context.Context, invoiceID string) (*payment.QueryOrderResponse, error) {
	params := &stripe.InvoiceRetrieveParams{}
	params.AddExpand("confirmation_secret")
	params.AddExpand("payments.data.payment.payment_intent")
	inv, err := s.sc.V1Invoices.Retrieve(ctx, invoiceID, params)
	if err != nil {
		return nil, fmt.Errorf("stripe query invoice: %w", err)
	}

	status := payment.ProviderStatusPending
	switch inv.Status {
	case stripe.InvoiceStatusPaid:
		status = payment.ProviderStatusPaid
	case stripe.InvoiceStatusVoid, stripe.InvoiceStatusUncollectible:
		status = payment.ProviderStatusFailed
	}

	amount := inv.AmountPaid
	if amount <= 0 {
		// Use amount due when paid amount is nonpositive, keeping an invoice amount
		// available for core confirmation of the selected state.
		amount = inv.AmountDue
	}
	tradeNo := stripeInvoicePaymentIntentID(inv)
	// Try the confirmation-secret representation after expanded payment entries
	// yield no intent; invoice identity remains available if extraction also
	// fails.
	if tradeNo == "" && inv.ConfirmationSecret != nil {
		tradeNo = stripePaymentIntentIDFromClientSecret(inv.ConfirmationSecret.ClientSecret)
	}
	if tradeNo == "" {
		// Retain invoice identity when no intent representation is usable, while
		// metadata separately supplies its document and state fields.
		tradeNo = inv.ID
	}
	return &payment.QueryOrderResponse{
		TradeNo: tradeNo,
		Status:  status,
		// Invoice query money uses instance currency precision even when the invoice
		// reports a different currency value.
		Amount: payment.MinorUnitToAmount(amount, s.currency()),
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
			"currency":       s.currency(),
		},
	}, nil
}

// VerifyNotification requires nonempty webhook secret and lowercase-map
// signature header, then delegates body authentication to the pinned Stripe
// webhook constructor. The SDK rejects signatures more than three hundred
// seconds old and incompatible event API versions before this adapter selects
// an event.
//
// That timestamp test checks age rather than absolute distance, so a future
// timestamp is not rejected by its age comparison. This adapter adds no
// separate time window or event-ID replay store, and ctx does not govern
// parsing. Actionable observations still pass through local order association
// and amount confirmation before fulfillment.
func (s *Stripe) VerifyNotification(_ context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	s.ensureInit()

	webhookSecret := s.config["webhookSecret"]
	if webhookSecret == "" {
		return nil, fmt.Errorf("stripe webhookSecret not configured")
	}

	sig := headers["stripe-signature"]
	if sig == "" {
		return nil, fmt.Errorf("stripe notification missing stripe-signature header")
	}

	event, err := webhook.ConstructEvent([]byte(rawBody), sig, webhookSecret)
	if err != nil {
		return nil, fmt.Errorf("stripe verify notification: %w", err)
	}
	return parseStripeEvent(&event, rawBody)
}

// parseStripeEvent maps supported verified event names to checkout, invoice or
// intent observations. Nil and unrelated events return nil, nil; callers
// provide authenticated data because this helper performs no signature
// verification and actionable events require usable event.Data.
//
// Completed checkout starts as processing and its parser consults payment
// status. Expired or asynchronous failed sessions produce failure, while paid
// invoice and succeeded intent events produce success. Invoice-payment and
// intent-attempt failures are ignored because they describe a retryable
// attempt rather than a closing session outcome; later actionable events or
// queries decide order progression.
func parseStripeEvent(event *stripe.Event, rawBody string) (*payment.PaymentNotification, error) {
	if event == nil {
		return nil, nil
	}
	switch event.Type {
	case stripeEventCheckoutDone:
		return parseStripeCheckoutSession(event, payment.ProviderStatusProcessing, rawBody)
	case stripeEventCheckoutExpired:
		return parseStripeCheckoutSession(event, payment.ProviderStatusFailed, rawBody)
	case stripeEventCheckoutAsyncSucceeded:
		return parseStripeCheckoutSession(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventCheckoutAsyncFailed:
		return parseStripeCheckoutSession(event, payment.ProviderStatusFailed, rawBody)
	case stripeEventInvoicePaid:
		return parseStripeInvoice(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventPaymentSuccess:
		return parseStripePaymentIntent(event, payment.ProviderStatusSuccess, rawBody)
	case stripeEventInvoiceFailed, stripeEventPaymentFailed:
		// Ignore attempt-level failures because another checkout attempt may still
		// succeed; expiry and asynchronous failure events carry the actionable
		// closing outcome.
		return nil, nil
	}

	return nil, nil
}

// parseStripeCheckoutSession decodes a usable event.Data.Raw object and
// returns merchant order, money and session/document associations. Reported
// session currency selects conversion with the payment default as fallback,
// and primary trade identity prefers an expanded intent over session identity.
//
// A processing input represents completed events: paid becomes success, unpaid
// remains processing, and other payment statuses are ignored. Any success
// input also requires session PaymentStatus paid, even when another expanded
// object is paid. Failure input remains actionable as supplied by the event
// selector, leaving local transition validation to the payment core.
func parseStripeCheckoutSession(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var session stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
		return nil, fmt.Errorf("stripe parse checkout session: %w", err)
	}
	currency := stripeIntentCurrency(session.Currency, payment.DefaultPaymentCurrency)
	tradeNo := stripeCheckoutTradeNo(&session)
	if tradeNo == "" {
		// Retain raw session identity when the trimmed helper yields no reference,
		// while merchant metadata still supplies the local order association.
		tradeNo = session.ID
	}
	if status == payment.ProviderStatusProcessing {
		// Interpret completed events through payment status; unsupported values
		// produce no observation rather than letting generic completion grant local
		// rights.
		switch session.PaymentStatus {
		case stripe.CheckoutSessionPaymentStatusPaid:
			status = payment.ProviderStatusSuccess
		case stripe.CheckoutSessionPaymentStatusUnpaid:
		default:
			return nil, nil
		}
	}
	// Require session payment status paid for success events, even if expanded
	// intent or invoice data could classify a query as paid.
	if status == payment.ProviderStatusSuccess && session.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid {
		return nil, nil
	}
	return &payment.PaymentNotification{
		TradeNo: tradeNo,
		OrderID: session.Metadata["orderId"],
		// Session event money follows reported currency precision, using CNY when
		// the reported label is absent or invalid.
		Amount:   payment.MinorUnitToAmount(session.AmountTotal, currency),
		Status:   status,
		RawData:  rawBody,
		Metadata: stripeCheckoutMetadata(&session, currency),
	}, nil
}

// parseStripePaymentIntent decodes the supplied nonnil event with usable Data
// and returns its merchant-order metadata and requested observation status.
// Reported intent currency uses the payment default on absence or invalidity;
// the caller authenticates and selects the event first.
func parseStripePaymentIntent(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var pi stripe.PaymentIntent
	if err := json.Unmarshal(event.Data.Raw, &pi); err != nil {
		return nil, fmt.Errorf("stripe parse payment_intent: %w", err)
	}
	currency := stripeIntentCurrency(pi.Currency, payment.DefaultPaymentCurrency)
	return &payment.PaymentNotification{
		TradeNo: pi.ID,
		OrderID: pi.Metadata["orderId"],
		// Intent event money follows reported currency precision, using CNY when the
		// reported label is absent or invalid.
		Amount:  payment.MinorUnitToAmount(pi.Amount, currency),
		Status:  status,
		RawData: rawBody,
		Metadata: map[string]string{
			"currency": currency,
		},
	}, nil
}

// parseStripeInvoice decodes a usable authenticated event and combines invoice
// money, order metadata and trade identity. Positive AmountPaid wins over
// AmountDue, and conversion uses reported invoice currency with the payment
// default, unlike queryInvoice configured-currency conversion.
//
// The first usable expanded payment intent supplies trade identity; invoice ID
// remains when that representation is absent. This event path does not consult
// confirmation-secret text. Caller-supplied observation status comes from
// parseStripeEvent, and returned invoice document metadata accompanies the
// amount for subsequent core confirmation and persistence.
func parseStripeInvoice(event *stripe.Event, status string, rawBody string) (*payment.PaymentNotification, error) {
	var inv stripe.Invoice
	if err := json.Unmarshal(event.Data.Raw, &inv); err != nil {
		return nil, fmt.Errorf("stripe parse invoice: %w", err)
	}
	amount := inv.AmountPaid
	if amount <= 0 {
		// Use amount due when paid amount is nonpositive, keeping an invoice amount
		// available for core confirmation of the selected state.
		amount = inv.AmountDue
	}
	currency := stripeIntentCurrency(inv.Currency, payment.DefaultPaymentCurrency)
	tradeNo := stripeInvoicePaymentIntentID(&inv)
	if tradeNo == "" {
		// Keep invoice identity when the event has no expanded intent, allowing
		// order metadata to associate this paid observation locally.
		tradeNo = inv.ID
	}
	return &payment.PaymentNotification{
		TradeNo: tradeNo,
		OrderID: inv.Metadata["orderId"],
		// Invoice event money follows reported currency precision, while invoice
		// query conversion uses the configured instance currency.
		Amount:  payment.MinorUnitToAmount(amount, currency),
		Status:  status,
		RawData: rawBody,
		Metadata: map[string]string{
			"invoice_id":     inv.ID,
			"invoice_status": string(inv.Status),
			"invoice_url":    inv.HostedInvoiceURL,
			"invoice_pdf":    inv.InvoicePDF,
			"currency":       currency,
		},
	}, nil
}

// Refund submits a currency-normalized refund using the stored PaymentIntent.
// Session and invoice references are resolved before submission, and the
// order/amount key keeps network retries attached to the same operation.
//
// After submission, only a clear channel rejection or terminal failure can
// release the local hold. Network and unclassified Stripe errors instead
// leave the outcome unknown so recovery can look for an accepted refund.
func (s *Stripe) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	s.ensureInit()

	amountInMinorUnit, err := payment.AmountToMinorUnit(req.Amount, s.currency())
	if err != nil {
		return nil, fmt.Errorf("stripe refund: %w", err)
	}
	// Resolve session and invoice references before creating the refund; channel
	// refund parameters require an intent while local orders can retain another
	// representation.
	paymentIntentID, err := s.refundPaymentIntent(ctx, req.TradeNo)
	if err != nil {
		return nil, err
	}

	params := &stripe.RefundCreateParams{
		PaymentIntent: stripe.String(paymentIntentID),
		Amount:        stripe.Int64(amountInMinorUnit),
		Reason:        stripe.String(string(stripe.RefundReasonRequestedByCustomer)),
	}
	// Reuse the order and minor-amount request key on retries; the workflow
	// OperationID and human reason do not distinguish these channel submissions.
	params.SetIdempotencyKey(fmt.Sprintf("re-%s-%d", req.OrderID, amountInMinorUnit))
	params.Context = ctx

	r, err := s.sc.V1Refunds.Create(ctx, params)
	if err != nil {
		// A rejected transport response can follow an accepted refund. Preserve
		// preparation unless Stripe explicitly rejected the request before execution.
		if stripeRefundDefinitelyRejected(err) {
			return nil, fmt.Errorf("stripe refund rejected: %w", err)
		}
		return nil, fmt.Errorf("%w: stripe refund submission: %v", payment.ErrRefundOutcomeUnknown, err)
	}
	if r == nil || strings.TrimSpace(r.ID) == "" {
		return nil, fmt.Errorf("%w: stripe refund response has no identity", payment.ErrRefundOutcomeUnknown)
	}
	return &payment.RefundResponse{RefundID: r.ID, Status: stripeRefundProviderStatus(r.Status)}, nil
}

// QueryRefund verifies a known refund against the order's intent, currency and
// minor-unit amount. A supplied ID also needs to date from preparation or
// later. Without any ID, discovery accepts only a unique matching success.
//
// A missing, ambiguous or unconfirmed match leaves the local preparation
// intact. The method reads Stripe and cannot submit a replacement refund.
func (s *Stripe) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	s.ensureInit()
	intent, err := s.refundPaymentIntent(ctx, req.TradeNo)
	if err != nil {
		return nil, err
	}
	amount, err := payment.AmountToMinorUnit(req.Amount, req.Currency)
	if err != nil || amount <= 0 || strings.TrimSpace(req.Currency) == "" {
		return nil, fmt.Errorf("stripe query refund: invalid expected amount or currency")
	}

	if refundID := strings.TrimSpace(req.RefundID); refundID != "" {
		refund, err := s.sc.V1Refunds.Retrieve(ctx, refundID, nil)
		if err != nil {
			return nil, fmt.Errorf("stripe query refund: %w", err)
		}
		if !stripeRefundMatches(refund, intent, req.Currency, amount) || refund.ID != refundID {
			return nil, fmt.Errorf("stripe query refund: refund does not match the prepared payment")
		}
		if !req.EarliestCreatedAt.IsZero() && refund.Created < req.EarliestCreatedAt.Unix() {
			return nil, fmt.Errorf("stripe query refund: supplied refund predates preparation")
		}
		return &payment.RefundResponse{RefundID: refund.ID, Status: stripeRefundProviderStatus(refund.Status)}, nil
	}

	if req.EarliestCreatedAt.IsZero() {
		return nil, fmt.Errorf("stripe query refund: missing refund preparation time")
	}
	// Stripe timestamps use whole seconds. Search from the second preparation
	// committed and inspect every matching page before deciding uniqueness.
	earliest := req.EarliestCreatedAt.Unix()
	params := &stripe.RefundListParams{
		PaymentIntent: stripe.String(intent),
		CreatedRange:  &stripe.RangeQueryParams{GreaterThanOrEqual: earliest},
	}
	var match *stripe.Refund
	for refund, err := range s.sc.V1Refunds.List(ctx, params).All(ctx) {
		if err != nil {
			return nil, fmt.Errorf("stripe query refund: %w", err)
		}
		if !stripeRefundMatches(refund, intent, req.Currency, amount) || refund.Created < earliest {
			continue
		}
		if match != nil {
			return nil, fmt.Errorf("stripe query refund: multiple matching refunds")
		}
		match = refund
	}
	if match == nil || match.Status != stripe.RefundStatusSucceeded {
		return nil, fmt.Errorf("stripe query refund: no uniquely confirmed refund found")
	}
	return &payment.RefundResponse{RefundID: match.ID, Status: payment.ProviderStatusSuccess}, nil
}

// refundPaymentIntent resolves stored checkout and invoice references for
// Stripe's refund APIs. Resolution errors precede submission and cannot have
// initiated a refund; an empty resolved identifier is rejected.
func (s *Stripe) refundPaymentIntent(ctx context.Context, tradeNo string) (string, error) {
	id := strings.TrimSpace(tradeNo)
	var err error
	switch {
	case strings.HasPrefix(id, "cs_"):
		id, err = s.findCheckoutSessionPaymentIntentID(ctx, id)
	case strings.HasPrefix(id, "in_"):
		id, err = s.findInvoicePaymentIntentID(ctx, id)
	}
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("stripe refund: payment intent is unavailable")
	}
	return id, nil
}

// stripeRefundMatches checks whether Stripe's refund identifies the expected
// payment and amount. A missing intent or refund identifier cannot establish
// the link needed to finish the local operation.
func stripeRefundMatches(refund *stripe.Refund, intent, currency string, amount int64) bool {
	return refund != nil && refund.ID != "" && refund.PaymentIntent != nil &&
		refund.PaymentIntent.ID == intent && strings.EqualFold(string(refund.Currency), currency) && refund.Amount == amount
}

// stripeRefundDefinitelyRejected recognizes pre-execution Stripe rejections.
// All transport errors and unrecognized API errors require recovery because
// the caller cannot tell whether Stripe accepted the refund before responding.
func stripeRefundDefinitelyRejected(err error) bool {
	var apiErr *stripe.Error
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.HTTPStatusCode {
	case 401, 403:
		return true
	case 400, 404:
		switch apiErr.Code {
		case stripe.ErrorCodeParameterInvalidEmpty, stripe.ErrorCodeParameterInvalidInteger,
			stripe.ErrorCodeParameterMissing, stripe.ErrorCodeParameterUnknown, stripe.ErrorCodeResourceMissing:
			return true
		}
	}
	return false
}

// stripeRefundProviderStatus maps known Stripe states to local outcomes.
// Returning an empty status for any new or missing state lets refund recovery
// retain preparation until it can verify an authoritative result.
func stripeRefundProviderStatus(status stripe.RefundStatus) string {
	switch status {
	case stripe.RefundStatusSucceeded:
		return payment.ProviderStatusSuccess
	case stripe.RefundStatusFailed, stripe.RefundStatusCanceled:
		return payment.ProviderStatusFailed
	case stripe.RefundStatusPending, stripe.RefundStatusRequiresAction:
		return payment.ProviderStatusPending
	default:
		return ""
	}
}

// stripeIntentCurrency chooses normalized reported currency, then normalized
// fallback, then the payment default. Empty reported text first normalizes to
// the default currency, but its absence is separately detected so a configured
// nondefault fallback can still win.
//
// Invalid reported text also uses the fallback; valid nonempty
// default-currency text retains its reported choice. This distinction keeps
// currency conversion attached to the channel observation when available while
// allowing query callers to use instance currency and event callers to use the
// payment default for omitted data.
func stripeIntentCurrency(raw stripe.Currency, fallback string) string {
	currency, err := payment.NormalizePaymentCurrency(string(raw))
	// Treat empty reported text as absent even after default normalization, so
	// the instance or event caller supplied fallback can decide conversion
	// currency.
	if err != nil || currency == payment.DefaultPaymentCurrency && strings.TrimSpace(string(raw)) == "" {
		normalizedFallback, fallbackErr := payment.NormalizePaymentCurrency(fallback)
		if fallbackErr == nil {
			return normalizedFallback
		}
		// An invalid supplied fallback still selects CNY, letting observation
		// construction continue until core money and order checks decide acceptance.
		return payment.DefaultPaymentCurrency
	}
	return currency
}

// CancelPayment expires a trimmed session label, voids an invoice label or
// cancels another reference as a payment intent. It uses ctx for the selected
// SDK request and returns channel errors for lifecycle resolution.
func (s *Stripe) CancelPayment(ctx context.Context, tradeNo string) error {
	s.ensureInit()

	tradeNo = strings.TrimSpace(tradeNo)
	if strings.HasPrefix(tradeNo, "cs_") {
		_, err := s.sc.V1CheckoutSessions.Expire(ctx, tradeNo, nil)
		if err != nil {
			return fmt.Errorf("stripe expire checkout session: %w", err)
		}
		return nil
	}
	if strings.HasPrefix(tradeNo, "in_") {
		_, err := s.sc.V1Invoices.VoidInvoice(ctx, tradeNo, nil)
		if err != nil {
			return fmt.Errorf("stripe void invoice: %w", err)
		}
		return nil
	}

	_, err := s.sc.V1PaymentIntents.Cancel(ctx, tradeNo, nil)
	if err != nil {
		return fmt.Errorf("stripe cancel payment: %w", err)
	}
	return nil
}

// GetPaymentDocument retrieves invoice or receipt data using supplied invoice
// and trade identities. A nonempty invoiceID takes precedence and returns its
// invoice-shaped response immediately, even with empty links; retrieval errors
// propagate without another document lookup.
//
// An invoice trade label follows that same path. A session first uses its
// expanded invoice when its selected link is usable, then follows an
// underlying intent receipt. Intent receipt lookup expands latest charge and
// retrieves that charge separately when its receipt URL is absent. Missing
// usable receipt is an error, and ctx controls every request.
func (s *Stripe) GetPaymentDocument(ctx context.Context, invoiceID string, tradeNo string) (*payment.PaymentDocumentResponse, error) {
	s.ensureInit()

	invoiceID = strings.TrimSpace(invoiceID)
	tradeNo = strings.TrimSpace(tradeNo)
	if invoiceID != "" {
		params := &stripe.InvoiceRetrieveParams{}
		params.AddExpand("payments.data.payment.payment_intent")
		inv, err := s.sc.V1Invoices.Retrieve(ctx, invoiceID, params)
		if err != nil {
			return nil, fmt.Errorf("stripe get invoice document: %w", err)
		}
		return stripeInvoiceDocumentResponse(inv), nil
	}
	if strings.HasPrefix(tradeNo, "in_") {
		return s.GetPaymentDocument(ctx, tradeNo, "")
	}
	if strings.HasPrefix(tradeNo, "cs_") {
		params := &stripe.CheckoutSessionRetrieveParams{}
		params.AddExpand("invoice")
		params.AddExpand("payment_intent")
		session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, tradeNo, params)
		if err != nil {
			return nil, fmt.Errorf("stripe get checkout session document: %w", err)
		}
		doc := stripeCheckoutInvoiceDocument(session)
		if strings.TrimSpace(doc.URL) != "" {
			return doc, nil
		}
		// Follow a usable underlying intent when the expanded session invoice has no
		// selected link; other session references cannot reach the receipt path.
		tradeNo = stripeCheckoutTradeNo(session)
		if !strings.HasPrefix(tradeNo, "pi_") {
			return nil, fmt.Errorf("stripe checkout session document is unavailable")
		}
	}
	if tradeNo == "" {
		return nil, fmt.Errorf("stripe payment document requires invoice id or trade no")
	}

	params := &stripe.PaymentIntentRetrieveParams{}
	params.AddExpand("latest_charge")
	pi, err := s.sc.V1PaymentIntents.Retrieve(ctx, tradeNo, params)
	if err != nil {
		return nil, fmt.Errorf("stripe get receipt payment intent: %w", err)
	}
	var charge *stripe.Charge
	if pi.LatestCharge != nil && pi.LatestCharge.ID != "" {
		charge = pi.LatestCharge
		if charge.ReceiptURL == "" {
			// Retrieve the already identified charge when expansion lacks a receipt
			// link; that channel read can fail before a document is returned.
			charge, err = s.sc.V1Charges.Retrieve(ctx, pi.LatestCharge.ID, nil)
			if err != nil {
				return nil, fmt.Errorf("stripe get receipt charge: %w", err)
			}
		}
	}
	if charge == nil || strings.TrimSpace(charge.ReceiptURL) == "" {
		return nil, fmt.Errorf("stripe receipt url is unavailable")
	}
	return &payment.PaymentDocumentResponse{
		Type:       "receipt",
		URL:        charge.ReceiptURL,
		ReceiptURL: charge.ReceiptURL,
	}, nil
}

// stripeInvoiceDocumentResponse selects a trimmed hosted invoice URL before a
// trimmed PDF link and retains raw individual document fields. Nil invoice
// returns an invoice-shaped response with empty identity and links, allowing
// session callers to distinguish usable selected URL themselves.
//
// A nonempty hosted link always wins even if the PDF is also present. The
// response carries invoice ID and status alongside both raw link strings, so
// later metadata extraction can retain invoice association without confusing
// the selected browser destination with the stored document fields.
func stripeInvoiceDocumentResponse(inv *stripe.Invoice) *payment.PaymentDocumentResponse {
	if inv == nil {
		return &payment.PaymentDocumentResponse{Type: "invoice"}
	}
	url := strings.TrimSpace(inv.HostedInvoiceURL)
	if url == "" {
		// Use the PDF destination when hosted invoice text is blank, retaining both
		// raw document fields alongside the selected link.
		url = strings.TrimSpace(inv.InvoicePDF)
	}
	return &payment.PaymentDocumentResponse{
		Type:             "invoice",
		URL:              url,
		HostedInvoiceURL: inv.HostedInvoiceURL,
		InvoicePDF:       inv.InvoicePDF,
		InvoiceID:        inv.ID,
		InvoiceStatus:    string(inv.Status),
	}
}

// stripeCheckoutTradeNo chooses a nonblank trimmed expanded payment-intent ID
// before the trimmed session ID. Nil session returns empty, and a nonnil but
// blank intent identity still leaves the session label as the usable
// reference.
//
// Checkout initially stores session identity and later confirmation can use
// the intent identity, so this preference controls the label used by query and
// event responses. Session identity remains available separately through
// stripeCheckoutMetadata, allowing document retrieval and diagnostics to
// follow the checkout even when the primary trade reference changes.
func stripeCheckoutTradeNo(session *stripe.CheckoutSession) string {
	if session == nil {
		return ""
	}
	if session.PaymentIntent != nil && strings.TrimSpace(session.PaymentIntent.ID) != "" {
		return strings.TrimSpace(session.PaymentIntent.ID)
	}
	// Keep the session reference when intent identity is missing, allowing future
	// lifecycle queries to retrieve that checkout object again.
	return strings.TrimSpace(session.ID)
}

// stripeCheckoutMetadata returns a fresh currency map with nonblank trimmed
// session and invoice document fields when available. Nil session keeps the
// supplied currency alone, and missing invoice data leaves that subset absent.
//
// The session label is retained separately from the primary trade label
// selected by stripeCheckoutTradeNo. Invoice ID, hosted URL, PDF and state are
// copied as strings from the expanded document response, so consumers can
// persist document association while confirmation chooses an intent reference.
// This helper performs no extra SDK retrieval for unexpanded document data.
func stripeCheckoutMetadata(session *stripe.CheckoutSession, currency string) map[string]string {
	metadata := map[string]string{
		"currency": currency,
	}
	if session == nil {
		return metadata
	}
	if sessionID := strings.TrimSpace(session.ID); sessionID != "" {
		metadata["checkout_session_id"] = sessionID
	}
	if doc := stripeCheckoutInvoiceDocument(session); doc != nil {
		if strings.TrimSpace(doc.InvoiceID) != "" {
			metadata["invoice_id"] = strings.TrimSpace(doc.InvoiceID)
		}
		if strings.TrimSpace(doc.HostedInvoiceURL) != "" {
			metadata["invoice_url"] = strings.TrimSpace(doc.HostedInvoiceURL)
		}
		if strings.TrimSpace(doc.InvoicePDF) != "" {
			metadata["invoice_pdf"] = strings.TrimSpace(doc.InvoicePDF)
		}
		if strings.TrimSpace(doc.InvoiceStatus) != "" {
			metadata["invoice_status"] = strings.TrimSpace(doc.InvoiceStatus)
		}
	}
	return metadata
}

// stripeCheckoutInvoiceDocument converts an expanded session invoice to the
// invoice document response. Nil session or missing invoice returns an
// invoice-shaped empty response, and consumers inspect its selected link or
// fields before using it.
func stripeCheckoutInvoiceDocument(session *stripe.CheckoutSession) *payment.PaymentDocumentResponse {
	if session == nil || session.Invoice == nil {
		return &payment.PaymentDocumentResponse{Type: "invoice"}
	}
	return stripeInvoiceDocumentResponse(session.Invoice)
}

// normalizeStripeBillingInfo copies billing scalars, trims name, email and tax
// labels, and uses UserEmail when the copied email is blank. The copied
// Address pointer still refers to the request address, whose country and text
// fields are normalized in place.
//
// Callers therefore retain independent scalar values but observe address
// mutations through req.BillingInfo. Customer creation uses this normalized
// form, and intent-data construction invokes normalization again for receipt
// email. The function accepts absent billing information and returns empty
// fields apart from the optional email default; it does not validate postal,
// tax or email syntax.
func normalizeStripeBillingInfo(req payment.CreatePaymentRequest) payment.BillingInfo {
	var billing payment.BillingInfo
	if req.BillingInfo != nil {
		billing = *req.BillingInfo
	}
	billing.Name = strings.TrimSpace(billing.Name)
	billing.Email = strings.TrimSpace(billing.Email)
	if billing.Email == "" {
		billing.Email = strings.TrimSpace(req.UserEmail)
	}
	billing.TaxIDType = strings.TrimSpace(billing.TaxIDType)
	billing.TaxID = strings.TrimSpace(billing.TaxID)
	// Normalize the shared address in place after copying billing scalars;
	// request owners observe these mutations through the original address
	// pointer.
	if billing.Address != nil {
		billing.Address.Country = strings.ToUpper(strings.TrimSpace(billing.Address.Country))
		billing.Address.Line1 = strings.TrimSpace(billing.Address.Line1)
		billing.Address.Line2 = strings.TrimSpace(billing.Address.Line2)
		billing.Address.City = strings.TrimSpace(billing.Address.City)
		billing.Address.State = strings.TrimSpace(billing.Address.State)
		billing.Address.PostalCode = strings.TrimSpace(billing.Address.PostalCode)
	}
	return billing
}

// buildStripeCustomerCreateParams returns customer parameters from the
// supplied normalized billing form and request metadata. A nonempty address is
// copied into SDK fields, and tax data is included only when both tax type and
// value exist.
func buildStripeCustomerCreateParams(req payment.CreatePaymentRequest, billing payment.BillingInfo, instanceID string) *stripe.CustomerCreateParams {
	params := &stripe.CustomerCreateParams{
		Name:        stripe.String(billing.Name),
		Email:       stripe.String(billing.Email),
		Description: stripe.String(req.Subject),
		Metadata: map[string]string{
			"orderId":            req.OrderID,
			"providerInstanceId": strings.TrimSpace(instanceID),
		},
	}
	if addr := stripeAddressParams(billing.Address); addr != nil {
		params.Address = addr
	}
	if billing.TaxIDType != "" && billing.TaxID != "" {
		params.TaxIDData = []*stripe.CustomerCreateTaxIDDataParams{{
			Type:  stripe.String(billing.TaxIDType),
			Value: stripe.String(billing.TaxID),
		}}
	}
	return params
}

// stripeAddressParams copies nonempty address fields into newly allocated SDK
// string pointers. Nil or wholly empty address returns nil; strings are used
// as supplied because normalizeStripeBillingInfo handles trimming before the
// normal customer-building call.
func stripeAddressParams(addr *payment.BillingAddress) *stripe.AddressParams {
	if addr == nil {
		return nil
	}
	params := &stripe.AddressParams{}
	hasValue := false
	if addr.Country != "" {
		params.Country = stripe.String(addr.Country)
		hasValue = true
	}
	if addr.Line1 != "" {
		params.Line1 = stripe.String(addr.Line1)
		hasValue = true
	}
	if addr.Line2 != "" {
		params.Line2 = stripe.String(addr.Line2)
		hasValue = true
	}
	if addr.City != "" {
		params.City = stripe.String(addr.City)
		hasValue = true
	}
	if addr.State != "" {
		params.State = stripe.String(addr.State)
		hasValue = true
	}
	if addr.PostalCode != "" {
		params.PostalCode = stripe.String(addr.PostalCode)
		hasValue = true
	}
	if !hasValue {
		return nil
	}
	return params
}

// stripeInvoicePaymentIntentID selects the first nonblank trimmed intent
// identity from the invoice payment entries. Nil invoice, missing payment list
// and incomplete entries produce no identity or are skipped before continuing
// to another entry.
//
// This selection uses the supplied expanded page in its order and performs no
// SDK request. queryInvoice can subsequently consult confirmation-secret text,
// while invoice event parsing falls back directly to invoice ID. Keeping the
// selection here consistent lets those callers share payment-entry
// interpretation while retaining their distinct recovery alternatives.
func stripeInvoicePaymentIntentID(inv *stripe.Invoice) string {
	if inv == nil || inv.Payments == nil {
		return ""
	}
	// Inspect the supplied expanded page in order. Skipping incomplete entries
	// lets later payments supply an intent before callers choose their distinct
	// invoice or secret-text alternatives.
	for _, p := range inv.Payments.Data {
		if p == nil || p.Payment == nil || p.Payment.PaymentIntent == nil {
			continue
		}
		if id := strings.TrimSpace(p.Payment.PaymentIntent.ID); id != "" {
			return id
		}
	}
	return ""
}

// stripePaymentIntentIDFromClientSecret extracts the prefix before _secret_
// from trimmed text beginning with pi_. Missing prefix or separator returns
// empty, allowing queryInvoice to keep invoice identity when
// confirmation-secret representation is unavailable.
func stripePaymentIntentIDFromClientSecret(clientSecret string) string {
	clientSecret = strings.TrimSpace(clientSecret)
	if !strings.HasPrefix(clientSecret, "pi_") {
		return ""
	}
	if idx := strings.Index(clientSecret, "_secret_"); idx > 0 {
		return clientSecret[:idx]
	}
	return ""
}

// findCheckoutSessionPaymentIntentID retrieves an expanded intent for a
// session using ctx and the already initialized sc client. Missing expansion
// returns empty, nil; refund callers then decide whether their operation can
// proceed without that identity.
func (s *Stripe) findCheckoutSessionPaymentIntentID(ctx context.Context, sessionID string) (string, error) {
	params := &stripe.CheckoutSessionRetrieveParams{}
	params.AddExpand("payment_intent")
	session, err := s.sc.V1CheckoutSessions.Retrieve(ctx, sessionID, params)
	if err != nil {
		return "", fmt.Errorf("stripe retrieve checkout session: %w", err)
	}
	if session.PaymentIntent == nil {
		return "", nil
	}
	return strings.TrimSpace(session.PaymentIntent.ID), nil
}

// findInvoicePaymentIntentID lists expanded invoice-payment entries and
// returns the first nonblank trimmed intent identity in the returned data. SDK
// list errors propagate, and incomplete entries are skipped before testing
// another item.
//
// The caller initializes sc before invoking this helper, and ctx controls the
// SDK request. An empty identity result means this observed page offers no
// usable intent; Refund treats that as unavailable, while QueryRefund applies
// its missing-intent check. This operation resolves invoice representation for
// those callers and makes no refund submission itself.
func (s *Stripe) findInvoicePaymentIntentID(ctx context.Context, invoiceID string) (string, error) {
	params := &stripe.InvoicePaymentListParams{
		Invoice: stripe.String(invoiceID),
	}
	params.AddExpand("data.payment.payment_intent")
	list := s.sc.V1InvoicePayments.List(ctx, params)
	if err := list.Err(); err != nil {
		return "", fmt.Errorf("stripe list invoice payments: %w", err)
	}
	// Inspect the SDK's current page after its error check. Incomplete entries
	// are skipped so a later expanded payment can supply the refund intent; this
	// loop does not fetch another page.
	for _, p := range list.Data() {
		if p == nil || p.Payment == nil || p.Payment.PaymentIntent == nil {
			continue
		}
		if id := strings.TrimSpace(p.Payment.PaymentIntent.ID); id != "" {
			return id, nil
		}
	}
	return "", nil
}
