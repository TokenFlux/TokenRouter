package provider

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// easypay.go signs EasyPay checkout requests, verifies callbacks, and
// translates payment and refund responses.
// EasyPay hosted checkout signs a browser URL, while API checkout posts the
// same merchant identity and retrieves payment references. Callback processing
// accepts a bounded V1 field set, rejects ambiguous signed values and confirms
// successful notifications through an upstream order query before fulfillment.
//
// Query responses support top-level and nested variants, with field presence
// deciding which representation wins even when the winning value is empty.
// Refund submission tries the merchant order label before the upstream trade
// label and advances only for an order-not-found diagnostic. HTTP status
// acceptance belongs to each response parser, so the creation wrapper can
// accept a successful JSON code from a non-success HTTP response.

// EasyPay protocol values select request modes, paid states, signing text and
// bounded HTTP reads or diagnostic summaries.
const (
	easypayCodeSuccess     = 1                // successful JSON response code
	easypayStatusPaid      = 1                // paid numeric query state
	easypayHTTPTimeout     = 10 * time.Second // default HTTP request timeout
	maxEasypayResponseSize = 1 << 20          // response prefix in bytes
	maxEasypayErrorSummary = 512              // diagnostic prefix in bytes
	easypayAmountTolerance = 0.005            // half-cent confirmation margin
	tradeStatusSuccess     = "TRADE_SUCCESS"  // paid textual trade state
	signTypeMD5            = "MD5"            // outbound signing type label
	paymentModePopup       = "popup"          // exact hosted checkout selector
	deviceMobile           = "mobile"         // mobile request device label
)

// easyPayNotifyAllowedParams contains genuine asynchronous notification fields
// across common V1 implementations. Checkout-only fields stay excluded because
// their reusable signatures can otherwise be spliced into callbacks.
var easyPayNotifyAllowedParams = map[string]struct{}{
	"pid": {}, "trade_no": {}, "out_trade_no": {}, "type": {},
	"name": {}, "money": {}, "trade_status": {}, "param": {},
	"api_trade_no": {}, "addtime": {}, "endtime": {}, "buyer": {},
	"timestamp": {}, "sign": {}, "sign_type": {},
}

// easyPayRefundSuccessTextPattern is the complete plain-text success format
// observed from V1-compatible refund endpoints. Captured money is compared
// with the amount submitted for the current attempt before success is exposed.
var easyPayRefundSuccessTextPattern = regexp.MustCompile(`^退款成功！退款金额¥([0-9]+(?:\.[0-9]{1,2})?)$`)

// EasyPay sends signed checkout, query and refund requests for one configured
// aggregation instance.
type EasyPay struct {
	instanceID string // configured instance identity
	// config owns the constructor copy of string settings; payment operations
	// read it as immutable provider configuration.
	config map[string]string
	// httpClient is reused for requests; a nil client selects the default timeout
	// in postRaw.
	httpClient *http.Client
}

// easyPayCustomMethod maps a visible local method to one upstream form value.
type easyPayCustomMethod struct {
	Type         string `json:"type"`         // visible local method
	UpstreamType string `json:"upstreamType"` // upstream payment type
	DisplayName  string `json:"displayName"`  // configured display text
}

// easyPayQueryData holds optional query fields shared by top-level and nested
// response layouts.
type easyPayQueryData struct {
	// TradeStatus wins over numeric Status when present, including an
	// unrecognized or empty string.
	TradeStatus *string `json:"trade_status"`
	// Status retains numeric or textual JSON for lenient PHP response decoding.
	Status json.RawMessage `json:"status"`
	// Money retains textual or numeric JSON before amount parsing.
	Money json.RawMessage `json:"money"`
	// TradeNo retains the exact upstream payment reference when supplied.
	TradeNo json.RawMessage `json:"trade_no"`
	// OutTradeNo echoes the merchant order identity when supplied by the
	// upstream response.
	OutTradeNo json.RawMessage `json:"out_trade_no"`
}

// easyPayQueryResponse represents both supported JSON layouts while retaining
// presence for precedence decisions.
type easyPayQueryResponse struct {
	easyPayQueryData
	Code any              `json:"code"` // numeric or textual response code
	Msg  string           `json:"msg"`  // query failure diagnostic text
	Data easyPayQueryData `json:"data"` // nested query response fields
}

// easyPayQueryResult retains the normalized upstream state and its exact
// returned amount and trade reference. An absent trade number stays empty so
// callback confirmation can distinguish it from the merchant-order fallback
// exposed by QueryOrder.
type easyPayQueryResult struct {
	status  string // normalized provider order state
	money   string // exact returned amount text
	tradeNo string // exact returned upstream trade reference
	failure string // explicit upstream failure diagnostic, if any
}

// easyPayRefundAttempt pairs a request form with the reference returned on its
// success.
type easyPayRefundAttempt struct {
	// params and refundID describe the same attempt: params contains either
	// merchant order or upstream trade identity, and refundID returns that
	// selected identity on success.
	params   map[string]string // individual refund request form
	refundID string            // selected refund reference label
}

// NewEasyPay validates five nonblank connection settings and copies their
// string map before normalizing apiBase. It installs a ten-second HTTP client;
// API operations report upstream credential or connectivity errors when
// requests run.
func NewEasyPay(instanceID string, config map[string]string) (*EasyPay, error) {
	for _, k := range []string{"pid", "pkey", "apiBase", "notifyUrl", "returnUrl"} {
		if strings.TrimSpace(config[k]) == "" {
			return nil, fmt.Errorf("easypay config missing required key: %s", k)
		}
	}
	cfg := make(map[string]string, len(config))
	maps.Copy(cfg, config)
	cfg["apiBase"] = normalizeEasyPayAPIBase(cfg["apiBase"])
	return &EasyPay{
		instanceID: instanceID,
		config:     cfg,
		httpClient: &http.Client{Timeout: easypayHTTPTimeout},
	}, nil
}

// normalizeEasyPayAPIBase removes recognized operation suffixes from a
// configured service base. A parsed URL with scheme and host loses its query,
// fragment and escaped path before suffix removal, so subsequent operations
// append their own endpoints.
//
// Unparseable or incomplete URLs instead pass through textual suffix trimming.
// This normalization accepts the supplied scheme and host, and callers needing
// destination restrictions apply those rules separately before provider
// construction.
func normalizeEasyPayAPIBase(apiBase string) string {
	base := strings.TrimSpace(apiBase)
	if base == "" {
		return ""
	}
	if parsed, err := url.Parse(base); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.RawQuery = ""
		parsed.Fragment = ""
		parsed.RawPath = ""
		parsed.Path = trimEasyPayEndpointPath(parsed.Path)
		return strings.TrimRight(parsed.String(), "/")
	}
	// Use textual suffix removal when URL components cannot be read; this keeps
	// configured prefixes available to endpoint construction.
	return strings.TrimRight(trimEasyPayEndpointPath(base), "/")
}

// trimEasyPayEndpointPath removes a trailing submit.php, mapi.php or api.php
// suffix case-insensitively after whitespace and slash trimming. Unrecognized
// paths remain available as prefixes for later endpoint construction.
func trimEasyPayEndpointPath(path string) string {
	path = strings.TrimRight(strings.TrimSpace(path), "/")
	lower := strings.ToLower(path)
	for _, endpoint := range []string{"/submit.php", "/mapi.php", "/api.php"} {
		if strings.HasSuffix(lower, endpoint) {
			// Remove only the recognized operation suffix; a preceding installation
			// path remains part of each later endpoint URL.
			return strings.TrimRight(path[:len(path)-len(endpoint)], "/")
		}
	}
	return path
}

// apiBase returns the normalized configured endpoint prefix, or empty for a
// nil provider. It repeats normalization during reads so request construction
// uses the same suffix handling as NewEasyPay.
func (e *EasyPay) apiBase() string {
	if e == nil {
		return ""
	}
	return normalizeEasyPayAPIBase(e.config["apiBase"])
}

// Name returns the fixed display name for this aggregation adapter.
func (e *EasyPay) Name() string { return "EasyPay" }

// ProviderKey returns the registry key identifying EasyPay provider instances.
func (e *EasyPay) ProviderKey() string { return payment.TypeEasyPay }

// SupportedTypes returns a fresh list starting with Alipay and WeChat and
// appends parsed custom local methods. Duplicate custom labels remain in the
// list, while incomplete or malformed custom entries are omitted.
func (e *EasyPay) SupportedTypes() []payment.PaymentType {
	types := []payment.PaymentType{payment.TypeAlipay, payment.TypeWxpay}
	for _, method := range e.customMethods() {
		if method.Type != "" {
			types = append(types, method.Type)
		}
	}
	return types
}

// MerchantIdentityMetadata returns a fresh map containing the trimmed
// configured merchant pid. A nil provider or blank pid returns nil; payment
// confirmation consumes this identity independently of the visible payment
// method.
func (e *EasyPay) MerchantIdentityMetadata() map[string]string {
	if e == nil {
		return nil
	}
	pid := strings.TrimSpace(e.config["pid"])
	if pid == "" {
		return nil
	}
	return map[string]string{"pid": pid}
}

// CreatePayment selects hosted checkout only for the exact configured popup
// value and delegates every other value to API checkout. Hosted construction
// returns a signed URL immediately; API construction uses ctx for its HTTP
// request.
func (e *EasyPay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	mode := e.config["paymentMode"]
	if mode == paymentModePopup {
		return e.createRedirectPayment(req)
	}
	// Use API checkout for every non-popup label, retaining creation error and
	// URL handling at that delegated request implementation.
	return e.createAPIPayment(ctx, req)
}

// createRedirectPayment signs a submit.php browser URL using the selected
// upstream method, merchant label, amount and callback destinations. Request
// destinations override nonempty configured destinations, and the response
// leaves TradeNo empty for later channel observations.
func (e *EasyPay) createRedirectPayment(req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	notifyURL, returnURL := e.resolveURLs(req)
	paymentType := e.upstreamPaymentType(req.PaymentType)
	params := map[string]string{
		"pid": e.config["pid"], "type": paymentType,
		"out_trade_no": req.OrderID, "notify_url": notifyURL,
		"return_url": returnURL, "name": req.Subject,
		"money": req.Amount,
	}
	if cid := e.resolveCID(paymentType); cid != "" {
		params["cid"] = cid
	}
	if req.IsMobile {
		params["device"] = deviceMobile
	}
	// Sign the merchant form before adding sign_type; that algorithm label
	// remains excluded from the shared wire signature.
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5

	q := url.Values{}
	for k, v := range params {
		q.Set(k, v)
	}
	payURL := e.apiBase() + "/submit.php?" + q.Encode()
	return &payment.CreatePaymentResponse{PayURL: payURL}, nil
}

// createAPIPayment posts a signed mapi.php form and returns upstream trade,
// launch and QR references. JSON code one is its acceptance condition because
// post discards HTTP status, while malformed JSON or another code returns an
// error.
//
// A mobile request prefers nonempty payurl2 over payurl. Slash-prefixed launch
// and QR references resolve against the instance base; other references retain
// their exact text so opaque QR payloads and application links remain usable
// by checkout consumers.
func (e *EasyPay) createAPIPayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	notifyURL, returnURL := e.resolveURLs(req)
	paymentType := e.upstreamPaymentType(req.PaymentType)
	params := map[string]string{
		"pid": e.config["pid"], "type": paymentType,
		"out_trade_no": req.OrderID, "notify_url": notifyURL,
		"return_url": returnURL, "name": req.Subject,
		"money": req.Amount, "clientip": req.ClientIP,
	}
	if cid := e.resolveCID(paymentType); cid != "" {
		params["cid"] = cid
	}
	if req.IsMobile {
		params["device"] = deviceMobile
	}
	// Sign the merchant form before adding sign_type; that algorithm label
	// remains excluded from the shared wire signature.
	params["sign"] = easyPaySign(params, e.config["pkey"])
	params["sign_type"] = signTypeMD5

	body, err := e.post(ctx, e.apiBase()+"/mapi.php", params)
	if err != nil {
		return nil, fmt.Errorf("easypay create: %w", err)
	}
	var resp struct {
		Code    int    `json:"code"`     // creation acceptance response code
		Msg     string `json:"msg"`      // creation failure message text
		TradeNo string `json:"trade_no"` // returned upstream trade reference
		PayURL  string `json:"payurl"`   // primary payment launch reference
		// PayURL2 replaces PayURL when the request is mobile and this returned
		// alternative is nonempty.
		PayURL2 string `json:"payurl2"`
		QRCode  string `json:"qrcode"` // returned QR payment payload
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("easypay parse: %w", err)
	}
	if resp.Code != easypayCodeSuccess {
		return nil, fmt.Errorf("easypay error: %s", resp.Msg)
	}
	payURL := resp.PayURL
	// Prefer the mobile launch reference before resolving relative paths, so
	// either URL variant uses the same base normalization.
	if req.IsMobile && resp.PayURL2 != "" {
		payURL = resp.PayURL2
	}
	base := e.apiBase()
	return &payment.CreatePaymentResponse{
		TradeNo: resp.TradeNo,
		PayURL:  resolveEasyPayReturnedRef(base, payURL),
		QRCode:  resolveEasyPayReturnedRef(base, resp.QRCode),
	}, nil
}

// resolveEasyPayReturnedRef resolves slash-prefixed response references
// against a usable service base. It trims only for parsing and prefix
// detection, and returns the supplied text unchanged for opaque tokens,
// application links or unusable URLs.
//
// A root path inherits the base origin, whereas a protocol-relative reference
// can supply another host while inheriting the base scheme. Checkout callers
// therefore receive the upstream reference format rather than an enforced
// same-origin destination, and keep any destination policy at their own
// validation step.
func resolveEasyPayReturnedRef(apiBase, ref string) string {
	trimmed := strings.TrimSpace(ref)
	if !strings.HasPrefix(trimmed, "/") {
		return ref
	}
	base, err := url.Parse(strings.TrimSpace(apiBase))
	// Retain the upstream reference when the base lacks usable URL components,
	// avoiding construction of a misleading launch destination.
	if err != nil || base.Scheme == "" || base.Host == "" {
		return ref
	}
	parsed, err := url.Parse(trimmed)
	// Retain a reference that cannot participate in relative resolution, leaving
	// upstream payload interpretation to its checkout consumer.
	if err != nil || parsed.Scheme != "" {
		return ref
	}
	return base.ResolveReference(parsed).String()
}

// resolveURLs returns notification and return destinations, taking each
// nonempty request value before its configured value. The check uses literal
// emptiness, so whitespace supplied by a caller also overrides configuration.
func (e *EasyPay) resolveURLs(req payment.CreatePaymentRequest) (string, string) {
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = e.config["notifyUrl"]
	}
	returnURL := req.ReturnURL
	if returnURL == "" {
		returnURL = e.config["returnUrl"]
	}
	return notifyURL, returnURL
}

// customMethods decodes custom mappings into a fresh slice and trims each
// local code, upstream code and display label. Missing or malformed
// configuration returns nil; entries missing either code are skipped without
// validating the remaining codes here.
func (e *EasyPay) customMethods() []easyPayCustomMethod {
	if e == nil {
		return nil
	}
	raw := strings.TrimSpace(e.config["customMethods"])
	if raw == "" {
		return nil
	}
	var methods []easyPayCustomMethod
	// Discard malformed custom configuration as an empty mapping list, so
	// built-in advertised methods remain available to registry consumers.
	if err := json.Unmarshal([]byte(raw), &methods); err != nil {
		return nil
	}
	result := make([]easyPayCustomMethod, 0, len(methods))
	for _, method := range methods {
		method.Type = strings.TrimSpace(method.Type)
		method.UpstreamType = strings.TrimSpace(method.UpstreamType)
		method.DisplayName = strings.TrimSpace(method.DisplayName)
		// Skip incomplete mappings before advertisement and request translation; a
		// visible code needs a corresponding upstream form value here.
		if method.Type == "" || method.UpstreamType == "" {
			continue
		}
		result = append(result, method)
	}
	return result
}

// upstreamPaymentType returns the first custom mapping matching the trimmed
// local method. When no mapping matches it returns that trimmed method
// directly, allowing built-in labels to become upstream form values.
func (e *EasyPay) upstreamPaymentType(paymentType string) string {
	paymentType = strings.TrimSpace(paymentType)
	for _, method := range e.customMethods() {
		if paymentType == method.Type {
			return method.UpstreamType
		}
	}
	// Unmatched visible labels pass directly to the upstream form, so the two
	// built-in methods need no custom mapping entry.
	return paymentType
}

// QueryOrder looks up a merchant order and exposes the normalized paid state,
// amount and upstream reference. When the response omits trade_no, the
// requested merchant label remains the public fallback.
func (e *EasyPay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	result, err := e.queryOrder(ctx, tradeNo)
	if err != nil {
		return nil, err
	}
	responseTradeNo := tradeNo
	if result.tradeNo != "" {
		responseTradeNo = result.tradeNo
	}
	amount, _ := strconv.ParseFloat(result.money, 64)
	return &payment.QueryOrderResponse{
		TradeNo:  responseTradeNo,
		Status:   result.status,
		Amount:   amount,
		Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// queryOrder sends lookup parameters in both the query and form body because
// V1-compatible PHP deployments differ between reading GET and POST. It
// strips credential-bearing request URLs from transport errors before parsing
// the upstream response.
func (e *EasyPay) queryOrder(ctx context.Context, outTradeNo string) (*easyPayQueryResult, error) {
	params := map[string]string{
		"act": "order", "pid": e.config["pid"],
		"key": e.config["pkey"], "out_trade_no": outTradeNo,
	}
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}
	body, statusCode, err := e.postRaw(ctx, e.apiBase()+"/api.php?"+query.Encode(), params)
	if err != nil {
		if urlErr, ok := errors.AsType[*url.Error](err); ok {
			err = urlErr.Err
		}
		return nil, fmt.Errorf("easypay query: %w", err)
	}
	return parseEasyPayQueryResponse(statusCode, body, outTradeNo)
}

// parseEasyPayQueryResponse accepts successful HTTP JSON objects and selects
// status, money and trade identity from optional top-level or nested fields.
// An explicit failure code becomes a pending observation with a retained
// diagnostic, while a different echoed merchant order rejects the response.
func parseEasyPayQueryResponse(statusCode int, body []byte, outTradeNo string) (*easyPayQueryResult, error) {
	if statusCode < http.StatusOK || statusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("easypay query HTTP %d", statusCode)
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, fmt.Errorf("easypay query empty response (HTTP %d)", statusCode)
	}
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") ||
		(strings.HasPrefix(lower, "<") && strings.Contains(lower, "html")) {
		return nil, fmt.Errorf("easypay query non-JSON response (HTTP %d)", statusCode)
	}
	if !strings.HasPrefix(trimmed, "{") {
		return nil, fmt.Errorf("easypay query non-JSON response (HTTP %d)", statusCode)
	}

	var resp easyPayQueryResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("easypay query non-JSON response (HTTP %d)", statusCode)
	}
	if resp.Code != nil && !easyPayResponseCodeIsSuccess(resp.Code) {
		msg := strings.TrimSpace(resp.Msg)
		if msg == "" {
			msg = summarizeEasyPayResponse(body)
		}
		// QueryOrder is also used as a reachability probe with a fresh random
		// order number. Preserve explicit upstream failures as pending observations
		// there, while ConfirmNotification rejects the retained diagnostic before
		// fulfillment.
		return &easyPayQueryResult{status: payment.ProviderStatusPending, failure: msg}, nil
	}
	echoed := easyPayJSONScalar(pickEasyPayJSON(resp.OutTradeNo, resp.Data.OutTradeNo))
	if echoed != "" && echoed != strings.TrimSpace(outTradeNo) {
		return nil, fmt.Errorf("easypay query out_trade_no mismatch: expected %s, got %s", strings.TrimSpace(outTradeNo), echoed)
	}
	// Choose by field presence in descending precedence; a present unknown state
	// deliberately prevents another response layout from claiming paid.
	status := payment.ProviderStatusPending
	if resp.TradeStatus != nil {
		if *resp.TradeStatus == tradeStatusSuccess {
			status = payment.ProviderStatusPaid
		}
	} else if resp.Data.TradeStatus != nil {
		if *resp.Data.TradeStatus == tradeStatusSuccess {
			status = payment.ProviderStatusPaid
		}
	} else if raw := pickEasyPayJSON(resp.Status, resp.Data.Status); raw != nil {
		if easyPayJSONScalar(raw) == strconv.Itoa(easypayStatusPaid) {
			status = payment.ProviderStatusPaid
		}
	}

	return &easyPayQueryResult{
		status: status,
		money:  easyPayJSONScalar(pickEasyPayJSON(resp.Money, resp.Data.Money)),
		tradeNo: easyPayJSONScalar(
			pickEasyPayJSON(resp.TradeNo, resp.Data.TradeNo),
		),
	}, nil
}

// ConfirmNotification is called for a verified EasyPay success notification
// after webhook handling establishes that its local order exists. It queries
// the same merchant configuration that verified the callback; paid state,
// amount and any returned upstream trade number must agree. On success it may
// fill the notification's TradeNo before local fulfillment, while every error
// leaves the local order unchanged and asks the gateway to retry the callback.
func (e *EasyPay) ConfirmNotification(ctx context.Context, notification *payment.PaymentNotification) error {
	if notification == nil || notification.Status != payment.NotificationStatusSuccess {
		return nil
	}
	result, err := e.queryOrder(ctx, notification.OrderID)
	if err != nil {
		return fmt.Errorf("easypay confirm: %w", err)
	}
	if result.failure != "" {
		return fmt.Errorf("easypay confirm: query failed: %s", result.failure)
	}
	if result.status != payment.ProviderStatusPaid {
		return fmt.Errorf("easypay confirm: upstream reports order %s as not paid", notification.OrderID)
	}
	queriedAmount, err := strconv.ParseFloat(result.money, 64)
	if err != nil || !payment.IsValidProviderAmount(queriedAmount) ||
		math.Abs(queriedAmount-notification.Amount) > easypayAmountTolerance {
		return fmt.Errorf("easypay confirm: amount mismatch: notified %v, upstream %q", notification.Amount, result.money)
	}
	if result.tradeNo != "" {
		if notification.TradeNo != "" && notification.TradeNo != result.tradeNo {
			return fmt.Errorf("easypay confirm: trade_no mismatch: notified %s, upstream %s", notification.TradeNo, result.tradeNo)
		}
		notification.TradeNo = result.tradeNo
	}
	return nil
}

// easyPayJSONScalar decodes a JSON string or number into trimmed text. Other
// JSON types, absent fields and null return empty text.
func easyPayJSONScalar(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return strings.TrimSpace(text)
	}
	var number json.Number
	if err := json.Unmarshal(raw, &number); err == nil {
		return number.String()
	}
	return ""
}

// pickEasyPayJSON returns a present non-null top-level field before its nested
// equivalent. When neither exists it returns nil.
func pickEasyPayJSON(top, nested json.RawMessage) json.RawMessage {
	if len(top) > 0 && string(top) != "null" {
		return top
	}
	if len(nested) > 0 && string(nested) != "null" {
		return nested
	}
	return nil
}

// VerifyNotification validates EasyPay form structure, merchant identity,
// positive finite money and the computed signature before returning a callback
// observation. It accepts a bounded V1 notification field set with exactly one
// value per key. All fields except the merchant-defined product name reject
// delimiters that could make one signed value represent multiple parameters.
//
// Signing sorts nonempty parameters and excludes sign and sign_type. Signature
// text must equal the computed lowercase MD5 hex exactly; sign_type is
// accepted but does not select another algorithm. The adapter adds no
// timestamp window, and the payment core subsequently associates the merchant
// order label and validates the amount against local state.
// @project-doc docs/domains/payments_and_entitlements.md#callback_security
func (e *EasyPay) VerifyNotification(_ context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("parse notify: %w", err)
	}
	// Build one accepted value per key before signing; otherwise parsed field
	// selection and signature input could describe different callback data.
	params := make(map[string]string, len(values))
	for key, entries := range values {
		if _, allowed := easyPayNotifyAllowedParams[key]; !allowed {
			return nil, fmt.Errorf("unexpected notify param: %s", key)
		}
		// Reject duplicate values before extracting the first entry, keeping the
		// signed form identical to the callback fields later consumed.
		if len(entries) != 1 {
			return nil, fmt.Errorf("duplicate notify param: %s", key)
		}
		value := entries[0]
		if key != "name" &&
			(strings.ContainsAny(value, "&=\x00\r\n") || strings.TrimSpace(value) != value) {
			return nil, fmt.Errorf("invalid notify param: %s", key)
		}
		params[key] = value
	}
	// Constrain required identifiers, type, money and state before raw pair
	// concatenation. Product name remains the only free-text field.
	for _, key := range []string{"pid", "trade_no", "out_trade_no", "type", "money", "trade_status"} {
		value := params[key]
		if value == "" {
			return nil, fmt.Errorf("missing notify param: %s", key)
		}
		if strings.ContainsAny(value, "&=\x00\r\n") || strings.TrimSpace(value) != value {
			return nil, fmt.Errorf("invalid notify param: %s", key)
		}
	}
	if !payment.ConfigEasyPayCustomMethodCodePattern.MatchString(params["type"]) {
		return nil, fmt.Errorf("invalid notify param: type")
	}
	if params["pid"] != strings.TrimSpace(e.config["pid"]) {
		return nil, fmt.Errorf("easypay notify pid mismatch")
	}
	amount, err := strconv.ParseFloat(params["money"], 64)
	if err != nil || !payment.IsValidProviderAmount(amount) {
		return nil, fmt.Errorf("invalid notify param: money")
	}
	sign := params["sign"]
	if sign == "" {
		return nil, fmt.Errorf("missing sign")
	}
	if !easyPayVerifySign(params, e.config["pkey"], sign) {
		return nil, fmt.Errorf("invalid signature")
	}
	status := payment.ProviderStatusFailed
	if params["trade_status"] == tradeStatusSuccess {
		status = payment.ProviderStatusSuccess
	}
	return &payment.PaymentNotification{
		TradeNo: params["trade_no"], OrderID: params["out_trade_no"],
		Amount: amount, Status: status, RawData: rawBody, Metadata: e.MerchantIdentityMetadata(),
	}, nil
}

// Refund submits available merchant-order and upstream-trade forms in that
// order, returning the identity of the successful attempt. A transport error
// stops submission immediately with pending progress because request outcome
// is unknown; a parsed order-not-found diagnostic permits the next form, while
// other explicit failures return at once.
//
// Each form carries the requested amount but omits the workflow OperationID.
// The alternative is another order lookup representation, so callers should
// not infer request-level deduplication from the returned RefundID. A
// successful parser produces success status, and an exhausted attempt sequence
// returns its first parsed error.
func (e *EasyPay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	attempts := e.refundAttempts(req)
	if len(attempts) == 0 {
		return nil, fmt.Errorf("easypay refund missing order identifier")
	}
	var firstErr error
	for i, attempt := range attempts {
		body, status, err := e.postRaw(ctx, e.apiBase()+"/api.php?act=refund", attempt.params)
		if err != nil {
			return &payment.RefundResponse{
				RefundID: attempt.refundID,
				Status:   payment.ProviderStatusPending,
			}, fmt.Errorf("easypay refund request outcome unknown: %w", err)
		}
		refundStatus, err := parseEasyPayRefundResponse(status, body, req.Amount)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			// A missing-order diagnostic permits the other identifier representation;
			// transport errors and other channel rejections do not reach this retry.
			if i+1 < len(attempts) && isEasyPayRefundOrderNotFound(err) {
				continue
			}
			return nil, err
		}
		return &payment.RefundResponse{RefundID: attempt.refundID, Status: refundStatus}, nil
	}
	return nil, firstErr
}

// refundAttempts builds independent request maps for nonblank merchant order
// and upstream trade references, in that order. Each returned refundID is the
// trimmed identifier used by its form, including when both labels happen to
// have equal text.
//
// The shared merchant credentials and amount are copied into each map before
// adding its identity field. This keeps the alternative forms separate for
// Refund while leaving upstream repeat-submission behavior to the service;
// local OperationID and refund request identity are not added by this adapter.
func (e *EasyPay) refundAttempts(req payment.RefundRequest) []easyPayRefundAttempt {
	base := map[string]string{
		"pid": e.config["pid"], "key": e.config["pkey"], "money": req.Amount,
	}
	var attempts []easyPayRefundAttempt
	if orderID := strings.TrimSpace(req.OrderID); orderID != "" {
		params := cloneStringMap(base)
		params["out_trade_no"] = orderID
		attempts = append(attempts, easyPayRefundAttempt{params: params, refundID: orderID})
	}
	if tradeNo := strings.TrimSpace(req.TradeNo); tradeNo != "" {
		params := cloneStringMap(base)
		params["trade_no"] = tradeNo
		attempts = append(attempts, easyPayRefundAttempt{params: params, refundID: tradeNo})
	}
	return attempts
}

// cloneStringMap returns a fresh map containing the supplied string entries.
func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	maps.Copy(out, in)
	return out
}

// isEasyPayRefundOrderNotFound reports whether an error contains one of the
// accepted Chinese or case-insensitive English missing-order phrases. Refund
// uses this diagnostic classification to permit its alternative form; nil
// returns false.
func isEasyPayRefundOrderNotFound(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	lower := strings.ToLower(msg)
	return strings.Contains(msg, "订单编号不存在") ||
		strings.Contains(msg, "订单不存在") ||
		strings.Contains(lower, "order not found") ||
		strings.Contains(lower, "not exist")
}

// parseEasyPayRefundResponse classifies one submitted refund response as
// success, pending manual verification, or explicit failure. JSON code one and
// exact plain acknowledgements establish success. The complete Chinese success
// format must report the submitted amount; other successful HTTP responses are
// pending because they do not prove that this attempt completed. Transport and
// non-2xx outcomes are also pending at the adapter boundary; documented JSON
// code zero responses are explicit failures.
func parseEasyPayRefundResponse(status int, body []byte, expectedAmount string) (string, error) {
	summary := summarizeEasyPayResponse(body)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return payment.ProviderStatusPending, nil
	}

	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return payment.ProviderStatusPending, nil
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "<!doctype html") || strings.HasPrefix(lower, "<html") ||
		(strings.HasPrefix(lower, "<") && strings.Contains(lower, "html")) {
		return payment.ProviderStatusPending, nil
	}

	if strings.HasPrefix(trimmed, "{") {
		var resp struct {
			Code any `json:"code"` // numeric or textual response code
			// Msg supplies the V1 refund result, optional returned money, or failure
			// diagnostic. Code zero remains failure regardless of success-looking text.
			Msg string `json:"msg"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return payment.ProviderStatusPending, nil
		}
		if easyPayResponseCodeIsSuccess(resp.Code) {
			if easyPayRefundTextReportsAmount(resp.Msg) && !easyPayRefundTextIsSuccess(resp.Msg, expectedAmount) {
				return payment.ProviderStatusPending, nil
			}
			return payment.ProviderStatusSuccess, nil
		}
		if easyPayResponseCodeIsZero(resp.Code) {
			msg := strings.TrimSpace(resp.Msg)
			if msg == "" {
				// Use the bounded body diagnostic when JSON contains no usable message,
				// retaining context for refund failure classification.
				msg = summary
			}
			return "", fmt.Errorf("easypay refund failed (HTTP %d): %s", status, msg)
		}
		return payment.ProviderStatusPending, nil
	}
	if easyPayRefundTextIsSuccess(trimmed, expectedAmount) {
		return payment.ProviderStatusSuccess, nil
	}
	return payment.ProviderStatusPending, nil
}

// easyPayResponseCodeIsSuccess reports whether JSON code is exactly numeric or
// textual one. Fractional numbers and other decoded types are rejected.
func easyPayResponseCodeIsSuccess(code any) bool {
	switch v := code.(type) {
	case float64:
		return v == easypayCodeSuccess
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return err == nil && n == easypayCodeSuccess
	default:
		return false
	}
}

// easyPayResponseCodeIsZero reports whether JSON code is exactly numeric or
// textual zero. Refund handling treats this documented result as failure even
// when its free-text diagnostic contains success-looking words.
func easyPayResponseCodeIsZero(code any) bool {
	switch v := code.(type) {
	case float64:
		return v == 0
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		return err == nil && n == 0
	default:
		return false
	}
}

// easyPayRefundTextIsSuccess recognizes exact V1 refund-success text after
// trimming whitespace and an optional UTF-8 byte-order mark. English tokens
// carry no amount. The observed Chinese format must match completely and its
// reported money must equal the amount submitted for this refund attempt.
func easyPayRefundTextIsSuccess(value, expectedAmount string) bool {
	normalized := strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
	switch strings.ToLower(normalized) {
	case "1", "ok", "success":
		return true
	}
	matches := easyPayRefundSuccessTextPattern.FindStringSubmatch(normalized)
	if len(matches) != 2 {
		return false
	}
	reported, reportedErr := strconv.ParseFloat(matches[1], 64)
	expected, expectedErr := strconv.ParseFloat(strings.TrimSpace(expectedAmount), 64)
	return reportedErr == nil && expectedErr == nil &&
		payment.IsValidProviderAmount(reported) && payment.IsValidProviderAmount(expected) &&
		math.Abs(reported-expected) <= easypayAmountTolerance
}

// easyPayRefundTextReportsAmount reports whether text claims the observed
// Chinese success format, including malformed or mismatched variants. JSON
// code one uses this to keep a contradictory amount pending instead of letting
// the status code override the supplied money.
func easyPayRefundTextReportsAmount(value string) bool {
	normalized := strings.TrimSpace(strings.TrimPrefix(value, "\ufeff"))
	return strings.HasPrefix(normalized, "退款成功！退款金额¥")
}

// summarizeEasyPayResponse collapses body whitespace and uses an empty marker
// for absent text. Long summaries keep a 512-byte prefix shortened to valid
// UTF-8, then add an ellipsis; the text is not credential-filtered.
func summarizeEasyPayResponse(body []byte) string {
	summary := strings.Join(strings.Fields(string(body)), " ")
	if summary == "" {
		return "<empty>"
	}
	if len(summary) > maxEasypayErrorSummary {
		// Cap the collapsed diagnostic by bytes, then shorten an incomplete UTF-8
		// prefix before returning it with the ellipsis.
		truncated := summary[:maxEasypayErrorSummary]
		for truncated != "" && !utf8.ValidString(truncated) {
			truncated = truncated[:len(truncated)-1]
		}
		return truncated + "..."
	}
	return summary
}

// resolveCID prefers cidAlipay for upstream types beginning with alipay and
// cidWxpay for every other type. Each branch uses generic cid when its
// selected override is empty, retaining the configured string unchanged.
func (e *EasyPay) resolveCID(paymentType string) string {
	if strings.HasPrefix(paymentType, "alipay") {
		if v := e.config["cidAlipay"]; v != "" {
			return v
		}
		// Use the shared channel ID when the Alipay-specific override is empty,
		// maintaining a destination for that upstream method family.
		return e.config["cid"]
	}
	if v := e.config["cidWxpay"]; v != "" {
		return v
	}
	// Use the shared channel ID when the non-Alipay override is empty; custom
	// upstream methods follow this same branch.
	return e.config["cid"]
}

// post returns the body and transport or read error from postRaw while
// discarding HTTP status. createAPIPayment consequently determines acceptance
// from its JSON parser, including bodies returned with an HTTP error status.
func (e *EasyPay) post(ctx context.Context, endpoint string, params map[string]string) ([]byte, error) {
	body, _, err := e.postRaw(ctx, endpoint, params)
	return body, err
}

// postRaw sends a form request using ctx, the configured client or a
// ten-second default, and closes the response body. Reads stop at one MiB,
// returning that prefix and HTTP status for operation-specific parsing; an
// oversized body is not independently rejected.
func (e *EasyPay) postRaw(ctx context.Context, endpoint string, params map[string]string) ([]byte, int, error) {
	form := url.Values{}
	for k, v := range params {
		form.Set(k, v)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := e.httpClient
	if client == nil {
		// A nil injected client still receives the constructor timeout policy, so
		// direct provider fixtures retain bounded HTTP request duration.
		client = &http.Client{Timeout: easypayHTTPTimeout}
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	// Close the response after reading its prefix; close errors are discarded
	// because parsing uses the obtained body and read error.
	defer func() { _ = resp.Body.Close() }()
	// Read at most one MiB for operation parsers; a larger response becomes a
	// prefix rather than an explicit oversize failure.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	return body, resp.StatusCode, nil
}

// easyPaySign produces the lowercase MD5 hex of sorted, nonempty form pairs
// followed by the merchant secret. sign and sign_type are excluded, and
// retained values are joined as raw key=value text rather than URL-escaped
// form text.
//
// The callback validator constrains structured values before this shared
// signing step, while creation supplies its own request values. Consequently
// free text and delimiter handling belong to the callers using this wire
// format, and changing the sort or escaping policy would change verification
// of the same parameters.
func easyPaySign(params map[string]string, pkey string) string {
	keys := make([]string, 0, len(params))
	for k, v := range params {
		if k == "sign" || k == "sign_type" || v == "" {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf strings.Builder
	// Builder writes cannot fail for their accepted inputs, so discarded write
	// results leave sorted raw pairs followed by the secret.
	for i, k := range keys {
		if i > 0 {
			_ = buf.WriteByte('&')
		}
		_, _ = buf.WriteString(k + "=" + params[k])
	}
	_, _ = buf.WriteString(pkey)
	hash := md5.Sum([]byte(buf.String()))
	return hex.EncodeToString(hash[:])
}

// easyPayVerifySign compares computed and supplied signature text using
// constant-time byte comparison. It performs no trimming or case
// normalization, so callers supply the exact lowercase hexadecimal text
// emitted by easyPaySign.
func easyPayVerifySign(params map[string]string, pkey string, sign string) bool {
	return hmac.Equal([]byte(easyPaySign(params, pkey)), []byte(sign))
}
