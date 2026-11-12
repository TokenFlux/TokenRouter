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
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// easypay.go signs EasyPay checkout requests, verifies callbacks, and
// translates payment and refund responses.
// EasyPay hosted checkout signs a browser URL, while API checkout posts the
// same merchant identity and retrieves payment references. Callback processing
// validates one value per accepted field before checking its signature; a
// successful signature triggers a query using that same merchant instance.
// Paid state, money, and trade identity must agree before the payment core
// checks the local order snapshot and grants its entitlements.
// An instance can opt into merchant-number echoes from API checkout. Paid
// queries then require explicit merchant and order identity before carrying
// permission to replace that echo during the first payment confirmation.
//
// Query responses support top-level and nested variants, with pointer presence
// deciding which representation wins even when the winning value is empty.
// Refund submission tries the merchant order label before the upstream trade
// label and advances only for an explicit order-not-found rejection. Unknown
// outcomes keep the workflow's prepared deduction available for recovery.
// Compensation requires a recognized business rejection; generic system or
// service failures cannot establish whether external money moved.
// HTTP status acceptance belongs to each response parser. The creation wrapper
// can accept a successful JSON code from a non-success HTTP response.

// EasyPay protocol values select request modes, paid states, signing text and
// bounded HTTP reads or diagnostic summaries.
const (
	easypayCodeSuccess     = 1                // successful JSON response code
	easypayStatusPaid      = 1                // paid numeric query state
	easypayHTTPTimeout     = 10 * time.Second // default HTTP request timeout
	maxEasypayResponseSize = 1 << 20          // maximum response bytes
	maxEasypayErrorSummary = 512              // diagnostic prefix in bytes
	tradeStatusSuccess     = "TRADE_SUCCESS"  // paid textual trade state
	signTypeMD5            = "MD5"            // outbound signing type label
	paymentModePopup       = "popup"          // exact hosted checkout selector
	deviceMobile           = "mobile"         // mobile request device label
)

var (
	// Decimal syntax excludes exponents before monetary validation, bounding work
	// on provider-controlled values used in payment and refund comparisons.
	easyPayMoneyPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)

	// Refund patterns recognize complete success messages and conflicting or
	// uncertain diagnostics. Recognized business rejections separately authorize compensation.
	easyPayRefundSuccessPattern     = regexp.MustCompile(`^退款成功[！!。.]?(?:\s*退款金额\s*[:：]?\s*[¥￥]?\s*([0-9]+(?:\.[0-9]{1,2})?)\s*元?[！!。.]?)?$`)
	easyPayRefundFailureHintPattern = regexp.MustCompile(`(?i)失败|不存在|不足|未开启|未开通|无权限|权限不足|不支持|不允许|错误|拒绝|无效|不正确|fail|error|denied|invalid|not found|not exist|insufficient|disabled`)
	easyPayRefundUnconfirmedPattern = regexp.MustCompile(`(?i)申请|受理|处理中|等待|稍后|超时|网络|连接|查询|未知|系统|异常|繁忙|不可用|故障|pending|processing|accepted|submitted|timeout|timed out|network|connection|query|unknown|system|internal|server|service|unavailable|busy|exception`)
	errEasyPayRefundOrderNotFound   = errors.New("easypay refund order not found")
)

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

// easyPayQueryData holds optional nested query fields considered after their
// top-level counterparts.
type easyPayQueryData struct {
	PID     json.RawMessage `json:"pid"`          // optional reported merchant identity
	OrderID *string         `json:"out_trade_no"` // optional reported merchant order
	// TradeStatus wins over numeric Status when top-level TradeStatus is absent,
	// including an unrecognized nested string.
	TradeStatus *string `json:"trade_status"`
	// Status supplies a nested numeric state only after both textual states and
	// top-level Status are absent.
	Status *int `json:"status"`
	// Money supplies amount text when top-level Money is absent; an empty
	// top-level string blocks this value.
	Money *string `json:"money"`
	// TradeNo supplies the upstream transaction only when the top-level field
	// is absent; paid responses require the selected reference to be nonempty.
	TradeNo *string `json:"trade_no"`
}

// easyPayQueryResponse represents both supported JSON layouts while retaining
// presence for precedence decisions.
type easyPayQueryResponse struct {
	Code    json.Number     `json:"code"`         // numeric or textual business code
	Msg     string          `json:"msg"`          // channel business result diagnostic
	PID     json.RawMessage `json:"pid"`          // optional reported merchant identity
	OrderID *string         `json:"out_trade_no"` // optional reported merchant order
	// TradeStatus has first status precedence whenever present; an unrecognized
	// or empty value keeps the result pending.
	TradeStatus *string `json:"trade_status"`
	// Status is considered after both textual states are absent, and presence
	// blocks nested numeric Status.
	Status *int `json:"status"`
	// Money has amount precedence whenever present, including empty text that
	// is rejected for a paid observation.
	Money *string `json:"money"`
	// TradeNo presence blocks nested TradeNo; paid observations require a
	// nonempty upstream reference for callback comparison and future refunds.
	TradeNo *string          `json:"trade_no"`
	Data    easyPayQueryData `json:"data"` // nested query response fields
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
// requests run. The optional merchant-number compatibility setting accepts
// only an empty value or the exact strings true and false.
func NewEasyPay(instanceID string, config map[string]string) (*EasyPay, error) {
	if err := payment.ConfigValidateEasyPayTradeNumberCompatibility(config); err != nil {
		return nil, err
	}
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
// With merchant-number compatibility enabled, an exact echoed order number
// leaves TradeNo empty so later verified confirmation can bind its real reference.
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
	tradeNo := resp.TradeNo
	if e.config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] == "true" && tradeNo == req.OrderID {
		// An echoed merchant number cannot bind a provider transaction. Leave the
		// reference absent so verified payment confirmation can publish the real number.
		tradeNo = ""
	}
	return &payment.CreatePaymentResponse{
		TradeNo: tradeNo,
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

// QueryOrder posts a merchant order lookup using the instance's credentials.
// Identical query and form parameters support V1 servers reading either input;
// ctx cancels the request. Transport failures expose a fixed diagnostic, with
// cancellation and deadline identities retained for callers checking errors.Is.
// Enabled merchant-number compatibility requires explicit identities on paid
// responses before issuing internal permission for the first transaction binding.
func (e *EasyPay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	params := map[string]string{
		"act": "order", "pid": e.config["pid"],
		"key": e.config["pkey"], "out_trade_no": tradeNo,
	}
	query := url.Values{}
	for key, value := range params {
		query.Set(key, value)
	}
	body, statusCode, err := e.postRaw(ctx, e.apiBase()+"/api.php?"+query.Encode(), params)
	if err != nil {
		// Both outer request errors and nested redirect diagnostics can embed the
		// merchant key. Expose known context sentinels or a fixed message so neither
		// formatted diagnostics nor an unwrap chain can reveal those request values.
		if errors.Is(err, context.Canceled) {
			return nil, fmt.Errorf("easypay query: %w", context.Canceled)
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("easypay query: %w", context.DeadlineExceeded)
		}
		return nil, errors.New("easypay query request failed")
	}
	return e.parseQueryResponse(statusCode, body, tradeNo)
}

// parseQueryResponse validates a successful V1 business response and
// resolves its optional top-level and nested fields. Textual status takes
// precedence over numeric status; a present unknown value remains pending.
//
// Every supplied merchant or order identity is checked against the authenticated
// lookup, including nested identities shadowed by top-level values. Paid results
// require positive CNY money and an actual upstream trade number. Empty or
// malformed paid fields return errors before polling or callback consumers can
// authorize fulfillment, while pending observations may omit payment details.
// Enabled merchant-number compatibility requires explicit matching identities
// in paid responses. Only those verified responses receive the internal order
// marker that authorizes replacing a previously stored merchant-number echo.
func (e *EasyPay) parseQueryResponse(statusCode int, body []byte, orderID string) (*payment.QueryOrderResponse, error) {
	metadata := e.MerchantIdentityMetadata()
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
	if !easyPayResponseCodeIsSuccess(resp.Code) {
		// A known missing-order reply proves connectivity for synthetic draft probes,
		// but remains an error for real payment confirmation and order recovery.
		if code, valid := easyPayIntegerCode(resp.Code); valid && code != easypayCodeSuccess && easyPayMissingOrderMessage(resp.Msg) {
			return nil, payment.ErrProviderOrderNotFound
		}
		return nil, fmt.Errorf("easypay query unsuccessful business response")
	}
	hasMerchant := false
	for _, pid := range []json.RawMessage{resp.PID, resp.Data.PID} {
		if len(pid) == 0 {
			continue
		}
		var merchant string
		if err := json.Unmarshal(pid, &merchant); err != nil {
			merchant = string(pid)
		}
		if merchant != metadata["pid"] {
			return nil, fmt.Errorf("easypay query merchant mismatch")
		}
		hasMerchant = true
	}
	hasOrder := false
	for _, returnedOrder := range []*string{resp.OrderID, resp.Data.OrderID} {
		if returnedOrder == nil {
			continue
		}
		if *returnedOrder != orderID {
			return nil, fmt.Errorf("easypay query order mismatch")
		}
		hasOrder = true
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
	} else if resp.Status != nil {
		if *resp.Status == easypayStatusPaid {
			status = payment.ProviderStatusPaid
		}
	} else if resp.Data.Status != nil && *resp.Data.Status == easypayStatusPaid {
		status = payment.ProviderStatusPaid
	}

	// Select money and trade references independently of status; a present empty
	// top-level value blocks its nested alternative.
	money := ""
	if resp.Money != nil {
		money = *resp.Money
	} else if resp.Data.Money != nil {
		money = *resp.Data.Money
	}
	responseTradeNo := ""
	if resp.TradeNo != nil {
		if *resp.TradeNo != "" {
			responseTradeNo = *resp.TradeNo
		}
	} else if resp.Data.TradeNo != nil && *resp.Data.TradeNo != "" {
		responseTradeNo = *resp.Data.TradeNo
	}

	amount, _ := strconv.ParseFloat(money, 64)
	if status == payment.ProviderStatusPaid {
		paid, err := easyPayPositiveMoney(money)
		if err != nil {
			return nil, fmt.Errorf("easypay query invalid paid amount")
		}
		amount, _ = paid.Float64()
		if responseTradeNo == "" || !easyPaySafeNotifyValue(responseTradeNo) {
			return nil, fmt.Errorf("easypay query missing or invalid trade number")
		}
		if e.config[payment.ConfigEasyPayAllowMerchantOrderNoAsTradeNo] == "true" {
			if !hasMerchant || !hasOrder || orderID == "" {
				return nil, fmt.Errorf("easypay compatibility requires explicit merchant and order identity")
			}
			// Derive this marker from the authenticated lookup and local configuration.
			// Callback fields never supply the permission to replace an order's initial echo.
			metadata[payment.EasyPayConfirmedMerchantOrderNo] = orderID
		}
	}
	return &payment.QueryOrderResponse{
		TradeNo:  responseTradeNo,
		Status:   status,
		Amount:   amount,
		Metadata: metadata,
	}, nil
}

// VerifyNotification validates callback fields and signature, then confirms
// successful payment through this same merchant's QueryOrder. Invalid structure
// or signature returns before any upstream request; non-success notifications
// remain ignored observations for the payment core.
//
// The MD5 wire format concatenates decoded values, so structured fields exclude
// separators and controls while name retains its product text. Successful
// callbacks require queried paid state, matching CNY money and matching trade
// identity. The resulting observation still needs local order, merchant and
// entitlement validation; an upstream error cannot authorize a local payment.
// Successful observations retain only the query's adapter-owned metadata, including
// any merchant-number compatibility proof derived from local instance configuration.
// @project-doc docs/domains/payments_and_entitlements.md#callback_security
func (e *EasyPay) VerifyNotification(ctx context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("parse notify: %w", err)
	}
	// Build one accepted value per key before signing; otherwise parsed field
	// selection and signature input could describe different callback data.
	params := make(map[string]string, len(values))
	for key, entries := range values {
		switch key {
		case "pid", "trade_no", "out_trade_no", "type", "name", "money", "trade_status", "param", "sign", "sign_type",
			"api_trade_no", "addtime", "endtime", "buyer", "timestamp":
		default:
			return nil, fmt.Errorf("unexpected notify param: %s", key)
		}
		// Reject duplicate values before extracting the first entry, keeping the
		// signed form identical to the callback fields later consumed.
		if len(entries) != 1 {
			return nil, fmt.Errorf("duplicate notify param: %s", key)
		}
		// Check optional fields too: their decoded text participates in the same
		// raw signature concatenation as required identifiers and payment state.
		if key != "name" && !easyPaySafeNotifyValue(entries[0]) {
			return nil, fmt.Errorf("invalid notify param: %s", key)
		}
		params[key] = entries[0]
	}
	// Require payment identity before signature verification, so an authenticated
	// observation cannot omit the order, transaction, merchant, state, or amount.
	for _, key := range []string{"pid", "trade_no", "out_trade_no", "type", "money", "trade_status"} {
		value := params[key]
		if value == "" {
			return nil, fmt.Errorf("missing notify param: %s", key)
		}
	}
	if !payment.ConfigEasyPayCustomMethodCodePattern.MatchString(params["type"]) {
		return nil, fmt.Errorf("invalid notify param: type")
	}
	if params["pid"] != strings.TrimSpace(e.config["pid"]) {
		return nil, fmt.Errorf("easypay notify pid mismatch")
	}
	paid, err := easyPayPositiveMoney(params["money"])
	if err != nil {
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
	metadata := e.MerchantIdentityMetadata()
	if params["trade_status"] == tradeStatusSuccess {
		// Reuse the verifier instance so registry refresh or another merchant cannot
		// replace the credentials authenticating this notification during paid confirmation.
		queried, queryErr := e.QueryOrder(ctx, params["out_trade_no"])
		if queryErr != nil {
			return nil, fmt.Errorf("confirm easypay notification: %w", queryErr)
		}
		if queried.Status != payment.ProviderStatusPaid ||
			!paid.Equal(decimal.NewFromFloat(queried.Amount)) || queried.TradeNo != params["trade_no"] {
			return nil, fmt.Errorf("easypay notification does not match a paid upstream order")
		}
		status = payment.ProviderStatusSuccess
		metadata = queried.Metadata
	}
	amount, _ := paid.Float64()
	return &payment.PaymentNotification{
		TradeNo: params["trade_no"], OrderID: params["out_trade_no"],
		Amount: amount, Status: status, RawData: rawBody, Metadata: metadata,
	}, nil
}

// easyPaySafeNotifyValue reports whether decoded structured text can occupy
// one signed form value. Empty optional values are permitted; separators,
// controls and surrounding whitespace are rejected before signature computation.
func easyPaySafeNotifyValue(value string) bool {
	return strings.TrimSpace(value) == value && !strings.ContainsAny(value, "&=") &&
		!strings.ContainsFunc(value, unicode.IsControl)
}

// easyPayPositiveMoney parses finite positive CNY money with at most two
// significant fractional digits. Decimal comparison keeps callback, query and
// refund amounts equal at the channel's smallest supported currency unit.
func easyPayPositiveMoney(value string) (decimal.Decimal, error) {
	// Bound decimal input before parsing so hostile exponents or large coefficients
	// cannot allocate arbitrary memory while validating a provider-controlled field.
	if len(value) > 32 || !easyPayMoneyPattern.MatchString(value) {
		return decimal.Zero, fmt.Errorf("invalid easypay amount")
	}
	amount, err := decimal.NewFromString(value)
	if err != nil || !amount.IsPositive() || !amount.Equal(amount.Truncate(2)) {
		return decimal.Zero, fmt.Errorf("invalid easypay amount")
	}
	approximate, _ := amount.Float64()
	if !payment.IsValidProviderAmount(approximate) {
		return decimal.Zero, fmt.Errorf("invalid easypay amount")
	}
	return amount, nil
}

// Refund submits available merchant-order and upstream-trade forms in that
// order, returning the identity of the successful attempt. A transport error
// stops submission immediately; a parsed order-not-found diagnostic permits
// the next form, while other parsed failures return at once.
//
// Each form carries the requested amount but omits the workflow OperationID.
// The alternative is another order lookup representation, so callers should
// not infer request-level deduplication from the returned RefundID. A
// successful parser produces success status. Unconfirmed responses and transport
// failures wrap ErrRefundOutcomeUnknown, retaining prepared local deductions;
// an exhausted attempt sequence returns its first parsed rejection.
func (e *EasyPay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	attempts := e.refundAttempts(req)
	if len(attempts) == 0 {
		return nil, fmt.Errorf("easypay refund missing order identifier")
	}
	var firstErr error
	for i, attempt := range attempts {
		body, status, err := e.postRaw(ctx, e.apiBase()+"/api.php?act=refund", attempt.params)
		if err != nil {
			return nil, fmt.Errorf("%w: easypay refund request: %v", payment.ErrRefundOutcomeUnknown, err)
		}
		if err := parseEasyPayRefundResponse(status, body, req.Amount); err != nil {
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
		return &payment.RefundResponse{RefundID: attempt.refundID, Status: payment.ProviderStatusSuccess}, nil
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

// isEasyPayRefundOrderNotFound recognizes an explicit parsed business
// rejection permitting the alternative order identifier. Transport errors and
// unrecognized response text cannot authorize a second refund submission.
func isEasyPayRefundOrderNotFound(err error) bool {
	return errors.Is(err, errEasyPayRefundOrderNotFound)
}

// parseEasyPayRefundResponse classifies the result of one submitted refund.
// Successful HTTP replies can confirm code one, code zero with a complete
// refund-success message, or that same plain-text message. A reported amount
// must equal the submitted CNY amount before success can finalize deductions.
//
// A recognized business rejection allows compensation. HTTP failures, system
// errors, malformed replies and unrecognized outcomes retain the prepared
// deduction through ErrRefundOutcomeUnknown. The missing-order sentinel is
// emitted only from parsed rejections, keeping identifier retries separate from
// ambiguous external effects that may already have transferred money.
func parseEasyPayRefundResponse(status int, body []byte, requestedAmount string) error {
	summary := summarizeEasyPayResponse(body)
	unknown := fmt.Errorf("%w: easypay refund unconfirmed (HTTP %d): %s", payment.ErrRefundOutcomeUnknown, status, summary)
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return unknown
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		return unknown
	}
	var resp struct {
		Code json.Number `json:"code"` // numeric or textual response code
		Msg  string      `json:"msg"`  // provider refund outcome message
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		if easyPayRefundMessageConfirms(message, requestedAmount) {
			return nil
		}
		return unknown
	}
	code, valid := easyPayIntegerCode(resp.Code)
	if !valid {
		return unknown
	}
	message = strings.TrimSpace(resp.Msg)
	confirmed := easyPayRefundMessageConfirms(message, requestedAmount)
	if confirmed {
		if code == 0 || code == easypayCodeSuccess {
			return nil
		}
		return unknown
	}
	// A refund success label with an invalid amount or an unfinished operation
	// describes conflicting evidence. Keep the prepared deduction until the merchant checks it.
	if strings.Contains(message, "退款成功") || easyPayRefundUnconfirmedPattern.MatchString(message) {
		return unknown
	}
	if code == easypayCodeSuccess {
		if easyPayRefundFailureHintPattern.MatchString(message) {
			return unknown
		}
		return nil
	}
	if !easyPayRefundRejectionMessage(message) {
		return unknown
	}
	if easyPayMissingOrderMessage(message) {
		return fmt.Errorf("%w: %s", errEasyPayRefundOrderNotFound, summary)
	}
	return fmt.Errorf("easypay refund rejected: %s", summary)
}

// easyPayRefundRejectionMessage recognizes complete business refusals that
// establish an unsuccessful submission. Optional refund-failed prefixes and
// surrounding punctuation are removed before matching known balance, permission,
// credential or parameter rejections. Generic failure words remain unconfirmed.
func easyPayRefundRejectionMessage(message string) bool {
	if easyPayMissingOrderMessage(message) {
		return true
	}
	reason := strings.ToLower(strings.TrimSpace(message))
	for _, prefix := range []string{"退款失败", "refund failed"} {
		reason = strings.TrimPrefix(reason, prefix)
	}
	reason = strings.Trim(reason, " \t\r\n:：,，!！。.")
	switch reason {
	case "余额不足", "商户余额不足", "账户余额不足", "账号余额不足",
		"退款金额超过订单金额", "退款金额超限", "参数错误", "参数不正确",
		"无退款权限", "权限不足", "退款功能未开启", "退款接口未开启",
		"订单退款api未开启", "退款api未开启", "商户密钥错误", "商户密钥不正确", "商户不存在",
		"insufficient balance", "permission denied", "refund disabled", "invalid merchant key",
		"invalid parameter", "invalid parameters", "acq.seller_balance_not_enough",
		"acq.invalid_parameter", "acq.refund_fee_error":
		return true
	default:
		return false
	}
}

// easyPayMissingOrderMessage recognizes complete missing-order business messages.
// It tolerates surrounding punctuation and whitespace used by V1 platforms;
// other diagnostic text cannot authorize another refund identifier or a probe success.
func easyPayMissingOrderMessage(message string) bool {
	switch strings.ToLower(strings.Trim(strings.TrimSpace(message), "!！。.")) {
	case "订单编号不存在", "订单不存在", "order not found", "order does not exist", "not found":
		return true
	default:
		return false
	}
}

// easyPayRefundMessageConfirms matches a complete supported success message.
// When a suffix reports money, both amounts must be positive CNY decimals and
// equal before the caller accepts external refund completion.
func easyPayRefundMessageConfirms(message, requestedAmount string) bool {
	matches := easyPayRefundSuccessPattern.FindStringSubmatch(message)
	if matches == nil {
		return false
	}
	if matches[1] == "" {
		return true
	}
	actual, err := easyPayPositiveMoney(matches[1])
	if err != nil {
		return false
	}
	expected, err := easyPayPositiveMoney(requestedAmount)
	return err == nil && actual.Equal(expected)
}

// easyPayResponseCodeIsSuccess accepts an exact numeric or textual one.
// Fractional codes and unsupported JSON types fail instead of being coerced to
// an integer that could confirm an unrelated business result.
func easyPayResponseCodeIsSuccess(code json.Number) bool {
	value, valid := easyPayIntegerCode(code)
	return valid && value == easypayCodeSuccess
}

// easyPayIntegerCode decodes a bounded integral JSON number or integer string.
// Missing, fractional and other JSON values return false so callers retain an
// unknown outcome rather than assigning a default successful code.
func easyPayIntegerCode(code json.Number) (int, bool) {
	parsed, err := strconv.ParseInt(string(code), 10, 32)
	return int(parsed), err == nil
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
// ten-second default, and closes the response body. Responses exceeding one
// MiB return an error, so a truncated successful prefix cannot confirm a
// payment or refund whose remaining content was discarded.
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
	// Close the response after its bounded read, including rejected oversized bodies.
	// Close errors cannot change the business result parsed from an already complete response.
	defer func() { _ = resp.Body.Close() }()
	// Read one extra byte to detect truncation before any parser can treat a
	// successful prefix as the complete outcome of an external financial operation.
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxEasypayResponseSize+1))
	if err != nil {
		return nil, resp.StatusCode, err
	}
	if len(body) > maxEasypayResponseSize {
		return nil, resp.StatusCode, fmt.Errorf("easypay response exceeds size limit")
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
