package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

// webhook.go reads provider callbacks, tries candidate verifiers, and
// dispatches accepted notifications to payment fulfillment.
// A callback's merchant number can narrow candidate accounts before its
// signature is verified, but it remains an unsigned hint. The handler passes
// the received text and first header values to each candidate until one
// returns no error, including a nil notification for an irrelevant verified
// event.
//
// POST reading is capped at one MiB by truncation rather than an explicit
// oversize rejection; GET query text bypasses that body reader. Verification
// failures return 400, while provider/order processing failures generally
// return 500. Missing provider lookup is acknowledged for non-WeChat, and a
// missing local order is acknowledged after verification. Provider-specific
// success bodies retain their protocol shape, and failed-verification body
// logging truncates by bytes, which can split UTF-8.

// WebhookMaxWebhookBodySize and WebhookWebhookLogTruncateLen bound received
// POST bytes and failure diagnostics. WebhookWxpaySuccessCode and
// WebhookWxpaySuccessMessage form the official WeChat acknowledgement payload.
const (
	// WebhookMaxWebhookBodySize caps bytes read from POST callbacks through
	// LimitReader; an overlong request is truncated at one MiB before provider
	// verification.
	WebhookMaxWebhookBodySize = 1 << 20

	// WebhookWebhookLogTruncateLen caps raw failure-body diagnostics by byte
	// prefix before the truncation suffix is appended.
	WebhookWebhookLogTruncateLen = 200

	// WebhookWxpaySuccessCode and WebhookWxpaySuccessMessage are the literal
	// code/message written by the official WeChat success response. Each belongs
	// to that acknowledgement rather than order status.
	WebhookWxpaySuccessCode    = "SUCCESS" // protocol success code
	WebhookWxpaySuccessMessage = "成功"      // protocol success message
)

// PaymentWebhookHandler adapts public provider callbacks into verified payment
// notification handling.
type PaymentWebhookHandler struct {
	paymentService *payment.Runtime // candidate and fulfillment runtime
}

// WebhookWxpaySuccessResponse is the official WeChat callback success
// acknowledgement JSON shape.
type WebhookWxpaySuccessResponse struct {
	Code    string `json:"code"`    // protocol acknowledgement code
	Message string `json:"message"` // protocol acknowledgement message
}

// NewPaymentWebhookHandler binds the payment runtime for callback verification
// and fulfillment. Candidate and notification operations require a usable
// runtime; callback handlers resolve provider accounts through its
// ProviderBindings before processing accepted notifications.
func NewPaymentWebhookHandler(paymentService *payment.Runtime) *PaymentWebhookHandler {
	return &PaymentWebhookHandler{
		paymentService: paymentService,
	}
}

// EasyPayNotify passes EasyPay GET query or POST form text to common callback
// handling. The selected provider verifies its signed notification before
// order processing, and success uses the plain-text acknowledgement.
func (h *PaymentWebhookHandler) EasyPayNotify(c *gin.Context) {
	h.handleNotify(c, payment.TypeEasyPay)
}

// AlipayNotify delegates the official Alipay callback to common body reading
// and candidate verification. Verified notification handling follows before
// its plain-text success acknowledgement; errors retain the shared status
// policy.
func (h *PaymentWebhookHandler) AlipayNotify(c *gin.Context) {
	h.handleNotify(c, payment.TypeAlipay)
}

// WxpayNotify delegates official WeChat signed/encrypted callbacks to common
// candidate verification. Account candidates perform verification/decryption
// before notification handling, and accepted callbacks receive the protocol
// code/message acknowledgement.
func (h *PaymentWebhookHandler) WxpayNotify(c *gin.Context) {
	h.handleNotify(c, payment.TypeWxpay)
}

// StripeWebhook delegates Stripe events to common candidate verification and
// notification handling. A verified irrelevant event can yield nil
// notification and still receive an empty successful response without order
// processing.
func (h *PaymentWebhookHandler) StripeWebhook(c *gin.Context) {
	h.handleNotify(c, payment.TypeStripe)
}

// AirwallexWebhook delegates Airwallex signed events to common callback
// handling. Candidate verification precedes order processing, and accepted or
// irrelevant events receive an empty successful response under the shared
// policy.
func (h *PaymentWebhookHandler) AirwallexWebhook(c *gin.Context) {
	h.handleNotify(c, payment.TypeAirwallex)
}

// handleNotify reads callback text, resolves candidate providers, and
// processes the first accepted notification before its provider-specific
// acknowledgement. POST uses a one-MiB LimitReader and GET uses raw query
// text; a read error returns bad request.
//
// Merchant extraction is an unsigned account hint. Candidate verification
// receives lowercased header names with each first value, and the first nil
// error wins even for an irrelevant nil notification. A missing provider is
// acknowledged except official WeChat; other lookup failures return 500.
// Failed verification returns 400 after bounded diagnostics. Verified missing
// orders are acknowledged before optional upstream confirmation, while real
// lookup, confirmation, or other processing failures remain retryable.
func (h *PaymentWebhookHandler) handleNotify(c *gin.Context, providerKey string) {
	var rawBody string
	if c.Request.Method == http.MethodGet {
		// EasyPay GET compatibility carries its signed form fields in the raw query.
		// Keep that received encoding for verification; this path does not use the
		// POST byte cap.
		rawBody = c.Request.URL.RawQuery
	} else {
		// Read at most the configured POST bytes before verification. LimitReader
		// truncates an overlong body rather than reporting overflow, so the provider
		// validates the resulting received prefix.
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, WebhookMaxWebhookBodySize))
		if err != nil {
			slog.Error("[Payment Webhook] failed to read body", "provider", providerKey, "error", err)
			c.String(http.StatusBadRequest, "failed to read body")
			return
		}
		rawBody = string(body)
	}

	// Use the unsigned merchant hint to narrow candidate account configuration
	// before verification. The provider's accepted notification still determines
	// order identity and saved-merchant validation during handling.
	outTradeNo := WebhookExtractOutTradeNo(rawBody, providerKey)

	providers, err := h.paymentService.GetWebhookProviders(c.Request.Context(), providerKey, outTradeNo)
	if err != nil {
		slog.Warn("[Payment Webhook] provider not found", "provider", providerKey, "outTradeNo", outTradeNo, "error", err)
		// Only classified missing-provider lookup reaches acknowledgement handling.
		// Other lookup failures stay retryable server errors; official WeChat
		// missing-account handling still returns verification failure.
		if !WebhookShouldAcknowledgeWebhookProviderLookupError(err) {
			c.String(http.StatusInternalServerError, "provider lookup failed")
			return
		}
		if providerKey == payment.TypeWxpay {
			c.String(http.StatusBadRequest, "verify failed")
			return
		}
		WebhookWriteSuccessResponse(c, providerKey)
		return
	}

	// Normalize header names for adapter lookups while retaining each first HTTP
	// value. Provider signature checks receive this shared map together with the
	// exact received body text.
	headers := make(map[string]string)
	for k := range c.Request.Header {
		headers[strings.ToLower(k)] = c.GetHeader(k)
	}

	verifiedProvider, notification, err := webhookVerifyNotificationWithProviders(c.Request.Context(), providers, rawBody, headers)
	if err != nil {
		truncatedBody := rawBody
		// Limit failure diagnostics to a byte prefix plus suffix. This bounds logged
		// payload length but can split a UTF-8 character and does not redact signed
		// field values.
		if len(truncatedBody) > WebhookWebhookLogTruncateLen {
			truncatedBody = truncatedBody[:WebhookWebhookLogTruncateLen] + "...(truncated)"
		}
		slog.Error("[Payment Webhook] verify failed", "provider", providerKey, "error", err, "method", c.Request.Method, "bodyLen", len(rawBody))
		slog.Debug("[Payment Webhook] verify failed body", "provider", providerKey, "rawBody", truncatedBody)
		c.String(http.StatusBadRequest, "verify failed")
		return
	}
	resolvedProviderKey := verifiedProvider.ProviderKey()

	// The accepted candidate can classify the event as irrelevant. Acknowledge
	// that nil notification immediately so it does not enter order handling or
	// cause later account trials.
	if notification == nil {
		WebhookWriteSuccessResponse(c, resolvedProviderKey)
		return
	}

	if confirmer, ok := verifiedProvider.(payment.NotificationConfirmer); ok &&
		notification.Status == payment.NotificationStatusSuccess {
		// Preserve the existing missing-order acknowledgement before a gateway
		// query. Obsolete verified callbacks have no local state to fulfill, while
		// database failures remain retryable and existing orders still require the
		// same verified merchant instance to confirm upstream payment.
		if _, err := h.paymentService.FindPaymentNotificationOrder(c.Request.Context(), notification.OrderID); err != nil {
			if errors.Is(err, payment.ErrOrderNotFound) {
				slog.Warn("[Payment Webhook] unknown order before confirmation, acking to stop retries",
					"provider", resolvedProviderKey,
					"outTradeNo", notification.OrderID,
					"tradeNo", notification.TradeNo,
				)
				WebhookWriteSuccessResponse(c, resolvedProviderKey)
				return
			}
			slog.Error("[Payment Webhook] pre-confirmation order lookup failed",
				"provider", resolvedProviderKey,
				"outTradeNo", notification.OrderID,
				"tradeNo", notification.TradeNo,
				"error", err,
			)
			c.String(http.StatusInternalServerError, "order lookup failed")
			return
		}
		if err := confirmer.ConfirmNotification(c.Request.Context(), notification); err != nil {
			slog.Error("[Payment Webhook] upstream confirmation failed",
				"provider", resolvedProviderKey,
				"outTradeNo", notification.OrderID,
				"tradeNo", notification.TradeNo,
				"error", err,
			)
			c.String(http.StatusBadGateway, "confirm failed")
			return
		}
	}

	if err := h.paymentService.HandlePaymentNotification(c.Request.Context(), notification, resolvedProviderKey); err != nil {
		// A verified notification can name no local order. Acknowledge that
		// classified absence so retries do not repeatedly request fulfillment for an
		// order this runtime cannot read.
		if errors.Is(err, payment.ErrOrderNotFound) {
			slog.Warn("[Payment Webhook] unknown order, acking to stop retries",
				"provider", resolvedProviderKey,
				"outTradeNo", notification.OrderID,
				"tradeNo", notification.TradeNo,
			)
			WebhookWriteSuccessResponse(c, resolvedProviderKey)
			return
		}
		slog.Error("[Payment Webhook] handle notification failed", "provider", resolvedProviderKey, "error", err)
		c.String(http.StatusInternalServerError, "handle failed")
		return
	}

	WebhookWriteSuccessResponse(c, resolvedProviderKey)
}

// WebhookShouldAcknowledgeWebhookProviderLookupError reports whether the
// lookup error wraps ErrProviderNotFound. Shared callback handling uses that
// classification before its provider-specific missing-account acknowledgement
// policy; other errors remain server failures.
func WebhookShouldAcknowledgeWebhookProviderLookupError(err error) bool {
	return errors.Is(err, payment.ErrProviderNotFound)
}

// WebhookExtractOutTradeNo extracts an unsigned merchant hint from supported
// form or JSON representations. Parse failure and providers such as encrypted
// official WeChat return empty text, so candidate verification must establish
// the notification's identity.
//
// EasyPay/Alipay read out_trade_no, Stripe reads object metadata orderId, and
// Airwallex reads its nested merchant_order_id. These representations select
// candidate accounts before authentication; no returned value authorizes
// fulfillment on its own. Trimming is applied to the JSON hints, while form
// extraction returns the parsed first value.
func WebhookExtractOutTradeNo(rawBody, providerKey string) string {
	switch providerKey {
	case payment.TypeEasyPay, payment.TypeAlipay:
		values, err := url.ParseQuery(rawBody)
		if err == nil {
			return values.Get("out_trade_no")
		}
	case payment.TypeStripe:
		return WebhookExtractStripeOutTradeNo(rawBody)
	case payment.TypeAirwallex:
		var payload struct {
			Data struct {
				Object struct {
					// MerchantOrderID is an unsigned nested order hint for selecting
					// candidate accounts. Signature verification remains required before this
					// event can affect a persisted order.
					MerchantOrderID string `json:"merchant_order_id"`
				} `json:"object"` // unsigned Airwallex object
			} `json:"data"` // unsigned Airwallex envelope
		}
		if err := json.Unmarshal([]byte(rawBody), &payload); err == nil {
			return strings.TrimSpace(payload.Data.Object.MerchantOrderID)
		}
	}
	// Absent or unreadable supported hints leave candidate verification
	// responsible for account selection. Encrypted official WeChat callbacks
	// reveal their order number only through an accepted provider decryptor.
	return ""
}

// WebhookExtractStripeOutTradeNo reads trimmed orderId metadata from an
// unsigned Stripe event object. Malformed JSON or absent metadata returns
// empty, leaving provider candidate discovery to continue before signature
// verification.
func WebhookExtractStripeOutTradeNo(rawBody string) string {
	var payload struct {
		Data struct {
			Object struct {
				// Metadata holds unsigned Stripe object fields used only as candidate
				// hints here. The provider verifier authenticates the event before its
				// notification metadata reaches payment handling.
				Metadata map[string]string `json:"metadata"`
			} `json:"object"` // unsigned Stripe object
		} `json:"data"` // unsigned Stripe envelope
	}
	// Unreadable unsigned event JSON supplies no merchant hint. Return empty so
	// candidate discovery continues; signature verification remains the authority
	// for accepting a later parsed notification.
	if err := json.Unmarshal([]byte(rawBody), &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Data.Object.Metadata["orderId"])
}

// WebhookVerifyNotificationWithProviders tries nonnil candidates in supplied
// order and returns the first nil-error result with that provider key. It does
// not require a nonnil notification: an accepted irrelevant event stops later
// candidate trials.
//
// Each candidate receives the same raw text and headers, and failed attempts
// retain their last error for an all-failed result. An empty/all-nil candidate
// list returns a separate verification error. HTTP handling then uses the
// resolved key for acknowledgement and notification validation, so ordering
// can determine which merchant accepts an event.
func WebhookVerifyNotificationWithProviders(ctx context.Context, providers []payment.Provider, rawBody string, headers map[string]string) (string, *payment.PaymentNotification, error) {
	provider, notification, err := webhookVerifyNotificationWithProviders(ctx, providers, rawBody, headers)
	if err != nil {
		return "", nil, err
	}
	return provider.ProviderKey(), notification, nil
}

// webhookVerifyNotificationWithProviders is the handler-only variant that
// retains the exact provider instance accepting the callback. Call it after
// candidate lookup and before any local order mutation. A nil notification is
// a verified irrelevant event; otherwise the caller may use optional provider
// capabilities on this same merchant instance before fulfillment. Candidates
// are tried in order, and the last verification error wins when none accepts.
func webhookVerifyNotificationWithProviders(ctx context.Context, providers []payment.Provider, rawBody string, headers map[string]string) (payment.Provider, *payment.PaymentNotification, error) {
	var lastErr error
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		notification, err := provider.VerifyNotification(ctx, rawBody, headers)
		// A failed account verification can leave another candidate usable. Continue
		// in supplied order and retain the last error only if no later candidate
		// accepts the event.
		if err != nil {
			lastErr = err
			continue
		}
		return provider, notification, nil
	}
	if lastErr != nil {
		return nil, nil, lastErr
	}
	return nil, nil, fmt.Errorf("no webhook provider could verify notification")
}

// WebhookWriteSuccessResponse writes the accepted callback acknowledgement for
// the resolved provider key. Official WeChat receives code/message JSON;
// Stripe/Airwallex receive empty 200 text, and other providers receive
// plain-text success according to their callback protocol.
func WebhookWriteSuccessResponse(c *gin.Context, providerKey string) {
	// Write the resolved adapter's expected acknowledgement shape after
	// acceptance so its retry policy sees protocol success. WeChat uses
	// code/message JSON while hosted/form adapters use their response text.
	switch providerKey {
	case payment.TypeWxpay:
		c.JSON(http.StatusOK, WebhookWxpaySuccessResponse{Code: WebhookWxpaySuccessCode, Message: WebhookWxpaySuccessMessage})
	case payment.TypeStripe, payment.TypeAirwallex:
		c.String(http.StatusOK, "")
	default:
		c.String(http.StatusOK, "success")
	}
}
