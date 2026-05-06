//go:build unit

package provider

import (
	"encoding/json"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	stripe "github.com/stripe/stripe-go/v85"
)

func TestResolveStripeInvoiceMethodTypes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{
			name:  "empty config defaults to card and link",
			input: "",
			want:  []string{"card", "link"},
		},
		{
			name:  "stripe top-level method expands to supported invoice methods",
			input: "stripe",
			want:  []string{"card", "link", "wechat_pay"},
		},
		{
			name:  "unsupported alipay is ignored for invoice confirmation",
			input: "alipay,card,link,wxpay",
			want:  []string{"card", "link", "wechat_pay"},
		},
		{
			name:  "duplicates are removed while preserving first occurrence",
			input: "card,stripe,link,wxpay",
			want:  []string{"card", "link", "wechat_pay"},
		},
		{
			name:  "only unsupported values falls back to card",
			input: "alipay,unknown",
			want:  []string{"card"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := resolveStripeInvoiceMethodTypes(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("method count = %d, want %d: %#v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Fatalf("method[%d] = %q, want %q: %#v", i, got[i], tt.want[i], got)
				}
			}
		})
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
