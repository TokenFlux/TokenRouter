//go:build unit

package provider

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	stripe "github.com/stripe/stripe-go/v85"
)

func TestBuildStripeCheckoutSessionCreateParamsUsesDashboardPaymentMethods(t *testing.T) {
	t.Parallel()

	expiresAt := time.Now().Add(2 * time.Hour).UTC().Truncate(time.Second)
	params := buildStripeCheckoutSessionCreateParams("cus_123", payment.CreatePaymentRequest{
		OrderID:   "sub2_order_123",
		Subject:   "TokenRouter Balance",
		ReturnURL: "https://app.example.com/payment/result?order_id=101",
		ExpiresAt: expiresAt,
		BillingInfo: &payment.BillingInfo{
			Email: "payer@example.com",
		},
	}, 1292, "42", "CNY")

	if params.Customer == nil || *params.Customer != "cus_123" {
		t.Fatalf("customer = %#v, want cus_123", params.Customer)
	}
	if params.Mode == nil || *params.Mode != string(stripe.CheckoutSessionModePayment) {
		t.Fatalf("mode = %#v, want payment", params.Mode)
	}
	if params.ClientReferenceID == nil || *params.ClientReferenceID != "sub2_order_123" {
		t.Fatalf("client_reference_id = %#v", params.ClientReferenceID)
	}
	if params.SuccessURL == nil || *params.SuccessURL != "https://app.example.com/payment/result?order_id=101&status=success" {
		t.Fatalf("success_url = %#v", params.SuccessURL)
	}
	if params.CancelURL == nil || *params.CancelURL != "https://app.example.com/payment/result?order_id=101&status=cancelled" {
		t.Fatalf("cancel_url = %#v", params.CancelURL)
	}
	if params.PaymentMethodTypes != nil {
		t.Fatalf("payment method types = %#v, want nil so Stripe Dashboard can decide", params.PaymentMethodTypes)
	}
	if params.InvoiceCreation == nil || params.InvoiceCreation.Enabled == nil || !*params.InvoiceCreation.Enabled {
		t.Fatalf("invoice_creation should be enabled")
	}
	if params.PaymentIntentData == nil || params.PaymentIntentData.ReceiptEmail == nil || *params.PaymentIntentData.ReceiptEmail != "payer@example.com" {
		t.Fatalf("receipt email = %#v", params.PaymentIntentData)
	}
	if params.Metadata["orderId"] != "sub2_order_123" || params.PaymentIntentData.Metadata["providerInstanceId"] != "42" {
		t.Fatalf("metadata not propagated: checkout=%#v payment_intent=%#v", params.Metadata, params.PaymentIntentData.Metadata)
	}
	if len(params.LineItems) != 1 || params.LineItems[0].PriceData == nil {
		t.Fatalf("line items = %#v, want one inline price", params.LineItems)
	}
	lineItem := params.LineItems[0]
	if lineItem.Quantity == nil || *lineItem.Quantity != 1 {
		t.Fatalf("quantity = %#v, want 1", lineItem.Quantity)
	}
	if lineItem.PriceData.Currency == nil || *lineItem.PriceData.Currency != "cny" {
		t.Fatalf("currency = %#v, want cny", lineItem.PriceData.Currency)
	}
	if lineItem.PriceData.UnitAmount == nil || *lineItem.PriceData.UnitAmount != 1292 {
		t.Fatalf("unit_amount = %#v, want 1292", lineItem.PriceData.UnitAmount)
	}
	if lineItem.PriceData.ProductData == nil || lineItem.PriceData.ProductData.Name == nil || *lineItem.PriceData.ProductData.Name != "TokenRouter Balance" {
		t.Fatalf("product data = %#v", lineItem.PriceData.ProductData)
	}
	if params.ExpiresAt == nil || *params.ExpiresAt != expiresAt.Unix() {
		t.Fatalf("expires_at = %#v, want %d", params.ExpiresAt, expiresAt.Unix())
	}
	gotExpand := strings.Join(stripeStringValues(params.Expand), ",")
	if !strings.Contains(gotExpand, "invoice") || !strings.Contains(gotExpand, "payment_intent") {
		t.Fatalf("expand = %q, want invoice and payment_intent", gotExpand)
	}
}

func TestStripeCheckoutReturnURLRequiresBaseURL(t *testing.T) {
	t.Parallel()

	if got := stripeCheckoutReturnURL("", "success"); got != "" {
		t.Fatalf("return URL = %q, want empty fallback", got)
	}
	if got := stripeCheckoutReturnURL("https://app.example.com/payment/result", ""); got != "https://app.example.com/payment/result" {
		t.Fatalf("return URL = %q, want unchanged base URL", got)
	}
	if got := stripeCheckoutReturnURL("https://app.example.com/payment/result?order_id=101&status=success", "cancelled"); got != "https://app.example.com/payment/result?order_id=101&status=cancelled" {
		t.Fatalf("return URL = %q, want status overwritten", got)
	}
}

func TestStripePaymentIntentIDFromClientSecret(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		secret string
		want   string
	}{
		{
			name:   "extracts payment intent id",
			secret: "pi_123_secret_abc",
			want:   "pi_123",
		},
		{
			name:   "trims whitespace before extracting",
			secret: "  pi_456_secret_def  ",
			want:   "pi_456",
		},
		{
			name:   "rejects non payment intent secret",
			secret: "seti_123_secret_abc",
			want:   "",
		},
		{
			name:   "requires secret marker",
			secret: "pi_123",
			want:   "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := stripePaymentIntentIDFromClientSecret(tt.secret); got != tt.want {
				t.Fatalf("payment intent id = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestParseStripeInvoiceUsesInvoicePaymentIntent(t *testing.T) {
	t.Parallel()

	rawBody := `{"id":"evt_invoice_paid"}`
	invoiceRaw := stripeInvoiceEventRaw(t, map[string]any{
		"id":                 "in_123",
		"object":             "invoice",
		"amount_paid":        1234,
		"amount_due":         1234,
		"status":             "paid",
		"hosted_invoice_url": "https://stripe.example/invoice/in_123",
		"invoice_pdf":        "https://stripe.example/invoice/in_123.pdf",
		"metadata": map[string]string{
			"orderId": "sub2_order_123",
		},
		"payments": map[string]any{
			"object": "list",
			"data": []any{
				map[string]any{
					"id":     "inpay_123",
					"object": "invoice_payment",
					"payment": map[string]any{
						"type": "payment_intent",
						"payment_intent": map[string]any{
							"id":     "pi_123",
							"object": "payment_intent",
						},
					},
				},
			},
		},
	})

	notification, err := parseStripeInvoice(&stripe.Event{
		Data: &stripe.EventData{Raw: invoiceRaw},
	}, payment.ProviderStatusSuccess, rawBody)
	if err != nil {
		t.Fatalf("parse invoice: %v", err)
	}

	if notification.TradeNo != "pi_123" {
		t.Fatalf("trade no = %q, want %q", notification.TradeNo, "pi_123")
	}
	if notification.OrderID != "sub2_order_123" {
		t.Fatalf("order id = %q, want %q", notification.OrderID, "sub2_order_123")
	}
	if notification.Amount != 12.34 {
		t.Fatalf("amount = %.2f, want 12.34", notification.Amount)
	}
	if notification.Status != payment.ProviderStatusSuccess {
		t.Fatalf("status = %q, want %q", notification.Status, payment.ProviderStatusSuccess)
	}
	if notification.RawData != rawBody {
		t.Fatalf("raw body = %q, want %q", notification.RawData, rawBody)
	}
	if notification.Metadata["invoice_id"] != "in_123" {
		t.Fatalf("invoice_id metadata = %q", notification.Metadata["invoice_id"])
	}
	if notification.Metadata["invoice_status"] != "paid" {
		t.Fatalf("invoice_status metadata = %q", notification.Metadata["invoice_status"])
	}
	if notification.Metadata["invoice_url"] != "https://stripe.example/invoice/in_123" {
		t.Fatalf("invoice_url metadata = %q", notification.Metadata["invoice_url"])
	}
	if notification.Metadata["invoice_pdf"] != "https://stripe.example/invoice/in_123.pdf" {
		t.Fatalf("invoice_pdf metadata = %q", notification.Metadata["invoice_pdf"])
	}
}

func TestParseStripeCheckoutSessionUsesPaymentIntentAndInvoiceMetadata(t *testing.T) {
	t.Parallel()

	rawBody := `{"id":"evt_checkout_completed"}`
	checkoutRaw := stripeCheckoutSessionEventRaw(t, map[string]any{
		"id":             "cs_test_123",
		"object":         "checkout.session",
		"amount_total":   1292,
		"currency":       "cny",
		"payment_status": "paid",
		"metadata": map[string]string{
			"orderId": "sub2_order_123",
		},
		"payment_intent": map[string]any{
			"id":     "pi_123",
			"object": "payment_intent",
		},
		"invoice": map[string]any{
			"id":                 "in_123",
			"object":             "invoice",
			"status":             "paid",
			"hosted_invoice_url": "https://stripe.example/invoice/in_123",
			"invoice_pdf":        "https://stripe.example/invoice/in_123.pdf",
		},
	})

	notification, err := parseStripeCheckoutSession(&stripe.Event{
		Data: &stripe.EventData{Raw: checkoutRaw},
	}, payment.ProviderStatusSuccess, rawBody)
	if err != nil {
		t.Fatalf("parse checkout session: %v", err)
	}

	if notification.TradeNo != "pi_123" {
		t.Fatalf("trade no = %q, want pi_123", notification.TradeNo)
	}
	if notification.OrderID != "sub2_order_123" {
		t.Fatalf("order id = %q, want sub2_order_123", notification.OrderID)
	}
	if notification.Amount != 12.92 {
		t.Fatalf("amount = %.2f, want 12.92", notification.Amount)
	}
	if notification.Metadata["currency"] != "CNY" {
		t.Fatalf("currency metadata = %q", notification.Metadata["currency"])
	}
	if notification.Metadata["invoice_id"] != "in_123" {
		t.Fatalf("invoice_id metadata = %q", notification.Metadata["invoice_id"])
	}
	if notification.Metadata["invoice_url"] != "https://stripe.example/invoice/in_123" {
		t.Fatalf("invoice_url metadata = %q", notification.Metadata["invoice_url"])
	}
	if notification.RawData != rawBody {
		t.Fatalf("raw body = %q, want %q", notification.RawData, rawBody)
	}
}

func TestParseStripeCheckoutSessionIgnoresUnpaidCompletedSession(t *testing.T) {
	t.Parallel()

	checkoutRaw := stripeCheckoutSessionEventRaw(t, map[string]any{
		"id":             "cs_async_pending",
		"object":         "checkout.session",
		"amount_total":   1292,
		"currency":       "cny",
		"payment_status": "unpaid",
		"metadata": map[string]string{
			"orderId": "sub2_async_pending",
		},
	})

	notification, err := parseStripeCheckoutSession(&stripe.Event{
		Data: &stripe.EventData{Raw: checkoutRaw},
	}, payment.ProviderStatusSuccess, "{}")
	if err != nil {
		t.Fatalf("parse checkout session: %v", err)
	}
	if notification != nil {
		t.Fatalf("notification = %#v, want nil before async payment succeeds", notification)
	}
}

func TestParseStripeCheckoutSessionKeepsAsyncFailureNotification(t *testing.T) {
	t.Parallel()

	checkoutRaw := stripeCheckoutSessionEventRaw(t, map[string]any{
		"id":             "cs_async_failed",
		"object":         "checkout.session",
		"amount_total":   1292,
		"currency":       "cny",
		"payment_status": "unpaid",
		"metadata": map[string]string{
			"orderId": "sub2_async_failed",
		},
	})

	notification, err := parseStripeCheckoutSession(&stripe.Event{
		Data: &stripe.EventData{Raw: checkoutRaw},
	}, payment.ProviderStatusFailed, "{}")
	if err != nil {
		t.Fatalf("parse checkout session: %v", err)
	}
	if notification == nil {
		t.Fatal("notification should be present for async failure")
	}
	if notification.Status != payment.ProviderStatusFailed {
		t.Fatalf("status = %q, want failed", notification.Status)
	}
	if notification.TradeNo != "cs_async_failed" {
		t.Fatalf("trade no = %q, want checkout session id fallback", notification.TradeNo)
	}
}

func TestStripeCheckoutInvoiceDocument(t *testing.T) {
	t.Parallel()

	doc := stripeCheckoutInvoiceDocument(&stripe.CheckoutSession{
		ID: "cs_123",
		Invoice: &stripe.Invoice{
			ID:               "in_123",
			Status:           stripe.InvoiceStatusPaid,
			HostedInvoiceURL: "https://stripe.example/invoice/in_123",
			InvoicePDF:       "https://stripe.example/invoice/in_123.pdf",
		},
	})

	if doc.Type != "invoice" {
		t.Fatalf("type = %q, want invoice", doc.Type)
	}
	if doc.URL != "https://stripe.example/invoice/in_123" {
		t.Fatalf("url = %q, want hosted invoice url", doc.URL)
	}
	if doc.InvoiceID != "in_123" || doc.InvoiceStatus != string(stripe.InvoiceStatusPaid) {
		t.Fatalf("invoice metadata = %#v", doc)
	}
}

func TestParseStripeInvoiceFallsBackToInvoiceIDAndAmountDue(t *testing.T) {
	t.Parallel()

	invoiceRaw := stripeInvoiceEventRaw(t, map[string]any{
		"id":          "in_due",
		"object":      "invoice",
		"amount_paid": 0,
		"amount_due":  8800,
		"status":      "open",
		"metadata": map[string]string{
			"orderId": "sub2_due",
		},
	})

	notification, err := parseStripeInvoice(&stripe.Event{
		Data: &stripe.EventData{Raw: invoiceRaw},
	}, payment.ProviderStatusFailed, "{}")
	if err != nil {
		t.Fatalf("parse invoice: %v", err)
	}

	if notification.TradeNo != "in_due" {
		t.Fatalf("trade no = %q, want %q", notification.TradeNo, "in_due")
	}
	if notification.Amount != 88 {
		t.Fatalf("amount = %.2f, want 88.00", notification.Amount)
	}
	if notification.Status != payment.ProviderStatusFailed {
		t.Fatalf("status = %q, want %q", notification.Status, payment.ProviderStatusFailed)
	}
}

func TestStripeInvoiceDocumentResponse(t *testing.T) {
	t.Parallel()

	withHosted := stripeInvoiceDocumentResponse(&stripe.Invoice{
		ID:               "in_hosted",
		Status:           stripe.InvoiceStatusPaid,
		HostedInvoiceURL: " https://stripe.example/invoice/hosted ",
		InvoicePDF:       "https://stripe.example/invoice/hosted.pdf",
	})
	if withHosted.Type != "invoice" {
		t.Fatalf("type = %q, want invoice", withHosted.Type)
	}
	if withHosted.URL != "https://stripe.example/invoice/hosted" {
		t.Fatalf("url = %q, want hosted invoice url", withHosted.URL)
	}
	if withHosted.HostedInvoiceURL != " https://stripe.example/invoice/hosted " {
		t.Fatalf("hosted invoice url should preserve provider value")
	}
	if withHosted.InvoicePDF != "https://stripe.example/invoice/hosted.pdf" {
		t.Fatalf("invoice pdf = %q", withHosted.InvoicePDF)
	}
	if withHosted.InvoiceID != "in_hosted" {
		t.Fatalf("invoice id = %q", withHosted.InvoiceID)
	}
	if withHosted.InvoiceStatus != string(stripe.InvoiceStatusPaid) {
		t.Fatalf("invoice status = %q", withHosted.InvoiceStatus)
	}

	withPDF := stripeInvoiceDocumentResponse(&stripe.Invoice{
		ID:         "in_pdf",
		InvoicePDF: " https://stripe.example/invoice/pdf.pdf ",
	})
	if withPDF.URL != "https://stripe.example/invoice/pdf.pdf" {
		t.Fatalf("url = %q, want pdf fallback", withPDF.URL)
	}

	empty := stripeInvoiceDocumentResponse(nil)
	if empty == nil || empty.Type != "invoice" {
		t.Fatalf("nil invoice response = %#v", empty)
	}
}

func stripeInvoiceEventRaw(t *testing.T, invoice map[string]any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(invoice)
	if err != nil {
		t.Fatalf("marshal invoice fixture: %v", err)
	}
	return raw
}

func stripeCheckoutSessionEventRaw(t *testing.T, session map[string]any) json.RawMessage {
	t.Helper()

	raw, err := json.Marshal(session)
	if err != nil {
		t.Fatalf("marshal checkout session fixture: %v", err)
	}
	return raw
}

func stripeStringValues(values []*string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			out = append(out, "")
			continue
		}
		out = append(out, *value)
	}
	return out
}
