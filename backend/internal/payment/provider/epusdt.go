package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// epusdt.go creates EPUSDT hosted checkouts through its EPay-compatible
// submit.php endpoint and verifies MD5-signed form notifications. The service
// implements a redirect checkout rather than the generic EasyPay API checkout:
// a successful POST returns a cashier Location containing the gateway trade
// identifier. The adapter saves that identifier for the existing payment
// runtime and reads the cashier's JSON endpoints to confirm later observations.
//
// Fiat order amounts remain separate from the token quantity selected by the
// cashier. Every successful callback must agree with the configured merchant,
// its signature, and a paid cashier observation before fulfillment checks the
// saved local order. Redirects are inspected without following them, responses
// are bounded, and ambiguous creation failures retain the local order because
// the gateway may already have accepted the signed form.

// EPUSDT transport bounds limit one provider operation and its decoded response;
// gateway statuses distinguish waiting, paid, expired, and asset-selection orders.
const (
	epusdtHTTPTimeout = 15 * time.Second // individual HTTP request budget
	epusdtMaxBody     = 1 << 20          // maximum response body bytes
	epusdtWaiting     = 1                // awaiting token transfer
	epusdtPaid        = 2                // gateway confirmed payment
	epusdtExpired     = 3                // gateway expired order
	epusdtSelecting   = 4                // awaiting cashier asset selection
)

// Adapter assertions keep checkout and merchant identity capabilities checked
// whenever the common payment interfaces change during application development.
var (
	_ payment.Provider                 = (*EPUSDT)(nil)
	_ payment.MerchantIdentityProvider = (*EPUSDT)(nil)
)

// EPUSDT implements GMPay checkout and verification for one configured merchant.
type EPUSDT struct {
	baseURL    string       // validated HTTPS service prefix
	pid        string       // configured merchant PID
	secret     string       // merchant EPay signing secret
	currency   string       // fiat order currency
	notifyURL  string       // configured callback destination
	httpClient *http.Client // shared bounded transport client
}

// epusdtReply decodes the gateway envelope and cashier observation fields.
type epusdtReply struct {
	Code int `json:"status_code"` // gateway business response code
	Data struct {
		TradeID  string      `json:"trade_id"` // gateway parent trade identifier
		Amount   json.Number `json:"amount"`   // fiat order amount
		Currency string      `json:"currency"` // fiat currency reported by gateway
		Status   int         `json:"status"`   // gateway payment state
	} `json:"data"` // cashier query payload
}

// NewEPUSDT constructs a merchant adapter with a bounded HTTPS client.
// The base URL must exclude user information, query parameters, and fragments;
// CNY and USD are supported fiat currencies, with CNY selected when omitted.
// A nonempty HTTPS notification destination is required before enabling checkout.
func NewEPUSDT(_ string, config map[string]string) (*EPUSDT, error) {
	base, err := url.Parse(strings.TrimRight(strings.TrimSpace(config["apiBase"]), "/"))
	if err != nil || base.Scheme != "https" || base.Hostname() == "" ||
		base.User != nil || base.RawQuery != "" || base.Fragment != "" {
		return nil, fmt.Errorf("epusdt apiBase must be an absolute HTTPS URL")
	}
	currency, err := payment.NormalizePaymentCurrency(config["currency"])
	if err != nil || (currency != "CNY" && currency != "USD") {
		return nil, fmt.Errorf("epusdt currency must be CNY or USD")
	}
	notifyURL, err := url.Parse(strings.TrimSpace(config["notifyUrl"]))
	if err != nil || notifyURL.Scheme != "https" || notifyURL.Hostname() == "" ||
		notifyURL.User != nil || notifyURL.Fragment != "" {
		return nil, fmt.Errorf("epusdt notifyUrl must be an absolute HTTPS URL")
	}
	pid := strings.TrimSpace(config["pid"])
	if pid == "" || !easyPaySafeNotifyValue(pid) || strings.TrimSpace(config["secretKey"]) == "" {
		return nil, fmt.Errorf("epusdt merchant PID and secret are required")
	}
	return &EPUSDT{
		baseURL:   strings.TrimRight(base.String(), "/"),
		pid:       pid,
		secret:    config["secretKey"],
		currency:  currency,
		notifyURL: notifyURL.String(),
		httpClient: &http.Client{
			Timeout: epusdtHTTPTimeout,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// Name returns the payment adapter's administrator-facing product display name.
func (e *EPUSDT) Name() string { return "EPUSDT (EPay protocol)" }

// ProviderKey returns the persisted identity used for GMPay provider dispatch.
func (e *EPUSDT) ProviderKey() string { return payment.TypeEPUSDT }

// SupportedTypes returns a fresh collection containing the cryptocurrency checkout method.
func (e *EPUSDT) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeEPUSDT}
}

// MerchantIdentityMetadata returns merchant and fiat currency attributes for order validation.
func (e *EPUSDT) MerchantIdentityMetadata() map[string]string {
	return map[string]string{"pid": e.pid, "currency": e.currency}
}

// CreatePayment posts one MD5-signed EPay form to the redirect checkout.
// The alipay compatibility type leaves asset selection to the cashier defaults.
// A same-origin cashier redirect supplies the gateway reference. Transport or
// unrecognized response failures retain the pending order for signed callback
// recovery because repeating creation could submit the same purchase twice.
// EPay does not return an expiry timestamp, so the local order keeps its deadline.
func (e *EPUSDT) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	if _, err := easyPayPositiveMoney(req.Amount); err != nil ||
		!easyPaySafeNotifyValue(req.OrderID) || req.OrderID == "" || len(req.OrderID) > 32 ||
		!easyPaySafeNotifyValue(req.Subject) {
		return nil, fmt.Errorf("epusdt EPay checkout order or amount is invalid")
	}
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = e.notifyURL
	}
	params := map[string]string{
		"pid": e.pid, "type": "alipay", "out_trade_no": req.OrderID,
		"money": req.Amount, "currency": strings.ToLower(e.currency),
		"notify_url": notifyURL, "return_url": req.ReturnURL, "name": req.Subject,
	}
	params["sign"] = easyPaySign(params, e.secret)
	params["sign_type"] = signTypeMD5
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		e.baseURL+"/payments/epay/v1/order/create-transaction/submit.php", strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("epusdt EPay request could not be constructed")
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := e.httpClient.Do(request)
	if err != nil {
		// The signed form may already have created a cashier. Preserve its local
		// order and suppress transport text that can include merchant request data.
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "EPUSDT EPay creation could not be confirmed")
	}
	// Release the redirect body without reading or following the cashier page,
	// keeping checkout creation separate from browser navigation and payment selection.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusFound && response.StatusCode != http.StatusSeeOther {
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "EPUSDT EPay cashier redirect was not returned")
	}
	location, err := response.Location()
	base, _ := url.Parse(e.baseURL)
	if err != nil || location.Scheme != "https" || location.Host != base.Host ||
		location.User != nil || location.RawQuery != "" || location.Fragment != "" {
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "EPUSDT EPay cashier destination is invalid")
	}
	prefix := strings.TrimRight(base.Path, "/") + "/pay/checkout-counter/"
	tradeID := strings.TrimPrefix(location.Path, prefix)
	if !strings.HasPrefix(location.Path, prefix) || !epusdtTradeReference(tradeID) {
		return nil, apperror.ServiceUnavailable(payment.CodePaymentCreateUnconfirmed, "EPUSDT EPay cashier reference is invalid")
	}
	return &payment.CreatePaymentResponse{
		TradeNo:  tradeID,
		PayURL:   location.String(),
		Currency: e.currency,
	}, nil
}

// QueryOrder observes the saved opaque gateway trade identifier over HTTPS.
// Cashier details provide fiat amount and currency, while the dedicated status
// endpoint supplies current payment progress. Missing or unrecognizable references
// remain errors, so an uncertain order cannot be silently classified as unpaid.
// Both responses must repeat the requested trade identifier, and invalid fiat
// amounts are rejected before the runtime can interpret a paid observation.
func (e *EPUSDT) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	if !epusdtTradeReference(tradeNo) {
		return nil, fmt.Errorf("epusdt gateway trade reference is unavailable")
	}
	details, err := e.query(ctx, "/pay/checkout-counter-resp/"+url.PathEscape(tradeNo))
	if err != nil || details.Code != http.StatusOK || details.Data.TradeID != tradeNo ||
		!strings.EqualFold(details.Data.Currency, e.currency) {
		return nil, fmt.Errorf("epusdt order details could not be confirmed")
	}
	current, err := e.query(ctx, "/pay/check-status/"+url.PathEscape(tradeNo))
	if err != nil || current.Code != http.StatusOK || current.Data.TradeID != tradeNo {
		return nil, fmt.Errorf("epusdt payment status could not be confirmed")
	}
	amount, err := details.Data.Amount.Float64()
	if err != nil || amount <= 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return nil, fmt.Errorf("epusdt order amount is invalid")
	}
	status := payment.ProviderStatusPending
	switch current.Data.Status {
	case epusdtPaid:
		status = payment.ProviderStatusSuccess
	case epusdtExpired:
		status = payment.ProviderStatusFailed
	case epusdtWaiting, epusdtSelecting:
	default:
		return nil, fmt.Errorf("epusdt payment status is unsupported")
	}
	return &payment.QueryOrderResponse{
		TradeNo:  tradeNo,
		Amount:   amount,
		Status:   status,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// VerifyNotification authenticates an EPay form and confirms its paid cashier.
// The signed money field is the fiat order amount. The merchant, compatibility
// type, trade reference, and queried amount must agree before the payment
// runtime associates this notification with its saved order snapshot.
// A valid signature alone cannot confirm success: its amount must also match
// the independently retrieved fiat amount, and the queried trade must have
// reached the gateway's paid state before any local entitlement can be granted.
// @project-doc docs/domains/payments_and_entitlements.md#epusdt_payments
func (e *EPUSDT) VerifyNotification(ctx context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	params, err := epusdtNotificationParams(rawBody)
	if err != nil {
		return nil, err
	}
	if params["pid"] != e.pid || !easyPayVerifySign(params, e.secret, params["sign"]) {
		return nil, fmt.Errorf("epusdt EPay notification signature is invalid")
	}
	amount, err := easyPayPositiveMoney(params["money"])
	if err != nil || params["trade_status"] != tradeStatusSuccess || params["type"] != "alipay" ||
		!epusdtTradeReference(params["trade_no"]) {
		return nil, fmt.Errorf("epusdt EPay notification payment fields are invalid")
	}
	queried, err := e.QueryOrder(ctx, params["trade_no"])
	if err != nil || queried.Status != payment.ProviderStatusSuccess ||
		!amount.Equal(decimal.NewFromFloat(queried.Amount)) {
		return nil, fmt.Errorf("epusdt EPay notification does not match a paid cashier order")
	}
	paid, _ := amount.Float64()
	return &payment.PaymentNotification{
		OrderID: params["out_trade_no"], TradeNo: params["trade_no"], Amount: paid,
		Status: payment.ProviderStatusSuccess, RawData: rawBody,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// Refund rejects automatic transfers because this EPay integration exposes no
// merchant refund operation. Administrators reconcile external refunds separately;
// configuration prevents enabling automatic or buyer-initiated refunds for EPUSDT.
func (e *EPUSDT) Refund(_ context.Context, _ payment.RefundRequest) (*payment.RefundResponse, error) {
	return nil, fmt.Errorf("epusdt automatic refunds are not supported")
}

// query reads one cashier observation through the shared bounded HTTPS client.
// Redirects and non-success HTTP status codes are rejected before JSON parsing.
// Transport diagnostics omit the opaque payment reference carried by the URL.
func (e *EPUSDT) query(ctx context.Context, path string) (*epusdtReply, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, e.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("epusdt query could not be constructed")
	}
	response, err := e.httpClient.Do(request)
	if err != nil {
		// Query URLs identify a private cashier. Keep transport details out of
		// caller-visible errors while leaving its payment state unresolved for recovery.
		return nil, fmt.Errorf("epusdt query could not be completed")
	}
	// Release the response on every status and parsing path. A close failure
	// cannot establish payment and does not replace the observed query result.
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("epusdt query HTTP status is not successful")
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, epusdtMaxBody+1))
	if err != nil || len(data) > epusdtMaxBody {
		return nil, fmt.Errorf("epusdt query response could not be read")
	}
	var reply epusdtReply
	if err := json.Unmarshal(data, &reply); err != nil {
		return nil, fmt.Errorf("epusdt query response is not valid JSON")
	}
	return &reply, nil
}

// epusdtNotificationParams parses EPay callback fields without ambiguous values.
// Required fields have one nonempty value; optional fields use the same checks.
// The signature joins decoded values with delimiters, so even the product name
// rejects delimiter and control characters before any authenticated field is used.
//
// These checks precede signature verification, including optional fields and
// names. Otherwise distinct decoded forms could share the same concatenated
// signing text and associate a valid signature with different payment fields.
func epusdtNotificationParams(raw string) (map[string]string, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return nil, fmt.Errorf("epusdt EPay notification is not a valid form")
	}
	params := make(map[string]string, len(values))
	for key, entries := range values {
		switch key {
		case "pid", "trade_no", "out_trade_no", "type", "name", "money", "trade_status", "param", "sign", "sign_type":
		default:
			return nil, fmt.Errorf("epusdt EPay notification field is unsupported")
		}
		if len(entries) != 1 || !easyPaySafeNotifyValue(entries[0]) {
			return nil, fmt.Errorf("epusdt EPay notification field is ambiguous")
		}
		params[key] = entries[0]
	}
	for _, key := range []string{"pid", "trade_no", "out_trade_no", "type", "money", "trade_status", "sign"} {
		if params[key] == "" {
			return nil, fmt.Errorf("epusdt EPay notification required field is missing")
		}
	}
	if params["sign_type"] != "" && !strings.EqualFold(params["sign_type"], signTypeMD5) {
		return nil, fmt.Errorf("epusdt EPay notification signing algorithm is unsupported")
	}
	return params, nil
}

// epusdtTradeReference validates one opaque gateway identifier before constructing query paths.
func epusdtTradeReference(value string) bool {
	// Limit identifiers to the gateway namespace and a bounded URL segment.
	if value == "" || len(value) > 128 {
		return false
	}
	for _, ch := range value {
		if ch != '_' && ch != '-' && (ch < 'a' || ch > 'z') &&
			(ch < 'A' || ch > 'Z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}
