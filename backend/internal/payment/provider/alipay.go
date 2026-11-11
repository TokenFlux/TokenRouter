package provider

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/smartwalle/alipay/v3"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// alipay.go creates Alipay checkout links and QR payments and translates
// query, refund, and notification results.
// Official Alipay checkout chooses signed browser URLs or a precreate QR
// according to request device flags and instance mode. WAP/page operations
// build URLs through the pinned SDK, while precreate/query/refund/close
// perform its context-bound requests. Desktop precreate failure can therefore
// produce a later browser URL, and that URL stays in PayURL rather than the QR
// field.
//
// Adapter construction initially checks only app/private-key presence and
// retains the supplied config map. A mutex lazily constructs a production SDK
// client and loads public verification key before publishing it; callers keep
// config immutable because other adapter reads occur outside that mutex.
// Notification verification uses SDK signature decoding with no additional
// adapter replay-time check, then returns configured or reported app identity
// for saved snapshot comparison. Refund request identity is freshly
// timestamped per call and is distinct from its returned trade/fallback
// reference.

// Alipay product codes identify precreate, WAP and page checkout forms.
// Response markers classify fund changes/missing trades, and the refund suffix
// supplies a local response reference when trade identity is absent.
const (
	// precreate QR product code.
	alipayProductCodePreCreate = "FACE_TO_FACE_PAYMENT"
	// mobile WAP product code.
	alipayProductCodeWapPay = "QUICK_WAP_WAY"
	// browser page product code.
	alipayProductCodePagePay = "FAST_INSTANT_TRADE_PAY"

	// successful fund change marker.
	alipayFundChangeYes = "Y"
	// missing trade diagnostic marker.
	alipayErrTradeNotExist = "ACQ.TRADE_NOT_EXIST"
)

// Alipay operation variables delegate to pinned SDK calls and let package
// tests substitute WAP, precreate or page behavior while restoring those
// shared functions afterward.
var (
	alipayTradeWapPay = func(client *alipay.Client, param alipay.TradeWapPay) (*url.URL, error) {
		return client.TradeWapPay(param)
	}
	alipayTradePreCreate = func(ctx context.Context, client *alipay.Client, param alipay.TradePreCreate) (*alipay.TradePreCreateRsp, error) {
		return client.TradePreCreate(ctx, param)
	}
	alipayTradePagePay = func(client *alipay.Client, param alipay.TradePagePay) (*url.URL, error) {
		return client.TradePagePay(param)
	}

	// These assertions check Alipay's provider, cancellation and
	// merchant-metadata method sets at compile time. Runtime construction still
	// performs its separate configuration presence and lazy-client checks.
	_ payment.Provider                 = (*Alipay)(nil)
	_ payment.CancelableProvider       = (*Alipay)(nil)
	_ payment.MerchantIdentityProvider = (*Alipay)(nil)
)

// Alipay supplies official trade creation/query, signed notification decoding,
// refunds and cancellation through the SDK.
type Alipay struct {
	instanceID string // configured local instance identity
	// config retains the caller's map without copying it. Callers keep
	// app/key/URL/mode values immutable throughout adapter use; method reads
	// outside getClient are not guarded by mu.
	config map[string]string

	// mu coordinates only lazy SDK client initialization and publication. It does
	// not synchronize external writes to the retained config map or every SDK
	// operation.
	mu sync.Mutex
	// client is published after production SDK creation and public-key loading
	// succeed. Failure leaves it nil for another getClient attempt, while later
	// calls reuse the same pointer.
	client *alipay.Client
}

// NewAlipay checks nonempty appId/privateKey and retains instance identity
// plus the supplied config map. It does not eagerly parse keys or require the
// verification key; getClient performs those checks later, and callers keep
// the shared map immutable.
func NewAlipay(instanceID string, config map[string]string) (*Alipay, error) {
	required := []string{"appId", "privateKey"}
	for _, k := range required {
		if config[k] == "" {
			return nil, fmt.Errorf("alipay config missing required key: %s", k)
		}
	}
	return &Alipay{
		instanceID: instanceID,
		config:     config,
	}, nil
}

// getClient initializes and returns one production SDK client under mu,
// reusing the published pointer on later calls. The adapter's config map
// remains caller-owned immutable input throughout that initialization.
//
// Private/client creation runs before loading publicKey, with alipayPublicKey
// used only when publicKey is empty. Missing or unusable verification key
// returns an error before assignment, leaving initialization retryable.
// Publication occurs only after both stages succeed. The lock protects this
// lazy pointer; operation methods use the shared SDK client outside that
// initialization lock and retain their separate immutable config reads.
func (a *Alipay) getClient() (*alipay.Client, error) {
	// Hold initialization through SDK creation and public-key loading, then
	// publish one client pointer. Failure leaves the pointer unset so another
	// call can retry using the retained immutable config.
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil {
		return a.client, nil
	}
	client, err := alipay.New(a.config["appId"], a.config["privateKey"], true)
	if err != nil {
		return nil, fmt.Errorf("alipay init client: %w", err)
	}
	pubKey := a.config["publicKey"]
	// The alternate alipayPublicKey spelling is compatibility input only when
	// publicKey is empty. A supplied publicKey wins even if loading it later
	// returns an error.
	if pubKey == "" {
		pubKey = a.config["alipayPublicKey"]
	}
	if pubKey == "" {
		return nil, fmt.Errorf("alipay config missing required key: publicKey (or alipayPublicKey)")
	}
	if err := client.LoadAliPayPublicKey(pubKey); err != nil {
		return nil, fmt.Errorf("alipay load public key: %w", err)
	}
	a.client = client
	return a.client, nil
}

// Name returns the display label for the official Alipay adapter.
func (a *Alipay) Name() string { return "Alipay" }

// ProviderKey returns the official Alipay key used by merchant binding
// callers.
func (a *Alipay) ProviderKey() string { return payment.TypeAlipay }

// SupportedTypes returns a new slice containing the visible Alipay method for
// registry binding. Callers can change that returned slice without changing
// later results or the retained provider configuration.
func (a *Alipay) SupportedTypes() []payment.PaymentType {
	return []payment.PaymentType{payment.TypeAlipay}
}

// MerchantIdentityMetadata returns a new app_id map from trimmed configured
// appId for saved-merchant comparison. Nil receiver or blank app identity
// returns nil, leaving the consuming snapshot validator's metadata rules
// applicable.
func (a *Alipay) MerchantIdentityMetadata() map[string]string {
	if a == nil {
		return nil
	}
	appID := strings.TrimSpace(a.config["appId"])
	if appID == "" {
		return nil
	}
	return map[string]string{"app_id": appID}
}

// CreatePayment chooses mobile WAP, requested mobile precreate or desktop mode
// after lazy client setup. Nonempty request notify/return URLs override
// configured values; the shared map remains immutable caller input.
//
// Mobile precreate uses ctx for the API call and returns QR payload. WAP and
// page operations build signed URLs without receiving ctx in the pinned SDK
// wrappers. Desktop redirect mode directly uses page, while other modes
// attempt precreate then page on failure. Response field choice preserves
// browser versus QR presentation, and this adapter performs no local order
// persistence around those results.
func (a *Alipay) CreatePayment(ctx context.Context, req payment.CreatePaymentRequest) (*payment.CreatePaymentResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	notifyURL := a.config["notifyUrl"]
	if req.NotifyURL != "" {
		notifyURL = req.NotifyURL
	}
	returnURL := a.config["returnUrl"]
	if req.ReturnURL != "" {
		returnURL = req.ReturnURL
	}

	if req.IsMobile {
		if req.AlipayMobilePrecreate {
			return a.createPrecreateTrade(ctx, client, req, notifyURL)
		}
		return a.createWapTrade(client, req, notifyURL, returnURL)
	}
	return a.createDesktopTrade(ctx, client, req, notifyURL, returnURL)
}

// createWapTrade builds the mobile signed checkout URL from the supplied
// usable SDK client, merchant number, amount, subject and callbacks. The
// pinned WAP operation constructs a URL locally and has no ctx argument;
// success reports the merchant reference in TradeNo and the URL in PayURL.
func (a *Alipay) createWapTrade(client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradeWapPay{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodeWapPay
	param.NotifyURL = notifyURL
	param.ReturnURL = returnURL

	payURL, err := alipayTradeWapPay(client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradeWapPay: %w", err)
	}
	return &payment.CreatePaymentResponse{
		TradeNo: req.OrderID,
		PayURL:  payURL.String(),
	}, nil
}

// createDesktopTrade returns page checkout directly for trimmed
// case-insensitive redirect mode, otherwise attempts precreate before page.
// Supply the usable SDK client and callback URLs chosen by CreatePayment.
//
// Any precreate error, including nil/failure/empty QR handling, permits the
// signed browser page attempt. A usable QR returns immediately; a successful
// page result replaces the failed precreate result. When both fail, the error
// includes precreate text and wraps the page error. Only precreate receives
// ctx here; URL building remains the pinned SDK's local operation.
func (a *Alipay) createDesktopTrade(ctx context.Context, client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	// Redirect mode chooses a signed browser URL before QR creation. Other
	// desktop modes keep the precreate attempt so response fields can select
	// their intended frontend launch path.
	if strings.EqualFold(strings.TrimSpace(a.config["paymentMode"]), "redirect") {
		return a.createPagePayTrade(client, req, notifyURL, returnURL)
	}

	resp, precreateErr := a.createPrecreateTrade(ctx, client, req, notifyURL)
	if precreateErr == nil {
		return resp, nil
	}

	// A failed precreate can still obtain browser checkout from local page URL
	// construction. Try that separate presentation path and retain both
	// diagnostics only when neither result is usable.
	resp, pagePayErr := a.createPagePayTrade(client, req, notifyURL, returnURL)
	if pagePayErr == nil {
		return resp, nil
	}

	return nil, fmt.Errorf("alipay desktop payment failed: precreate=%v; pagepay=%w", precreateErr, pagePayErr)
}

// createPrecreateTrade submits a context-bound QR precreate for the supplied
// merchant/amount/subject and notify URL. Nil response, API failure or
// trimmed-empty QR returns an error; success retains returned QR text and uses
// the merchant number as the initial trade reference.
func (a *Alipay) createPrecreateTrade(ctx context.Context, client *alipay.Client, req payment.CreatePaymentRequest, notifyURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradePreCreate{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodePreCreate
	param.NotifyURL = notifyURL

	rsp, err := alipayTradePreCreate(ctx, client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradePreCreate: %w", err)
	}
	if rsp == nil {
		return nil, fmt.Errorf("alipay TradePreCreate: empty response")
	}
	if rsp.IsFailure() {
		return nil, fmt.Errorf("alipay TradePreCreate failed: %s", rsp.Error.Error())
	}
	if strings.TrimSpace(rsp.QRCode) == "" {
		return nil, fmt.Errorf("alipay TradePreCreate: empty qr_code")
	}

	return &payment.CreatePaymentResponse{
		TradeNo: req.OrderID,
		QRCode:  rsp.QRCode,
	}, nil
}

// createPagePayTrade builds a signed browser checkout URL from the supplied
// usable client and request fields. It has no ctx-bound API call in the pinned
// page wrapper; success populates PayURL while QRCode stays empty for frontend
// launch selection.
func (a *Alipay) createPagePayTrade(client *alipay.Client, req payment.CreatePaymentRequest, notifyURL, returnURL string) (*payment.CreatePaymentResponse, error) {
	param := alipay.TradePagePay{}
	param.OutTradeNo = req.OrderID
	param.TotalAmount = req.Amount
	param.Subject = req.Subject
	param.ProductCode = alipayProductCodePagePay
	param.NotifyURL = notifyURL
	param.ReturnURL = returnURL

	payURL, err := alipayTradePagePay(client, param)
	if err != nil {
		return nil, fmt.Errorf("alipay TradePagePay: %w", err)
	}
	// Put the signed page address in PayURL so frontend launch can use browser
	// checkout. QRCode is reserved for the precreate payload and remains empty on
	// this presentation path.
	return &payment.CreatePaymentResponse{
		TradeNo: req.OrderID,
		PayURL:  payURL.String(),
	}, nil
}

// QueryOrder queries the supplied merchant OutTradeNo through the lazy SDK
// client using ctx. A diagnostic containing the missing-trade marker returns
// pending with that reference; other query errors return with context.
//
// Success/finished statuses map to paid, closed maps to failed and other
// statuses stay pending. TotalAmount is parsed first, then the listed
// alternative amount fields are tried after parse failure. Parsing alone can
// accept nonfinite/sign values, which fulfillment validates separately before
// confirmation. The returned PaidAt is provider text and metadata comes from
// current configured app identity.
func (a *Alipay) QueryOrder(ctx context.Context, tradeNo string) (*payment.QueryOrderResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	result, err := client.TradeQuery(ctx, alipay.TradeQuery{OutTradeNo: tradeNo})
	if err != nil {
		// A missing-trade diagnostic is a pending observation for merchant lookup
		// rather than a confirmed failure. Lifecycle can then apply its
		// cancel/requery policy without losing this requested reference.
		if isTradeNotExist(err) {
			return &payment.QueryOrderResponse{
				TradeNo: tradeNo,
				Status:  payment.ProviderStatusPending,
			}, nil
		}
		return nil, fmt.Errorf("alipay TradeQuery: %w", err)
	}

	status := payment.ProviderStatusPending
	switch result.TradeStatus {
	case alipay.TradeStatusSuccess, alipay.TradeStatusFinished:
		status = payment.ProviderStatusPaid
	case alipay.TradeStatusClosed:
		status = payment.ProviderStatusFailed
	}

	amount, err := strconv.ParseFloat(result.TotalAmount, 64)
	// The primary amount text failed parsing. Try trimmed total, receipt, buyer
	// and invoice fields in that order, preserving the first parseable gateway
	// quantity for later fulfillment validation.
	if err != nil {
		amount, err = parseAlipayAmount(
			result.TotalAmount,
			result.ReceiptAmount,
			result.BuyerPayAmount,
			result.InvoiceAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("alipay parse amount: %w", err)
		}
	}

	return &payment.QueryOrderResponse{
		TradeNo:  result.TradeNo,
		Status:   status,
		Amount:   amount,
		PaidAt:   result.SendPayDate,
		Metadata: a.MerchantIdentityMetadata(),
	}, nil
}

// VerifyNotification parses form text and delegates signature
// validation/notification decoding to the pinned SDK client. Headers are
// unused, and the adapter adds no separate notification-time replay window or
// NotifyVerify request.
//
// Success/finished maps to success while other trade states return failed,
// which core callback handling ignores for non-Stripe providers. Amount
// parsing can use receipt/buyer fallback fields and leaves finite-positive
// confirmation to fulfillment. Configured app metadata is produced first; a
// nonblank reported AppId replaces its app_id value for saved snapshot
// comparison. Raw body and merchant/trade identifiers remain available in the
// returned observation.
func (a *Alipay) VerifyNotification(ctx context.Context, rawBody string, _ map[string]string) (*payment.PaymentNotification, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	values, err := url.ParseQuery(rawBody)
	if err != nil {
		return nil, fmt.Errorf("alipay parse notification: %w", err)
	}

	notification, err := client.DecodeNotification(ctx, values)
	if err != nil {
		return nil, fmt.Errorf("alipay verify notification: %w", err)
	}

	status := payment.ProviderStatusFailed
	if notification.TradeStatus == alipay.TradeStatusSuccess || notification.TradeStatus == alipay.TradeStatusFinished {
		status = payment.ProviderStatusSuccess
	}

	amount, err := strconv.ParseFloat(notification.TotalAmount, 64)
	// The signed notification's primary total failed parsing. Try its trimmed
	// total, receipt and buyer fields in order while preserving merchant/trade
	// data for later amount and snapshot validation.
	if err != nil {
		amount, err = parseAlipayAmount(
			notification.TotalAmount,
			notification.ReceiptAmount,
			notification.BuyerPayAmount,
		)
		if err != nil {
			return nil, fmt.Errorf("alipay parse notification amount: %w", err)
		}
	}

	metadata := a.MerchantIdentityMetadata()
	if appID := strings.TrimSpace(notification.AppId); appID != "" {
		if metadata == nil {
			metadata = map[string]string{}
		}
		metadata["app_id"] = appID
	}

	return &payment.PaymentNotification{
		TradeNo:  notification.TradeNo,
		OrderID:  notification.OutTradeNo,
		Amount:   amount,
		Status:   status,
		RawData:  rawBody,
		Metadata: metadata,
	}, nil
}

// Refund submits a context-bound merchant refund with requested amount/reason
// and an OutRequestNo built from current Unix nanoseconds on each call. The
// workflow's preparation OperationID is not sent here, so this construction is
// not its stable retry key.
//
// FundChange Y maps to success and other successful replies remain pending.
// The adapter returns the merchant refund request number even after an
// ambiguous submission. Alipay queries require manual verification here, so
// the stored request number identifies the operation at the provider.
func (a *Alipay) Refund(ctx context.Context, req payment.RefundRequest) (*payment.RefundResponse, error) {
	client, err := a.getClient()
	if err != nil {
		return nil, err
	}

	// Retain this merchant refund request number even if the response is lost
	// so operators can reconcile the operation at Alipay without resubmitting.
	refundID := fmt.Sprintf("%s-refund-%d", req.OrderID, time.Now().UnixNano())
	result, err := client.TradeRefund(ctx, alipay.TradeRefund{
		OutTradeNo:   req.OrderID,
		RefundAmount: req.Amount,
		RefundReason: req.Reason,
		OutRequestNo: refundID,
	})
	if err != nil {
		var apiErr *alipay.Error
		if errors.As(err, &apiErr) && alipayRefundDefinitelyRejected(apiErr.Code) {
			return nil, fmt.Errorf("alipay refund rejected: %w", err)
		}
		return &payment.RefundResponse{RefundID: refundID},
			fmt.Errorf("%w: alipay refund submission: %v", payment.ErrRefundOutcomeUnknown, err)
	}
	if result == nil || !result.IsSuccess() {
		if result != nil && alipayRefundDefinitelyRejected(result.Code) {
			return nil, fmt.Errorf("alipay refund rejected: %s", result.Code)
		}
		return &payment.RefundResponse{RefundID: refundID},
			fmt.Errorf("%w: alipay refund response was not confirmed", payment.ErrRefundOutcomeUnknown)
	}

	refundStatus := payment.ProviderStatusPending
	if result.FundChange == alipayFundChangeYes {
		refundStatus = payment.ProviderStatusSuccess
	}

	return &payment.RefundResponse{
		RefundID: refundID,
		Status:   refundStatus,
	}, nil
}

// alipayRefundDefinitelyRejected recognizes validation and authorization failures
// that precede refund execution. Other responses require provider verification.
func alipayRefundDefinitelyRejected(code alipay.Code) bool {
	switch code {
	case alipay.CodeInvalidAuthToken, alipay.CodeMissingParam, alipay.CodeInvalidParam, alipay.CodePermissionDenied:
		return true
	default:
		return false
	}
}

// CancelPayment closes the supplied merchant OutTradeNo using the
// context-bound SDK operation. A missing-trade diagnostic is accepted as no
// remaining trade, while other errors return with context for lifecycle's
// requery and pending-preservation decision.
func (a *Alipay) CancelPayment(ctx context.Context, tradeNo string) error {
	client, err := a.getClient()
	if err != nil {
		return err
	}

	_, err = client.TradeClose(ctx, alipay.TradeClose{OutTradeNo: tradeNo})
	if err != nil {
		// A missing upstream trade leaves nothing for this close request to cancel.
		// Return nil for that classified diagnostic so lifecycle can proceed to its
		// conditional local close.
		if isTradeNotExist(err) {
			return nil
		}
		return fmt.Errorf("alipay TradeClose: %w", err)
	}
	return nil
}

// isTradeNotExist reports the missing-trade marker appearing anywhere in a
// nonnil error string. It is substring classification rather than a structured
// response-code check, and query/close callers choose their distinct
// pending/nil recovery results.
func isTradeNotExist(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), alipayErrTradeNotExist)
}

// parseAlipayAmount returns the first nonblank trimmed value accepted by
// ParseFloat in supplied order. Unreadable fields are skipped; exhaustion
// returns an error. It adds no finite-positive check, so callers validate the
// resulting gateway quantity before payment confirmation.
func parseAlipayAmount(values ...string) (float64, error) {
	// Query and notification callers supply ordered alternatives after the
	// primary total failed. Skip blank/unparseable fields individually so a later
	// usable quantity can still reach fulfillment amount validation.
	for _, raw := range values {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		amount, err := strconv.ParseFloat(raw, 64)
		if err == nil {
			return amount, nil
		}
	}
	return 0, fmt.Errorf("no valid amount field")
}
