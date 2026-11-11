package provider

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	"github.com/wechatpay-apiv3/wechatpay-go/core"
	"github.com/wechatpay-apiv3/wechatpay-go/core/auth/verifiers"
	"github.com/wechatpay-apiv3/wechatpay-go/core/notify"
	"github.com/wechatpay-apiv3/wechatpay-go/core/option"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/h5"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/jsapi"
	"github.com/wechatpay-apiv3/wechatpay-go/services/payments/native"
	"github.com/wechatpay-apiv3/wechatpay-go/services/refunddomestic"
	"github.com/wechatpay-apiv3/wechatpay-go/utils"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// wxpay.go initiates official WeChat payments and translates signed
// notifications, queries, cancellations, and refunds.
// Official WeChat payment chooses JSAPI from a nonblank payer OpenID, H5 from
// the mobile flag and required client address, or native QR otherwise. All
// charge/refund amounts use CNY fen, while merchant order numbers remain
// query/cancel references and amount-derived merchant refund labels address
// recovery queries. Missing native/H5 response URLs can remain empty
// initiation fields.
//
// Construction eagerly checks required credential presence, API-v3 key bytes
// and RSA key parsing, then retains the caller config map. A mutex lazily
// publishes one SDK client and notification handler using Background
// initialization; individual API calls use their supplied context. Callback
// processing reconstructs an HTTP request for the pinned verifier's
// timestamp/signature check and AES-GCM decryption before decoding transaction
// metadata. Refund creation and querying have different terminal-status
// mappings, and the adapter supplies transaction metadata without implementing
// the optional MerchantIdentityMetadata method used by refund preparation.

// WeChat constants identify CNY/H5 and result navigation, returned transaction
// metadata, initiation modes, recognized trade/event states and the required
// API-v3 key byte length.
const (
	// gateway amount currency label.
	wxpayCurrency = "CNY"
	// H5 browser scene type.
	wxpayH5Type = "Wap"
	// default payment result path.
	wxpayResultPath = "/payment/result"

	// transaction application identity key.
	wxpayMetadataAppID = "appid"
	// transaction merchant identity key.
	wxpayMetadataMerchantID = "mchid"
	// transaction amount currency key.
	wxpayMetadataCurrency = "currency"
	// transaction trade state key.
	wxpayMetadataTradeState = "trade_state"

	// native QR initiation mode.
	wxpayModeNative = "native"
	// mobile H5 initiation mode.
	wxpayModeH5 = "h5"
	// OpenID JSAPI initiation mode.
	wxpayModeJSAPI = "jsapi"

	// confirmed payment success state.
	wxpayTradeStateSuccess = "SUCCESS"
	// reported refunded trade state.
	wxpayTradeStateRefund = "REFUND"
	// reported closed trade state.
	wxpayTradeStateClosed = "CLOSED"
	// reported payment error state.
	wxpayTradeStatePayError = "PAYERROR"

	// handled transaction success event.
	wxpayEventTransactionSuccess = "TRANSACTION.SUCCESS"

	// required API key bytes.
	wxpayAPIv3KeyLength = 32
)

// WeChat prepay variables delegate to SDK modes and allow serialized package
// tests to substitute responses before restoring these shared operation
// functions.
var (
	wxpayNativePrepay = func(ctx context.Context, svc native.NativeApiService, req native.PrepayRequest) (*native.PrepayResponse, *core.APIResult, error) {
		return svc.Prepay(ctx, req)
	}
	wxpayH5Prepay = func(ctx context.Context, svc h5.H5ApiService, req h5.PrepayRequest) (*h5.PrepayResponse, *core.APIResult, error) {
		return svc.Prepay(ctx, req)
	}
	wxpayJSAPIPrepayWithRequestPayment = func(ctx context.Context, svc jsapi.JsapiApiService, req jsapi.PrepayRequest) (*jsapi.PrepayWithRequestPaymentResponse, *core.APIResult, error) {
		return svc.PrepayWithRequestPayment(ctx, req)
	}

	// These assertions check the provider and cancellation method sets at compile
	// time. QueryRefund remains an optional method recognized by workflow
	// interface assertion at runtime.
	_ payment.Provider           = (*Wxpay)(nil)
	_ payment.CancelableProvider = (*Wxpay)(nil)
)

// Wxpay supplies official prepay, transaction verification, merchant refunds
// and cancellation through the API-v3 SDK.
type Wxpay struct {
	instanceID string // configured local instance identity
	// config retains the caller's map without cloning it. Credential, URL and
	// scene values stay immutable during adapter use because reads outside
	// ensureClient are not synchronized by mu.
	config map[string]string
	// mu coordinates lazy creation/publication of coreClient and notifyHandler.
	// It does not guard external config writes or every subsequent SDK
	// API/notification operation.
	mu sync.Mutex
	// coreClient is assigned only after both client and notification handler
	// construction succeed. Nil remains retryable after initialization errors,
	// and later operations reuse its shared pointer.
	coreClient *core.Client
	// notifyHandler is published with coreClient using configured RSA verifier
	// and API-v3 decryption key. VerifyNotification uses this shared handler
	// after ensureClient returns successfully.
	notifyHandler *notify.Handler
}

// NewWxpay checks nonempty required credentials, exact API-v3 key byte length
// and eager RSA private/public key parsing. It retains the supplied config map
// and instance ID; callers keep the map immutable, while lazy client/handler
// creation still occurs on first operation.
func NewWxpay(instanceID string, config map[string]string) (*Wxpay, error) {
	// Public-key verification needs its key identity alongside merchant signing
	// and decryption credentials. Check their presence before eager parsing so
	// configuration callers receive the missing field reason first.
	required := []string{"appId", "mchId", "privateKey", "apiV3Key", "certSerial", "publicKey", "publicKeyId"}
	for _, k := range required {
		if config[k] == "" {
			return nil, apperror.BadRequest("WXPAY_CONFIG_MISSING_KEY", "missing_required_key").
				WithMetadata(map[string]string{"key": k})
		}
	}
	if len(config["apiV3Key"]) != wxpayAPIv3KeyLength {
		return nil, apperror.BadRequest("WXPAY_CONFIG_INVALID_KEY_LENGTH", "invalid_key_length").
			WithMetadata(map[string]string{
				"key":      "apiV3Key",
				"expected": strconv.Itoa(wxpayAPIv3KeyLength),
				"actual":   strconv.Itoa(len(config["apiV3Key"])),
			})
	}
	// Parse both RSA key inputs before returning an adapter candidate.
	// ConfigService constructor validation can then reject malformed key text
	// during its enabled save path, while lazy setup still uses immutable values
	// later.
	if _, err := utils.LoadPrivateKey(formatPEM(config["privateKey"], "PRIVATE KEY")); err != nil {
		return nil, apperror.BadRequest("WXPAY_CONFIG_INVALID_KEY", "invalid_key").
			WithMetadata(map[string]string{"key": "privateKey"})
	}
	if _, err := utils.LoadPublicKey(formatPEM(config["publicKey"], "PUBLIC KEY")); err != nil {
		return nil, apperror.BadRequest("WXPAY_CONFIG_INVALID_KEY", "invalid_key").
			WithMetadata(map[string]string{"key": "publicKey"})
	}
	return &Wxpay{instanceID: instanceID, config: config}, nil
}

// Name returns the display label for the official WeChat payment adapter.
func (w *Wxpay) Name() string { return "Wxpay" }

// ProviderKey returns the official WeChat key used by payment merchant
// bindings.
func (w *Wxpay) ProviderKey() string { return payment.TypeWxpay }

// SupportedTypes returns a fresh slice containing the visible WeChat method
// for registry registration. Mutating that returned slice leaves later results
// and the retained instance configuration unchanged.
func (w *Wxpay) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeWxpay}
}

// ResolveWxpayJSAPIAppID delegates the trimmed mpAppId-before-appId rule
// shared with checkout app validation. Nil or absent configuration returns
// empty through that helper, leaving caller/provider configuration checks to
// decide availability.
func ResolveWxpayJSAPIAppID(config map[string]string) string {
	return payment.ResolveWxpayJSAPIAppID(config)
}

// formatPEM trims key text and retains an existing BEGIN-prefixed PEM
// independently of keyType. Other text is wrapped with the requested
// header/footer; parsing/algorithm validation remains with the SDK key loader.
func formatPEM(key, keyType string) string {
	// Retain supplied PEM headers when present so already formatted keys keep
	// their encoded representation. Bare key text receives the requested label
	// before the SDK loader validates its RSA content.
	key = strings.TrimSpace(key)
	if strings.HasPrefix(key, "-----BEGIN") {
		return key
	}
	return fmt.Sprintf("-----BEGIN %s-----\n%s\n-----END %s-----", keyType, key, keyType)
}

// ensureClient returns a shared SDK client after lazily creating it and its
// notification handler under mu. It reparses configured RSA keys and uses the
// configured public-key ID for verification, with the retained config
// immutable.
//
// SDK initialization receives Background rather than the caller API context.
// The notification handler is constructed before assigning either cached
// field, so an earlier error leaves initialization retryable. Successful API
// calls later pass their own context separately, and verification uses the
// published handler. This lock coordinates pointer publication rather than all
// request operations or caller mutation of config.
func (w *Wxpay) ensureClient() (*core.Client, error) {
	// Construct client and decryption handler before publishing their shared
	// pointers under one mutex. Earlier failure leaves both unset for retry,
	// while API request context is supplied separately after initialization.
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.coreClient != nil {
		return w.coreClient, nil
	}
	privateKey, err := utils.LoadPrivateKey(formatPEM(w.config["privateKey"], "PRIVATE KEY"))
	if err != nil {
		return nil, apperror.BadRequest("WXPAY_CONFIG_INVALID_KEY", "invalid_key").
			WithMetadata(map[string]string{"key": "privateKey"})
	}
	publicKey, err := utils.LoadPublicKey(formatPEM(w.config["publicKey"], "PUBLIC KEY"))
	if err != nil {
		return nil, apperror.BadRequest("WXPAY_CONFIG_INVALID_KEY", "invalid_key").
			WithMetadata(map[string]string{"key": "publicKey"})
	}
	verifier := verifiers.NewSHA256WithRSAPubkeyVerifier(w.config["publicKeyId"], *publicKey)
	client, err := core.NewClient(context.Background(),
		option.WithMerchantCredential(w.config["mchId"], w.config["certSerial"], privateKey),
		option.WithVerifier(verifier))
	if err != nil {
		return nil, fmt.Errorf("wxpay init client: %w", err)
	}
	handler, err := notify.NewRSANotifyHandler(w.config["apiV3Key"], verifier)
	if err != nil {
		return nil, fmt.Errorf("wxpay init notify handler: %w", err)
	}
	w.notifyHandler = handler
	w.coreClient = client
	return w.coreClient, nil
}

// CreatePayment validates a nonempty request/config notify URL, converts
// amount text to CNY fen and chooses its initiation mode after client setup.
// Nonempty request NotifyURL wins, and amount sign/business validation belongs
// to checkout and the provider API.
//
// Nonblank trimmed OpenID selects JSAPI even for a nonmobile request.
// Otherwise mobile requires client IP and uses H5, while desktop uses native.
// Context is passed to the selected SDK operation; local order persistence
// remains with checkout. Request ExpiresAt is not sent by these prepay
// builders, and native/H5 helpers can return empty URL fields when the
// successful SDK response omitted them.
func (w *Wxpay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	client, err := w.ensureClient()
	if err != nil {
		return nil, err
	}
	// Use the caller's selected notification destination before configured
	// fallback, retaining an explicit request value. Unlike an optional return
	// URL, this callback destination must be nonempty before prepay can proceed.
	notifyURL := req.NotifyURL
	if notifyURL == "" {
		notifyURL = w.config["notifyUrl"]
	}
	if notifyURL == "" {
		return nil, fmt.Errorf("wxpay notifyUrl is required")
	}
	// Convert validated CNY text to integer fen for the API.
	totalFen, err := payment.YuanToFen(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("wxpay create payment: %w", err)
	}

	mode, err := resolveWxpayCreateMode(req)
	if err != nil {
		return nil, err
	}
	switch mode {
	case wxpayModeJSAPI:
		return w.prepayJSAPI(ctx, client, req, notifyURL, totalFen)
	case wxpayModeH5:
		return w.prepayH5(ctx, client, req, notifyURL, totalFen)
	case wxpayModeNative:
		return w.prepayNative(ctx, client, req, notifyURL, totalFen)
	default:
		return nil, fmt.Errorf("wxpay create payment: unsupported mode %q", mode)
	}
}

// prepayJSAPI submits CNY fen and trimmed payer OpenID with the shared JSAPI
// app precedence and optional nonblank client IP. Supply a usable SDK client
// and prevalidated request/notify values; the operation returns a ready
// payload from a nonnil successful SDK response, using empty strings for nil
// payload fields.
func (w *Wxpay) prepayJSAPI(ctx context.Context, c *core.Client, req payment.CreatePaymentRequest, notifyURL string, totalFen int64) (*payment.CreatePaymentResponse, error) {
	svc := jsapi.JsapiApiService{Client: c}
	cur := wxpayCurrency
	appID := ResolveWxpayJSAPIAppID(w.config)
	prepayReq := jsapi.PrepayRequest{
		Appid:       core.String(appID),
		Mchid:       core.String(w.config["mchId"]),
		Description: core.String(req.Subject),
		OutTradeNo:  core.String(req.OrderID),
		NotifyUrl:   core.String(notifyURL),
		Amount:      &jsapi.Amount{Total: core.Int64(totalFen), Currency: &cur},
		Payer:       &jsapi.Payer{Openid: core.String(strings.TrimSpace(req.OpenID))},
	}
	if clientIP := strings.TrimSpace(req.ClientIP); clientIP != "" {
		prepayReq.SceneInfo = &jsapi.SceneInfo{PayerClientIp: core.String(clientIP)}
	}
	resp, _, err := wxpayJSAPIPrepayWithRequestPayment(ctx, svc, prepayReq)
	if err != nil {
		return nil, fmt.Errorf("wxpay jsapi prepay: %w", err)
	}
	return &payment.CreatePaymentResponse{
		TradeNo:    req.OrderID,
		ResultType: payment.CreatePaymentResultJSAPIReady,
		JSAPI: &payment.WechatJSAPIPayload{
			AppID:     wxSV(resp.Appid),
			TimeStamp: wxSV(resp.TimeStamp),
			NonceStr:  wxSV(resp.NonceStr),
			Package:   wxSV(resp.Package),
			SignType:  wxSV(resp.SignType),
			PaySign:   wxSV(resp.PaySign),
		},
	}, nil
}

// prepayNative submits the merchant order and CNY fen with configured merchant
// app identity, returning the response CodeUrl as QRCode. Supply a usable
// client/nonblank notify URL; a nonnil successful response with nil CodeUrl
// produces empty QR text rather than an adapter error.
func (w *Wxpay) prepayNative(ctx context.Context, c *core.Client, req payment.CreatePaymentRequest, notifyURL string, totalFen int64) (*payment.CreatePaymentResponse, error) {
	svc := native.NativeApiService{Client: c}
	cur := wxpayCurrency
	resp, _, err := wxpayNativePrepay(ctx, svc, native.PrepayRequest{
		Appid: core.String(w.config["appId"]), Mchid: core.String(w.config["mchId"]),
		Description: core.String(req.Subject), OutTradeNo: core.String(req.OrderID),
		NotifyUrl: core.String(notifyURL),
		Amount:    &native.Amount{Total: core.Int64(totalFen), Currency: &cur},
	})
	if err != nil {
		return nil, fmt.Errorf("wxpay native prepay: %w", err)
	}
	codeURL := ""
	if resp.CodeUrl != nil {
		codeURL = *resp.CodeUrl
	}
	return &payment.CreatePaymentResponse{TradeNo: req.OrderID, QRCode: codeURL}, nil
}

// prepayH5 submits the merchant order/CNY fen with mobile client IP and
// configured H5 scene, then adds payment result redirection to the returned
// URL. Supply a usable client and validated request; nil H5Url on a nonnil
// successful response stays empty and bypasses redirect construction.
//
// A nonblank H5/return pair is rebuilt with merchant/method result parameters
// and query-escaped as redirect_url. URL validation errors occur after prepay
// may already have succeeded externally, and checkout retains that initiation
// error for local recovery. Configured scene fields are independent of the
// canonical return destination selected by checkout.
func (w *Wxpay) prepayH5(ctx context.Context, c *core.Client, req payment.CreatePaymentRequest, notifyURL string, totalFen int64) (*payment.CreatePaymentResponse, error) {
	svc := h5.H5ApiService{Client: c}
	cur := wxpayCurrency
	resp, _, err := wxpayH5Prepay(ctx, svc, h5.PrepayRequest{
		Appid: core.String(w.config["appId"]), Mchid: core.String(w.config["mchId"]),
		Description: core.String(req.Subject), OutTradeNo: core.String(req.OrderID),
		NotifyUrl: core.String(notifyURL),
		Amount:    &h5.Amount{Total: core.Int64(totalFen), Currency: &cur},
		SceneInfo: &h5.SceneInfo{PayerClientIp: core.String(req.ClientIP), H5Info: buildWxpayH5Info(w.config)},
	})
	if err != nil {
		return nil, fmt.Errorf("wxpay h5 prepay: %w", err)
	}
	h5URL := ""
	if resp.H5Url != nil {
		h5URL = *resp.H5Url
	}
	h5URL, err = appendWxpayRedirectURL(h5URL, req)
	if err != nil {
		return nil, err
	}
	return &payment.CreatePaymentResponse{TradeNo: req.OrderID, PayURL: h5URL}, nil
}

// buildWxpayH5Info returns a fresh Wap scene with optional trimmed app
// name/URL from config. Missing or nil config leaves those optional fields
// absent; it does not validate the scene URL or resolve a payment return
// destination.
func buildWxpayH5Info(config map[string]string) *h5.H5Info {
	tp := wxpayH5Type
	info := &h5.H5Info{Type: &tp}
	if appName := strings.TrimSpace(config["h5AppName"]); appName != "" {
		info.AppName = core.String(appName)
	}
	if appURL := strings.TrimSpace(config["h5AppUrl"]); appURL != "" {
		info.AppUrl = core.String(appURL)
	}
	return info
}

// resolveWxpayCreateMode chooses JSAPI for nonblank OpenID, then H5 for mobile
// with nonblank client IP, otherwise native. Missing IP rejects only the
// selected H5 path, and the method does not inspect browser identity or
// request payment mode.
func resolveWxpayCreateMode(req payment.CreatePaymentRequest) (string, error) {
	if strings.TrimSpace(req.OpenID) != "" {
		return wxpayModeJSAPI, nil
	}
	if req.IsMobile {
		if strings.TrimSpace(req.ClientIP) == "" {
			return "", fmt.Errorf("wxpay H5 payment requires client IP")
		}
		return wxpayModeH5, nil
	}
	return wxpayModeNative, nil
}

// appendWxpayRedirectURL appends query-escaped result navigation only when
// both trimmed H5 and return URLs are nonblank. Empty input returns the
// trimmed H5 text directly, including an empty URL.
//
// The result destination is built first with server merchant/method values and
// can fail validation before concatenation. Separator choice tests whether H5
// text contains a question mark rather than parsing that URL, so existing
// query/fragment spelling follows that literal concatenation. Checkout
// validates its canonical destination separately; this helper preserves the H5
// URL as the external launch address.
func appendWxpayRedirectURL(h5URL string, req payment.CreatePaymentRequest) (string, error) {
	h5URL = strings.TrimSpace(h5URL)
	returnURL := strings.TrimSpace(req.ReturnURL)
	if h5URL == "" || returnURL == "" {
		return h5URL, nil
	}

	redirectURL, err := buildWxpayResultURL(returnURL, req)
	if err != nil {
		return "", err
	}

	sep := "&"
	if !strings.Contains(h5URL, "?") {
		sep = "?"
	}
	return h5URL + sep + "redirect_url=" + url.QueryEscape(redirectURL), nil
}

// buildWxpayResultURL validates an absolute HTTP/HTTPS return URL and adds
// trimmed merchant/method query entries for H5 navigation. The caller supplies
// an already permitted destination; this helper does not enforce checkout's
// fixed path, userinfo or same-host checks.
//
// An empty path becomes the internal result path, while a nonempty path is
// retained. Existing other query values remain, RawPath is cleared for
// decoded-path serialization and fragment is removed. This keeps server
// merchant identity distinct from local numeric order/session values that can
// already be present after checkout return construction.
func buildWxpayResultURL(returnURL string, req payment.CreatePaymentRequest) (string, error) {
	u, err := url.Parse(returnURL)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("return URL must be an absolute http(s) URL")
	}

	values := u.Query()
	values.Set("out_trade_no", strings.TrimSpace(req.OrderID))
	if paymentType := strings.TrimSpace(req.PaymentType); paymentType != "" {
		values.Set("payment_type", paymentType)
	}
	if strings.TrimSpace(u.Path) == "" {
		u.Path = wxpayResultPath
	}
	// Serialize the retained decoded path after adding merchant/method query
	// values, clearing alternate RawPath and fragment. The preceding checkout
	// canonicalization owns destination trust checks before this navigation
	// transformation.
	u.RawPath = ""
	u.RawQuery = values.Encode()
	u.Fragment = ""
	return u.String(), nil
}

// wxSV returns pointed-to SDK text or empty for nil input. Transaction/payload
// builders use that distinction to omit absent metadata or keep an empty
// response value, without changing the SDK pointer.
func wxSV(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// mapWxState maps SUCCESS to paid, REFUND to refunded and CLOSED/PAYERROR to
// failed. Every other trade label remains pending, including states not named
// here; lifecycle interprets that observation before its own cancel/recovery
// decision.
func mapWxState(s string) string {
	switch s {
	case wxpayTradeStateSuccess:
		return payment.ProviderStatusPaid
	case wxpayTradeStateRefund:
		return payment.ProviderStatusRefunded
	case wxpayTradeStateClosed, wxpayTradeStatePayError:
		return payment.ProviderStatusFailed
	default:
		return payment.ProviderStatusPending
	}
}

// buildWxpayTransactionMetadata copies nonempty transaction app, merchant,
// trade state and available currency into a new map. Nil transaction or no
// retained fields returns nil, and text is not trimmed; saved snapshot
// validation owns the applicable comparison rules.
func buildWxpayTransactionMetadata(tx *payments.Transaction) map[string]string {
	if tx == nil {
		return nil
	}

	metadata := map[string]string{}
	if appID := wxSV(tx.Appid); appID != "" {
		metadata[wxpayMetadataAppID] = appID
	}
	if merchantID := wxSV(tx.Mchid); merchantID != "" {
		metadata[wxpayMetadataMerchantID] = merchantID
	}
	if tradeState := wxSV(tx.TradeState); tradeState != "" {
		metadata[wxpayMetadataTradeState] = tradeState
	}
	if tx.Amount != nil {
		if currency := wxSV(tx.Amount.Currency); currency != "" {
			metadata[wxpayMetadataCurrency] = currency
		}
	}
	if len(metadata) == 0 {
		return nil
	}
	return metadata
}

// QueryOrder reads the supplied merchant OutTradeNo for configured mchId using
// the shared client and caller context. It returns provider state mapping and
// transaction metadata without local order persistence.
//
// Available total fen becomes approximate CNY yuan, while missing amount
// leaves zero for later core validation. A nonnil transaction ID pointer
// replaces the requested reference even when its text is empty; absent pointer
// keeps the merchant number. SuccessTime remains provider text. Query errors
// return with context, while accepted paid observations still pass
// fulfillment's amount and merchant checks.
func (w *Wxpay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	c, err := w.ensureClient()
	if err != nil {
		return nil, err
	}
	svc := native.NativeApiService{Client: c}
	tx, _, err := svc.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(tradeNo), Mchid: core.String(w.config["mchId"]),
	})
	if err != nil {
		return nil, fmt.Errorf("wxpay query order: %w", err)
	}
	var amt float64
	// Convert available total fen back to approximate CNY yuan.
	if tx.Amount != nil && tx.Amount.Total != nil {
		amt = payment.FenToYuan(*tx.Amount.Total)
	}
	id := tradeNo
	if tx.TransactionId != nil {
		id = *tx.TransactionId
	}
	pa := ""
	if tx.SuccessTime != nil {
		pa = *tx.SuccessTime
	}
	return &payment.QueryOrderResponse{
		TradeNo:  id,
		Status:   mapWxState(wxSV(tx.TradeState)),
		Amount:   amt,
		PaidAt:   pa,
		Metadata: buildWxpayTransactionMetadata(tx),
	}, nil
}

// VerifyNotification reconstructs a memory-backed HTTP request and delegates
// the received headers/body to the published RSA notification handler. The
// pinned SDK rejects absolute timestamp differences of at least five minutes
// and checks signature before AES-GCM decryption and transaction decoding;
// this adapter sends no outbound verification request.
//
// Only TRANSACTION.SUCCESS yields a notification; other verified events return
// nil,nil. Transaction SUCCESS maps to success and other decoded trade states
// to failed, which non-Stripe core callback handling ignores. Available fen
// becomes yuan and optional transaction identity/currency metadata is copied
// for snapshot comparison. Malformed cryptographic inputs retain the pinned
// SDK/GCM preconditions, so not every invalid decoded nonce becomes an
// ordinary returned error.
func (w *Wxpay) VerifyNotification(ctx context.Context, rawBody string, headers map[string]string) (*payment.PaymentNotification, error) {
	if _, err := w.ensureClient(); err != nil {
		return nil, err
	}
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/", io.NopCloser(bytes.NewBufferString(rawBody)))
	if err != nil {
		return nil, fmt.Errorf("wxpay construct request: %w", err)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	var tx payments.Transaction
	nr, err := w.notifyHandler.ParseNotifyRequest(ctx, r, &tx)
	if err != nil {
		return nil, fmt.Errorf("wxpay verify notification: %w", err)
	}
	// An authenticated/decrypted event outside transaction success is irrelevant
	// to this adapter's payment transition. Return nil so the HTTP verifier
	// acknowledges it without entering order fulfillment.
	if nr.EventType != wxpayEventTransactionSuccess {
		return nil, nil
	}
	var amt float64
	// Convert available total fen back to approximate CNY yuan.
	if tx.Amount != nil && tx.Amount.Total != nil {
		amt = payment.FenToYuan(*tx.Amount.Total)
	}
	st := payment.ProviderStatusFailed
	if wxSV(tx.TradeState) == wxpayTradeStateSuccess {
		st = payment.ProviderStatusSuccess
	}
	return &payment.PaymentNotification{
		TradeNo: wxSV(tx.TransactionId), OrderID: wxSV(tx.OutTradeNo),
		Amount: amt, Status: st, RawData: rawBody, Metadata: buildWxpayTransactionMetadata(&tx),
	}, nil
}

// Refund converts requested CNY amount to fen, queries the original merchant
// order total and submits a merchant-derived refund number. The amount/order
// inputs have been validated by the workflow; missing original total fields
// become zero before the provider request.
//
// wxpayRefundID uses order plus amount text rather than the workflow
// preparation ID, so spelling controls that fallback identity. Immediate
// Channel status uses the same mapping as query. The returned RefundID is the
// merchant refund number even when the POST outcome is unknown, so a later
// recovery can read the exact operation without creating another refund.
func (w *Wxpay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	c, err := w.ensureClient()
	if err != nil {
		return nil, err
	}
	// Convert requested refund CNY text into integer API fen.
	rf, err := payment.YuanToFen(req.Amount)
	if err != nil {
		return nil, fmt.Errorf("wxpay refund amount: %w", err)
	}
	tf, err := w.queryOrderTotalFen(ctx, c, req.OrderID)
	if err != nil {
		return nil, err
	}
	rs := refunddomestic.RefundsApiService{Client: c}
	cur := wxpayCurrency
	outRefundNo := wxpayRefundID(req.OrderID, req.Amount)
	res, _, err := rs.Create(ctx, refunddomestic.CreateRequest{
		OutTradeNo:  core.String(req.OrderID),
		OutRefundNo: core.String(outRefundNo),
		Reason:      core.String(req.Reason),
		Amount:      &refunddomestic.AmountReq{Refund: core.Int64(rf), Total: core.Int64(tf), Currency: &cur},
	})
	if err != nil {
		// The merchant refund number already identifies this request after a
		// transport failure. Preserve it so recovery can query before retrying.
		var apiErr *core.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode < 500 {
			switch apiErr.Code {
			case "PARAM_ERROR", "SIGN_ERROR", "NO_AUTH":
				return nil, fmt.Errorf("wxpay refund rejected: %w", err)
			}
		}
		return &payment.RefundResponse{RefundID: outRefundNo},
			fmt.Errorf("%w: wxpay refund submission: %v", payment.ErrRefundOutcomeUnknown, err)
	}
	return &payment.RefundResponse{RefundID: outRefundNo, Status: wxpayRefundStatus(res)}, nil
}

// QueryRefund queries the supplied trimmed merchant refund number or derives
// it from order/amount text when absent. Empty derived identity returns an
// error, and no new refund submission occurs in this method.
//
// Query SUCCESS maps to success, CLOSED/ABNORMAL to failed, and other or
// missing response/status values remain pending. This differs from creation's
// non-success mapping. The fallback depends on the exact amount spelling, so
// recovery callers preserve the saved refund number whenever available; local
// state/version validation remains with workflow finalization.
func (w *Wxpay) QueryRefund(ctx context.Context, req payment.RefundQueryRequest) (*payment.RefundResponse, error) {
	c, err := w.ensureClient()
	if err != nil {
		return nil, err
	}
	outRefundNo := strings.TrimSpace(req.RefundID)
	// Missing saved refund identity uses the same order/amount-text construction
	// as creation. Reusing the saved number first avoids associating differently
	// spelled or differently based recovery money with another query label.
	if outRefundNo == "" {
		outRefundNo = wxpayRefundID(req.OrderID, req.Amount)
	}
	if outRefundNo == "" {
		return nil, fmt.Errorf("wxpay query refund: missing refund id")
	}
	rs := refunddomestic.RefundsApiService{Client: c}
	res, _, err := rs.QueryByOutRefundNo(ctx, refunddomestic.QueryByOutRefundNoRequest{
		OutRefundNo: core.String(outRefundNo),
	})
	if err != nil {
		return nil, fmt.Errorf("wxpay query refund: %w", err)
	}
	expectedAmount, err := payment.YuanToFen(req.Amount)
	if err != nil || expectedAmount <= 0 || !strings.EqualFold(req.Currency, wxpayCurrency) {
		return nil, fmt.Errorf("wxpay query refund: invalid expected amount or currency")
	}
	if res == nil || res.OutRefundNo == nil || *res.OutRefundNo != outRefundNo ||
		res.OutTradeNo == nil || *res.OutTradeNo != req.OrderID ||
		res.Amount == nil || res.Amount.Refund == nil || *res.Amount.Refund != expectedAmount ||
		res.Amount.Currency == nil || !strings.EqualFold(*res.Amount.Currency, req.Currency) {
		return nil, fmt.Errorf("wxpay query refund: refund does not match the prepared payment")
	}
	return &payment.RefundResponse{RefundID: outRefundNo, Status: wxpayRefundStatus(res)}, nil
}

// wxpayRefundStatus treats abnormal refunds as pending because operators can
// still resolve them in WeChat Pay. Missing or unrecognized states are unknown.
func wxpayRefundStatus(res *refunddomestic.Refund) string {
	if res == nil || res.Status == nil {
		return ""
	}
	switch *res.Status {
	case refunddomestic.STATUS_SUCCESS:
		return payment.ProviderStatusSuccess
	case refunddomestic.STATUS_CLOSED:
		return payment.ProviderStatusFailed
	case refunddomestic.STATUS_PROCESSING, refunddomestic.STATUS_ABNORMAL:
		return payment.ProviderStatusPending
	default:
		return ""
	}
}

// wxpayRefundID derives merchant refund text from a trimmed nonblank order
// number and amount spelling. It removes periods/hyphens from amount rather
// than parsing a numeric quantity, using a bare refund suffix for empty
// transformed amount.
//
// Equal numeric values can therefore produce different labels when spelled
// differently, and different signed/fraction punctuation can collapse to one
// label. This construction adds no unique operation version and is not the
// workflow preparation ID. Refund returns it for later QueryRefund to reuse;
// only absent saved identity invokes the same textual fallback.
func wxpayRefundID(orderID, amount string) string {
	orderID = strings.TrimSpace(orderID)
	if orderID == "" {
		return ""
	}
	// Build the label from amount spelling by stripping decimal/sign punctuation,
	// not numeric minor-unit conversion. Preserve this distinction when recovery
	// has both a saved refund number and separately formatted amount.
	amount = strings.NewReplacer(".", "", "-", "").Replace(strings.TrimSpace(amount))
	if amount == "" {
		return orderID + "-refund"
	}
	return orderID + "-refund-" + amount
}

// queryOrderTotalFen reads the original merchant order total using the
// supplied usable SDK client and configured merchant ID. Query errors return
// with context; absent total/amount fields yield zero, and the helper does not
// validate positivity or the transaction currency.
func (w *Wxpay) queryOrderTotalFen(ctx context.Context, c *core.Client, orderID string) (int64, error) {
	svc := native.NativeApiService{Client: c}
	tx, _, err := svc.QueryOrderByOutTradeNo(ctx, native.QueryOrderByOutTradeNoRequest{
		OutTradeNo: core.String(orderID), Mchid: core.String(w.config["mchId"]),
	})
	if err != nil {
		return 0, fmt.Errorf("wxpay refund query order: %w", err)
	}
	var tf int64
	// Keep the original order total in integer API fen for the refund request's
	// Total field. This helper does not perform the yuan display conversion used
	// by payment observations.
	if tx.Amount != nil && tx.Amount.Total != nil {
		tf = *tx.Amount.Total
	}
	return tf, nil
}

// CancelPayment closes the supplied merchant OutTradeNo for configured mchId
// using the caller context. SDK errors return with context for lifecycle's
// requery/pending-preservation handling; this adapter does not suppress
// missing-trade errors into nil.
func (w *Wxpay) CancelPayment(ctx context.Context, tradeNo string) error {
	c, err := w.ensureClient()
	if err != nil {
		return err
	}
	svc := native.NativeApiService{Client: c}
	_, err = svc.CloseOrder(ctx, native.CloseOrderRequest{
		OutTradeNo: core.String(tradeNo), Mchid: core.String(w.config["mchId"]),
	})
	if err != nil {
		return fmt.Errorf("wxpay cancel payment: %w", err)
	}
	return nil
}
