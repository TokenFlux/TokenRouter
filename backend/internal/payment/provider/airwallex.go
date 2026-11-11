package provider

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// airwallex.go implements Airwallex payment intents, refunds, and callback
// verification with shared credential-scoped token caching.
// Airwallex checkout creates an intent whose ID and client secret are returned
// for browser confirmation. Payment and refund requests derive request labels
// from their input text, so numerically equivalent amounts with different
// spelling can produce different labels. Queries and signed events report
// channel state for subsequent local confirmation.
//
// Access tokens are shared by endpoint and credential-derived cache identity.
// Each token state remains locked through login, parsing and publication,
// while failures retain its previous fields. Webhooks authenticate timestamp
// text together with the raw body before JSON parsing and apply a five-minute
// absolute time window. Refund submission can return both a channel response
// and an error while settlement is pending, allowing the workflow to retain
// that reference for later queries.

// Airwallex wire labels and limits govern endpoint validation, checkout
// environment, token refresh, signed events and response handling.
const (
	// accepted demo service endpoint.
	airwallexDemoAPIBase = "https://api-demo.airwallex.com/api/v1"
	// accepted production service endpoint.
	airwallexProdAPIBase    = "https://api.airwallex.com/api/v1"
	airwallexDefaultCountry = "CN" // default checkout country label
	// default HTTP request duration.
	airwallexHTTPTimeout     = 15 * time.Second
	airwallexMaxResponseSize = 1 << 20         // response prefix in bytes
	airwallexMaxErrorSummary = 512             // diagnostic prefix in bytes
	airwallexTokenSkew       = 2 * time.Minute // token refresh lead time
	// absolute callback time tolerance.
	airwallexWebhookTolerance = 5 * time.Minute

	// actionable successful payment event.
	airwallexEventPaymentSucceeded = "payment_intent.succeeded"
	// actionable cancelled payment event.
	airwallexEventPaymentCancelled = "payment_intent.cancelled"

	airwallexPaymentStatusSucceeded = "SUCCEEDED" // paid payment intent state
	airwallexPaymentStatusCancelled = "CANCELLED" // failed payment intent state
	// pending received refund state.
	airwallexRefundStatusReceived = "RECEIVED"
	// pending accepted refund state.
	airwallexRefundStatusAccepted = "ACCEPTED"
	// successful settled refund state.
	airwallexRefundStatusSettled = "SETTLED"
	airwallexRefundStatusFailed  = "FAILED" // failed channel refund state
)

// Airwallex shared token storage outlives individual instances, and
// compile-time assignments require the adapter capabilities used by payment
// callers.
var (
	// airwallexAccessTokens retains per-key token states for reuse across
	// instances; accessToken performs typed lookup and no eviction is scheduled
	// here.
	airwallexAccessTokens sync.Map

	_ payment.Provider                 = (*Airwallex)(nil)
	_ payment.CancelableProvider       = (*Airwallex)(nil)
	_ payment.MerchantIdentityProvider = (*Airwallex)(nil)
)

// Airwallex implements intent checkout and refund operations for one
// configured account context.
type Airwallex struct {
	instanceID string // configured provider instance identity
	// config owns the constructor copy; request and cache helpers read these
	// settings as immutable after construction.
	config map[string]string
	// httpClient is reused by do; a nil injected client selects the default
	// fifteen-second request timeout.
	httpClient *http.Client
}

// airwallexTokenState retains the reusable login result for one shared
// credential-derived cache key.
type airwallexTokenState struct {
	// mu protects token and expiresAt through cache checks, HTTP login and
	// publication. Failed refresh leaves both values available for a later
	// eligibility check.
	mu        sync.Mutex // token refresh serialization lock
	token     string     // raw returned access token
	expiresAt time.Time  // token reuse expiration instant
}

// airwallexAuthResponse decodes the access token and its upstream expiration
// text.
type airwallexAuthResponse struct {
	// Token must be nonblank after trimming for acceptance, but accessToken
	// publishes and returns the raw supplied string.
	Token string `json:"token"`
	// ExpiresAt is parsed by parseAirwallexTime; a parse failure selects a
	// twenty-five-minute reuse lifetime at login completion.
	ExpiresAt string `json:"expires_at"`
}

// airwallexCreatePaymentIntentRequest encodes checkout money, merchant
// association and optional browser or descriptor details.
type airwallexCreatePaymentIntentRequest struct {
	// RequestID hashes operation, merchant order, exact amount text and currency,
	// so differing text can select another upstream request label.
	RequestID string `json:"request_id"`
	// unquoted decimal money number.
	Amount airwallexRequestAmount `json:"amount"`
	// normalized configured payment currency.
	Currency string `json:"currency"`
	// local merchant order label.
	MerchantOrderID string `json:"merchant_order_id"`
	// requested browser return destination.
	ReturnURL string `json:"return_url,omitempty"`
	// optional configured statement descriptor.
	Descriptor string `json:"descriptor,omitempty"`
	// local order association attributes.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// airwallexCreateRefundRequest encodes an intent refund and the
// adapter-derived request association label.
type airwallexCreateRefundRequest struct {
	// RequestID hashes refund operation, intent ID and exact amount text; the
	// workflow OperationID does not participate here.
	RequestID string `json:"request_id"`
	// trimmed upstream intent identity.
	PaymentIntentID string `json:"payment_intent_id"`
	// unquoted decimal refund money.
	Amount airwallexRequestAmount `json:"amount,omitempty"`
	// trimmed refund reason text.
	Reason string `json:"reason,omitempty"`
}

// airwallexRequestAmount writes decimal money as a JSON number regardless of
// decimal global quoting configuration.
type airwallexRequestAmount struct {
	decimal.Decimal // decimal money value
}

// airwallexPaymentIntent decodes checkout credentials, merchant identity,
// reported money and channel state.
type airwallexPaymentIntent struct {
	ID string `json:"id"` // upstream payment intent identity.
	// reported creation request label.
	RequestID string `json:"request_id"`
	// ClientSecret is required with ID by CreatePayment after trimming, while the
	// response retains the raw reported value.
	ClientSecret string `json:"client_secret"`
	// reported local merchant label.
	MerchantOrderID string `json:"merchant_order_id"`
	// reported decimal payment money.
	Amount decimal.Decimal `json:"amount"`
	// reported intent payment currency.
	Currency string `json:"currency"`
	Status   string `json:"status"` // reported intent state label.
	// reported unused intent attributes.
	Metadata map[string]string `json:"metadata"`
}

// airwallexHTTPError retains the response information needed to classify a
// failed request without parsing its diagnostic message.
type airwallexHTTPError struct {
	status  int    // HTTP response status
	code    string // Airwallex error code
	summary string // sanitized response summary
}

// airwallexRefund decodes the upstream refund reference and state used during
// local finalization.
type airwallexRefund struct {
	// ID identifies the refund operation for both creation and later verification.
	ID string `json:"id"`
	// reported refund request label.
	RequestID string `json:"request_id"`
	// reported original intent identity.
	PaymentIntentID string `json:"payment_intent_id"`
	// reported decimal refund money.
	Amount decimal.Decimal `json:"amount"`
	// reported refund payment currency.
	Currency string `json:"currency"`
	Status   string `json:"status"` // reported refund state label
}

// airwallexWebhookEvent keeps signed event selection and the raw nested
// payment intent object.
type airwallexWebhookEvent struct {
	ID   string `json:"id"`   // reported unused event identity
	Name string `json:"name"` // actionable event selection name
	// AccountID has identity precedence when its trimmed camel-case value is
	// nonempty; accountID then omits the snake-case alternative.
	AccountID string `json:"accountId"`
	// AccountIDSnake supplies account identity when the trimmed camel-case
	// AccountID is empty, before intentMetadata considers instance configuration.
	AccountIDSnake string   `json:"account_id"`
	Data           struct { // nested callback event data
		Object json.RawMessage `json:"object"` // raw payment intent JSON
	} `json:"data"`
}

// NewAirwallex checks four nonblank connection settings and copies
// configuration before normalizing endpoint, currency and country. It installs
// a fifteen-second HTTP client; login and credential acceptance occur during
// later operations.
func NewAirwallex(instanceID string, config map[string]string) (*Airwallex, error) {
	for _, k := range []string{"clientId", "apiKey", "webhookSecret", "apiBase"} {
		if strings.TrimSpace(config[k]) == "" {
			return nil, fmt.Errorf("airwallex config missing required key: %s", k)
		}
	}
	cfg := cloneStringMap(config)
	apiBase, err := normalizeAirwallexAPIBase(cfg["apiBase"])
	if err != nil {
		return nil, err
	}
	cfg["apiBase"] = apiBase
	currency, err := payment.NormalizePaymentCurrency(cfg["currency"])
	if err != nil {
		return nil, fmt.Errorf("airwallex config currency: %w", err)
	}
	cfg["currency"] = currency
	countryCode, err := normalizeAirwallexCountryCode(cfg["countryCode"])
	if err != nil {
		return nil, err
	}
	cfg["countryCode"] = countryCode
	return &Airwallex{
		instanceID: instanceID,
		config:     cfg,
		httpClient: &http.Client{Timeout: airwallexHTTPTimeout},
	}, nil
}

// normalizeAirwallexCountryCode trims and uppercases the country label, using
// CN for empty text. It requires two ASCII letters after normalization; it
// checks the label syntax rather than membership in an ISO country registry.
func normalizeAirwallexCountryCode(raw string) (string, error) {
	countryCode := strings.ToUpper(strings.TrimSpace(raw))
	if countryCode == "" {
		return airwallexDefaultCountry, nil
	}
	if len(countryCode) != 2 {
		return "", fmt.Errorf("airwallex config countryCode must be a two-letter ISO country code")
	}
	for _, ch := range countryCode {
		if ch < 'A' || ch > 'Z' {
			return "", fmt.Errorf("airwallex config countryCode must be a two-letter ISO country code")
		}
	}
	return countryCode, nil
}

// normalizeAirwallexAPIBase accepts HTTPS bases on the exact demo or
// production host and canonical api/v1 path. Query, fragment and escaped-path
// text are removed, and a missing path receives that API prefix.
//
// The hostname comparison is case-insensitive and includes any supplied port
// in the rejected host text. Returned URL spelling and user information remain
// as parsed, so checkoutEnv subsequently recognizes production by comparison
// with the complete production base string rather than by hostname alone.
func normalizeAirwallexAPIBase(raw string) (string, error) {
	base := strings.TrimSpace(raw)
	if base == "" {
		return "", fmt.Errorf("airwallex apiBase is required")
	}
	parsed, err := url.Parse(base)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return "", fmt.Errorf("airwallex apiBase must be an HTTPS URL")
	}
	host := strings.ToLower(parsed.Host)
	if host != "api-demo.airwallex.com" && host != "api.airwallex.com" {
		return "", fmt.Errorf("airwallex apiBase host must be api-demo.airwallex.com or api.airwallex.com")
	}
	parsed.RawQuery = ""
	parsed.Fragment = ""
	parsed.RawPath = ""
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	if parsed.Path == "" {
		parsed.Path = "/api/v1"
	}
	if parsed.Path != "/api/v1" {
		return "", fmt.Errorf("airwallex apiBase path must be /api/v1")
	}
	return parsed.String(), nil
}

// Name returns the fixed display label for this Airwallex adapter.
func (a *Airwallex) Name() string { return "空中云汇" }

// ProviderKey returns the registry key identifying Airwallex provider
// instances.
func (a *Airwallex) ProviderKey() string { return payment.TypeAirwallex }

// SupportedTypes returns a fresh list containing the single Airwallex checkout
// method.
func (a *Airwallex) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAirwallex}
}

// MerchantIdentityMetadata returns configured currency and an optional trimmed
// account ID in a fresh map. Nil providers return nil, and payment
// confirmation compares reported account identity when this configured
// identity exists.
func (a *Airwallex) MerchantIdentityMetadata() map[string]string {
	if a == nil {
		return nil
	}
	metadata := map[string]string{"currency": a.currency()}
	if accountID := strings.TrimSpace(a.config["accountId"]); accountID != "" {
		metadata["account_id"] = accountID
	}
	return metadata
}

// currency returns normalized configured payment currency, using the payment
// default for a nil provider or invalid value. NewAirwallex normally validates
// the setting before these read-time compatibility defaults apply.
func (a *Airwallex) currency() string {
	if a == nil {
		return payment.DefaultPaymentCurrency
	}
	currency, err := payment.NormalizePaymentCurrency(a.config["currency"])
	if err != nil {
		// Use the payment default after invalid configuration parsing, keeping
		// metadata and request currency reads usable for direct provider fixtures.
		return payment.DefaultPaymentCurrency
	}
	return currency
}

// CreatePayment validates positive decimal amount text, obtains an access
// token and creates an intent for the merchant order. Its deterministic
// request label includes the exact supplied amount spelling and configured
// currency, so repeated identical input reconstructs the label.
//
// The returned intent ID and client secret must be nonblank after trimming,
// but checkout receives their raw strings. Intent ID serves as both trade
// reference and browser intent identity; return destination is passed through
// and ExpiresAt is not mapped. Subsequent local response persistence belongs
// to the calling checkout workflow.
func (a *Airwallex) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("airwallex create payment: invalid amount %s", req.Amount)
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("airwallex auth: %w", err)
	}

	currency := a.currency()
	// Bind the request label to exact order, amount text and currency before
	// submission; the caller persists returned intent identity separately.
	requestID := airwallexDeterministicRequestID("payment-intent", req.OrderID, req.Amount, currency)
	payload := airwallexCreatePaymentIntentRequest{
		RequestID:       requestID,
		Amount:          newAirwallexRequestAmount(amount),
		Currency:        currency,
		MerchantOrderID: req.OrderID,
		ReturnURL:       req.ReturnURL,
		Metadata: map[string]string{
			"order_id": req.OrderID,
		},
	}
	if descriptor := strings.TrimSpace(a.config["descriptor"]); descriptor != "" {
		payload.Descriptor = descriptor
	}

	var intent airwallexPaymentIntent
	if err := a.doJSON(ctx, http.MethodPost, "/pa/payment_intents/create", token, payload, &intent); err != nil {
		return nil, fmt.Errorf("airwallex create payment: %w", err)
	}
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.ClientSecret) == "" {
		return nil, fmt.Errorf("airwallex create payment: missing payment intent id or client secret")
	}
	return &payment.CreatePaymentResponse{
		TradeNo:      intent.ID,
		ClientSecret: intent.ClientSecret,
		IntentID:     intent.ID,
		Currency:     currency,
		CountryCode:  a.config["countryCode"],
		PaymentEnv:   a.checkoutEnv(),
	}, nil
}

// QueryOrder requires a trimmed intent reference and reads that intent using
// ctx for login and HTTP operations. It returns reported identity, normalized
// state and approximate float money for core confirmation without
// independently requiring a complete paid response here.
func (a *Airwallex) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	intentID := strings.TrimSpace(tradeNo)
	if intentID == "" {
		return nil, fmt.Errorf("airwallex query order: missing payment intent id")
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("airwallex auth: %w", err)
	}

	var intent airwallexPaymentIntent
	if err := a.doJSON(ctx, http.MethodGet, "/pa/payment_intents/"+url.PathEscape(intentID), token, nil, &intent); err != nil {
		return nil, fmt.Errorf("airwallex query order: %w", err)
	}
	return &payment.QueryOrderResponse{
		TradeNo: intent.ID,
		Status:  airwallexProviderStatus(intent.Status),
		// Convert reported decimal money approximately for payment core amount
		// confirmation.
		Amount:   intent.Amount.InexactFloat64(),
		Metadata: a.intentMetadata(intent, ""),
	}, nil
}

// VerifyNotification authenticates timestamp and raw body before decoding an
// actionable success or cancellation event. Other signed event names return
// nil, nil, while accepted events require nonblank intent and merchant labels
// after trimming.
//
// A success name additionally requires the normalized intent state SUCCEEDED.
// Cancellation produces a failed observation without checking that intent
// state, and the core non-Stripe callback gate decides how to handle it.
// Reported money uses float conversion, reported account identity takes
// precedence over configured identity, and ctx does not control this parsing
// operation.
func (a *Airwallex) VerifyNotification(_ context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	if err := verifyAirwallexWebhookSignature(rawBody, headers, a.config["webhookSecret"], time.Now()); err != nil {
		return nil, err
	}

	var event airwallexWebhookEvent
	if err := json.Unmarshal([]byte(rawBody), &event); err != nil {
		return nil, fmt.Errorf("airwallex parse webhook: %w", err)
	}
	switch event.Name {
	case airwallexEventPaymentSucceeded, airwallexEventPaymentCancelled:
	default:
		return nil, nil
	}

	var intent airwallexPaymentIntent
	if err := json.Unmarshal(event.Data.Object, &intent); err != nil {
		return nil, fmt.Errorf("airwallex parse payment intent: %w", err)
	}
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.MerchantOrderID) == "" {
		return nil, fmt.Errorf("airwallex webhook missing payment intent id or merchant_order_id")
	}
	status := payment.ProviderStatusFailed
	if event.Name == airwallexEventPaymentSucceeded {
		if strings.ToUpper(strings.TrimSpace(intent.Status)) != airwallexPaymentStatusSucceeded {
			return nil, fmt.Errorf("airwallex succeeded webhook has non-succeeded status: %s", intent.Status)
		}
		status = payment.NotificationStatusSuccess
	}

	return &payment.PaymentNotification{
		TradeNo: intent.ID,
		OrderID: intent.MerchantOrderID,
		// Convert reported decimal money approximately for payment core amount
		// confirmation.
		Amount:   intent.Amount.InexactFloat64(),
		Status:   status,
		RawData:  rawBody,
		Metadata: a.intentMetadata(intent, event.accountID()),
	}, nil
}

// Refund requires a trimmed payment intent ID and positive decimal amount,
// then submits an authenticated refund. Its request label hashes the
// operation, intent and exact amount spelling; local order, currency and
// workflow OperationID are not included.
//
// A nonblank returned refund ID is required and retained as raw text. Settled
// status returns success; every other state returns a response together with
// an error, including pending received or accepted states. The refund workflow
// can retain a pending response for query-based finalization, while failed
// channel state follows its failure handling.
func (a *Airwallex) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	intentID := strings.TrimSpace(req.TradeNo)
	if intentID == "" {
		return nil, fmt.Errorf("airwallex refund missing payment intent id")
	}
	amount, err := decimal.NewFromString(req.Amount)
	if err != nil || amount.LessThanOrEqual(decimal.Zero) {
		return nil, fmt.Errorf("airwallex refund: invalid amount %s", req.Amount)
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("airwallex auth: %w", err)
	}

	payload := airwallexCreateRefundRequest{
		RequestID:       airwallexDeterministicRequestID("refund", intentID, req.Amount),
		PaymentIntentID: intentID,
		Amount:          newAirwallexRequestAmount(amount),
		Reason:          strings.TrimSpace(req.Reason),
	}
	if payload.Reason == "" {
		payload.Reason = "refund"
	}

	var resp airwallexRefund
	if err := a.doJSON(ctx, http.MethodPost, "/pa/refunds/create", token, payload, &resp); err != nil {
		// A parameter or authorization rejection cannot create a refund.
		// Unknown responses keep the prepared rights available for recovery.
		if airwallexRefundDefinitelyRejected(err) {
			return nil, fmt.Errorf("airwallex refund rejected: %w", err)
		}
		return nil, fmt.Errorf("%w: airwallex refund submission: %v", payment.ErrRefundOutcomeUnknown, err)
	}
	if strings.TrimSpace(resp.ID) == "" {
		return nil, fmt.Errorf("%w: airwallex refund response has no identity", payment.ErrRefundOutcomeUnknown)
	}
	refundResp := &payment.RefundResponse{
		RefundID: resp.ID,
		Status:   airwallexRefundProviderStatus(resp.Status),
	}
	if refundResp.Status != payment.ProviderStatusSuccess {
		return refundResp, fmt.Errorf("airwallex refund not settled: status %s", strings.ToUpper(strings.TrimSpace(resp.Status)))
	}
	return refundResp, nil
}

// airwallexRefundDefinitelyRejected recognizes documented pre-execution
// rejections. Other HTTP or transport failures leave the provider outcome
// unknown so the caller cannot compensate a possibly accepted refund.
func airwallexRefundDefinitelyRejected(err error) bool {
	var apiErr *airwallexHTTPError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return true
	case http.StatusBadRequest:
		switch apiErr.code {
		case "invalid_argument", "validation_error", "field_required", "validation_failed",
			"invalid_status_for_operation", "amount_above_limit", "operation_not_supported", "resource_not_found":
			return true
		}
	case http.StatusNotFound:
		return apiErr.code == "not_found" || apiErr.code == "resource_not_found"
	}
	return false
}

// QueryRefund retrieves the saved Airwallex refund and verifies its original
// intent, amount and currency before returning a settlement outcome. An
// absent identity or inconsistent response leaves recovery pending.
func (a *Airwallex) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	refundID := strings.TrimSpace(req.RefundID)
	if refundID == "" {
		return nil, fmt.Errorf("airwallex query refund: missing refund id")
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("airwallex auth: %w", err)
	}
	var resp airwallexRefund
	if err := a.doJSON(ctx, http.MethodGet, "/pa/refunds/"+url.PathEscape(refundID), token, nil, &resp); err != nil {
		return nil, fmt.Errorf("airwallex query refund: %w", err)
	}
	expectedAmount, err := decimal.NewFromString(req.Amount)
	if err != nil || !expectedAmount.IsPositive() {
		return nil, fmt.Errorf("airwallex query refund: invalid expected amount")
	}
	if resp.ID != refundID || resp.PaymentIntentID == "" || resp.PaymentIntentID != strings.TrimSpace(req.TradeNo) ||
		req.Currency == "" || !strings.EqualFold(resp.Currency, req.Currency) || !resp.Amount.Equal(expectedAmount) {
		return nil, fmt.Errorf("airwallex query refund: refund does not match the prepared payment")
	}
	return &payment.RefundResponse{RefundID: resp.ID, Status: airwallexRefundProviderStatus(resp.Status)}, nil
}

// CancelPayment treats an empty trimmed intent reference as success and
// otherwise posts an authenticated cancellation. Successful HTTP parsing
// returns nil without requiring a cancelled response state; channel errors
// propagate for the caller to resolve.
func (a *Airwallex) CancelPayment(ctx context.Context, tradeNo string) error {
	intentID := strings.TrimSpace(tradeNo)
	if intentID == "" {
		return nil
	}
	token, err := a.accessToken(ctx)
	if err != nil {
		return fmt.Errorf("airwallex auth: %w", err)
	}
	var intent airwallexPaymentIntent
	if err := a.doJSON(ctx, http.MethodPost, "/pa/payment_intents/"+url.PathEscape(intentID)+"/cancel", token, nil, &intent); err != nil {
		return fmt.Errorf("airwallex cancel payment: %w", err)
	}
	return nil
}

// intentMetadata returns normalized reported currency and status in a fresh
// map. A nonblank trimmed reported account ID wins over configured account ID,
// supplying the identity comparison used by the payment core.
func (a *Airwallex) intentMetadata(intent airwallexPaymentIntent, accountID string) map[string]string {
	metadata := map[string]string{
		"currency": strings.ToUpper(strings.TrimSpace(intent.Currency)),
		"status":   strings.ToUpper(strings.TrimSpace(intent.Status)),
	}
	if accountID = strings.TrimSpace(accountID); accountID != "" {
		metadata["account_id"] = accountID
	} else if configured := strings.TrimSpace(a.config["accountId"]); configured != "" {
		// Use configured account identity when the event supplies none, so the core
		// can still compare this instance association.
		metadata["account_id"] = configured
	}
	return metadata
}

// checkoutEnv returns prod when the complete configured base equals the
// production constant case-insensitively. Every other base selects demo,
// including a parsed production-host URL retaining additional user-information
// text.
func (a *Airwallex) checkoutEnv() string {
	if strings.EqualFold(a.config["apiBase"], airwallexProdAPIBase) {
		return "prod"
	}
	return "demo"
}

// accessToken reuses or refreshes the shared token state selected by endpoint
// and credential-derived identity. It holds the state mutex through cache
// eligibility, context-bound login, response parsing and publication,
// serializing refreshes for that key.
//
// A token is reusable only when its expiry is strictly later than now plus two
// minutes. Cache hits return directly even if ctx is already cancelled; mutex
// admission itself is not cancellable. Login failures keep previous fields,
// and malformed expiry chooses twenty-five minutes from parsing time.
// Successful publication retains the raw nonblank token, and later API
// rejection does not invalidate this cache here.
func (a *Airwallex) accessToken(ctx context.Context) (string, error) {
	cacheKey := a.tokenCacheKey()
	// Publish one shared state before taking its mutex; another instance with the
	// same cache key joins that state for refresh.
	rawState, _ := airwallexAccessTokens.LoadOrStore(cacheKey, &airwallexTokenState{})
	state, ok := rawState.(*airwallexTokenState)
	if !ok {
		return "", fmt.Errorf("airwallex auth token cache state type mismatch")
	}
	// Hold token state through the login request and response validation, so
	// another refresher observes either the previous fields or the completed
	// replacement.
	state.mu.Lock()
	defer state.mu.Unlock()

	if state.token != "" && time.Now().Add(airwallexTokenSkew).Before(state.expiresAt) {
		return state.token, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.config["apiBase"]+"/authentication/login", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-client-id", a.config["clientId"])
	req.Header.Set("x-api-key", a.config["apiKey"])
	if accountID := strings.TrimSpace(a.config["accountId"]); accountID != "" {
		req.Header.Set("x-login-as", accountID)
	}

	body, status, err := a.do(req)
	if err != nil {
		return "", err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return "", formatAirwallexAuthHTTPError(status, body)
	}
	var resp airwallexAuthResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", fmt.Errorf("parse authentication response: %w", err)
	}
	if strings.TrimSpace(resp.Token) == "" {
		return "", fmt.Errorf("authentication response missing token")
	}
	expiresAt, err := parseAirwallexTime(resp.ExpiresAt)
	if err != nil {
		// Use a twenty-five-minute compatibility lifetime after malformed expiry
		// text; the two-minute refresh margin still applies to this stored instant.
		expiresAt = time.Now().Add(25 * time.Minute)
	}
	// Publish token and expiry together after all acceptance checks, retaining
	// the previous pair on every earlier login or parsing error.
	state.token = resp.Token
	state.expiresAt = expiresAt
	return state.token, nil
}

// formatAirwallexAuthHTTPError includes a bounded body summary in login
// failure diagnostics. Unauthorized or forbidden responses add fixed
// credential, environment and account-setting guidance, while other HTTP
// statuses return the shorter authentication error.
func formatAirwallexAuthHTTPError(status int, body []byte) error {
	summary := summarizeAirwallexResponse(body)
	if status == http.StatusUnauthorized || status == http.StatusForbidden {
		return fmt.Errorf("authentication HTTP %d: %s; Airwallex credentials were rejected, check Client ID/API Key, API Base environment (sandbox: https://api-demo.airwallex.com/api/v1, production: https://api.airwallex.com/api/v1), and Account ID (leave it empty for single-account scoped keys)", status, summary)
	}
	return fmt.Errorf("authentication HTTP %d: %s", status, summary)
}

// tokenCacheKey combines raw endpoint and client identity, trimmed account
// identity and a shortened API-key hash for shared token lookup. The hash
// contributes its first eight bytes as hexadecimal, so the key separates these
// configured observations without retaining API-key plaintext.
//
// This is a cache association label, and its delimiters and truncated hash do
// not establish globally unique credential identity. Callers keep
// configuration immutable after construction because changing its text can
// select another token state while previously inserted states remain in the
// shared map.
func (a *Airwallex) tokenCacheKey() string {
	sum := sha256.Sum256([]byte(a.config["apiKey"]))
	// Keep only eight hash bytes in the cache label; the shared cache retains
	// prior entries even when credential-derived text changes.
	return a.config["apiBase"] + "|" + a.config["clientId"] + "|" + strings.TrimSpace(a.config["accountId"]) + "|" + hex.EncodeToString(sum[:8])
}

// doJSON encodes a nonnil payload, sends an authenticated JSON request and
// requires successful HTTP status. A nil output or whitespace-only successful
// body returns nil with output unchanged; other bodies decode into out, and
// authentication failures do not trigger cache refresh here.
func (a *Airwallex) doJSON(ctx context.Context, method, path, token string, payload any, out any) error {
	var bodyReader io.Reader
	if payload != nil {
		body, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.config["apiBase"]+path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	if accountID := strings.TrimSpace(a.config["accountId"]); accountID != "" {
		req.Header.Set("x-on-behalf-of", accountID)
	}

	body, status, err := a.do(req)
	if err != nil {
		return err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		var failure struct {
			Code string `json:"code"` // provider classification code
		}
		// An unreadable error body has no trustworthy classification code.
		// Preserve the status and safe diagnostic without treating it as success.
		if err := json.Unmarshal(body, &failure); err != nil {
			failure.Code = ""
		}
		return &airwallexHTTPError{status: status, code: failure.Code, summary: summarizeAirwallexResponse(body)}
	}
	// Accept a successful empty body with output unchanged; operation-specific
	// required fields decide whether that response is sufficient afterward.
	if out == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("parse response: %w", err)
	}
	return nil
}

// Error formats the HTTP status and response summary for existing diagnostics.
func (e *airwallexHTTPError) Error() string {
	return fmt.Sprintf("HTTP %d: %s", e.status, e.summary)
}

// do sends the supplied request through the reused client or a fifteen-second
// default and closes its response. It returns HTTP status with at most one MiB
// of body, leaving status acceptance and JSON decoding to callers.
func (a *Airwallex) do(req *http.Request) ([]byte, int, error) {
	client := a.httpClient
	if client == nil {
		// A nil injected client uses the constructor duration policy, so direct test
		// fixtures still receive a bounded HTTP request lifetime.
		client = &http.Client{Timeout: airwallexHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	// Close the body after reading its prefix; close errors are discarded while
	// operation parsers consume the obtained bytes and read error.
	defer func() { _ = resp.Body.Close() }()
	// Limit parser input to one MiB; larger responses become a prefix and receive
	// no independent oversize rejection here.
	body, err := io.ReadAll(io.LimitReader(resp.Body, airwallexMaxResponseSize))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// airwallexProviderStatus normalizes channel text and maps succeeded to paid
// or cancelled to failed. Every other state remains pending, leaving
// subsequent recovery queries to observe further progress.
func airwallexProviderStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case airwallexPaymentStatusSucceeded:
		return payment.ProviderStatusPaid
	case airwallexPaymentStatusCancelled:
		return payment.ProviderStatusFailed
	default:
		return payment.ProviderStatusPending
	}
}

// airwallexRefundProviderStatus normalizes channel text and maps settled to
// success or failed to failure. Received, accepted and all unknown states
// remain pending for later query-based refund finalization.
func airwallexRefundProviderStatus(status string) string {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case airwallexRefundStatusSettled:
		return payment.ProviderStatusSuccess
	case airwallexRefundStatusFailed:
		return payment.ProviderStatusFailed
	case airwallexRefundStatusReceived, airwallexRefundStatusAccepted:
		return payment.ProviderStatusPending
	default:
		return payment.ProviderStatusPending
	}
}

// airwallexDeterministicRequestID hashes NUL-joined input text into a
// repeatable UUID-shaped request label. It copies sixteen hash bytes and sets
// version-four and variant bits, producing deterministic text rather than
// drawing randomness for each request.
//
// Exact spelling participates: amount strings 10 and 10.00 can yield different
// labels. Callers using the separator as an unambiguous tuple encoding supply
// NUL-free parts, since this helper does not validate them. Hash truncation
// and bit replacement also mean the label is an upstream association choice
// rather than proof of global uniqueness.
func airwallexDeterministicRequestID(parts ...string) string {
	hash := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	var id uuid.UUID
	// Retain sixteen hash bytes and apply UUID version and variant bits;
	// identical input text reconstructs the same request label.
	copy(id[:], hash[:16])
	id[6] = (id[6] & 0x0f) | 0x40
	id[8] = (id[8] & 0x3f) | 0x80
	return id.String()
}

// verifyAirwallexWebhookSignature compares an HMAC over trimmed timestamp text
// followed by the untouched body, then validates the timestamp. It trims the
// configured secret and lowercases signature text, so uppercase hexadecimal
// signatures can match the computed lowercase value.
//
// Timestamp parsing truncates decimal milliseconds before constructing an
// instant. A zero now selects the current time; absolute differences greater
// than five minutes reject both old and future callbacks, with equality
// accepted. Authentication occurs before the timestamp check, and callers
// subsequently parse JSON using the same raw body that participated in the
// HMAC.
func verifyAirwallexWebhookSignature(rawBody string, headers map[string]string, secret string, now time.Time) error {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return fmt.Errorf("airwallex webhookSecret not configured")
	}
	timestamp := strings.TrimSpace(headers["x-timestamp"])
	signature := strings.ToLower(strings.TrimSpace(headers["x-signature"]))
	if timestamp == "" || signature == "" {
		return fmt.Errorf("airwallex notification missing x-timestamp or x-signature header")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	// Hash timestamp text before raw body using the fixed HMAC implementation;
	// its Write methods return no operational error to retain.
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte(rawBody))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return fmt.Errorf("airwallex invalid signature")
	}

	ts, err := parseAirwallexWebhookTimestamp(timestamp)
	if err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now()
	}
	// Apply the same absolute tolerance to past and future instants; exact
	// five-minute differences remain accepted for subsequent JSON handling.
	if diff := now.Sub(ts).Abs(); diff > airwallexWebhookTolerance {
		return fmt.Errorf("airwallex webhook timestamp outside tolerance")
	}
	return nil
}

// parseAirwallexWebhookTimestamp parses decimal timestamp text and truncates
// it to integer Unix milliseconds. The truncated value must be positive;
// fractions below one millisecond fail, and the helper adds no separate
// integer-range validation.
func parseAirwallexWebhookTimestamp(raw string) (time.Time, error) {
	ts, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}, fmt.Errorf("airwallex invalid webhook timestamp")
	}
	// Truncate fractional decimal milliseconds before constructing the callback
	// time instant.
	millis := ts.IntPart()
	if millis <= 0 {
		return time.Time{}, fmt.Errorf("airwallex invalid webhook timestamp")
	}
	return time.UnixMilli(millis), nil
}

// parseAirwallexTime accepts trimmed RFC3339 text or either supported
// offset-without-colon layout. Empty or unmatched text returns an error, which
// accessToken converts into its configured compatibility reuse lifetime after
// successful login parsing.
func parseAirwallexTime(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, fmt.Errorf("empty time")
	}
	// Try the accepted layouts in order so offset text without a colon can still
	// supply expiration. Only total exhaustion reaches login's compatibility
	// lifetime.
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05.000-0700"} {
		if t, err := time.Parse(layout, raw); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("invalid time: %s", raw)
}

// summarizeAirwallexResponse collapses whitespace and uses an empty marker for
// absent response text. Longer diagnostics keep the first 512 bytes plus an
// ellipsis, which can split UTF-8 and carries no content redaction.
func summarizeAirwallexResponse(body []byte) string {
	summary := strings.Join(strings.Fields(string(body)), " ")
	if summary == "" {
		return "<empty>"
	}
	if len(summary) > airwallexMaxErrorSummary {
		// Bound collapsed diagnostics by bytes; this prefix can split a multibyte
		// character before the ellipsis is appended.
		return summary[:airwallexMaxErrorSummary] + "..."
	}
	return summary
}

// newAirwallexRequestAmount wraps decimal money for unquoted numeric JSON
// encoding.
func newAirwallexRequestAmount(amount decimal.Decimal) airwallexRequestAmount {
	return airwallexRequestAmount{Decimal: amount}
}

// MarshalJSON emits the decimal string directly as JSON number bytes. It
// avoids the decimal package global quoting switch, so payment and refund
// request amounts keep the same numeric wire representation.
func (a airwallexRequestAmount) MarshalJSON() ([]byte, error) {
	return []byte(a.String()), nil
}

// UnmarshalJSON parses decimal text after removing quote characters from both
// ends and replaces the embedded value on success. Invalid input returns its
// parse error and leaves the existing value unchanged.
func (a *airwallexRequestAmount) UnmarshalJSON(data []byte) error {
	amount, err := decimal.NewFromString(strings.Trim(string(data), `"`))
	if err != nil {
		return err
	}
	a.Decimal = amount
	return nil
}

// accountID returns a nonempty trimmed camel-case accountId before the trimmed
// snake-case account_id alternative. intentMetadata later uses instance
// configuration when this helper returns empty, retaining reported-account
// precedence in callback confirmation.
func (e airwallexWebhookEvent) accountID() string {
	if accountID := strings.TrimSpace(e.AccountID); accountID != "" {
		return accountID
	}
	return strings.TrimSpace(e.AccountIDSnake)
}
