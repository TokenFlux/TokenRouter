package admin

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

func TestSanitizeAdminPaymentOrderForResponseAddsCurrencyAndKeepsForkFields(t *testing.T) {
	now := time.Now()
	invoiceID := "in_202606250001"
	invoiceURL := "https://pay.example.com/invoices/in_202606250001"
	invoicePDF := "https://pay.example.com/invoices/in_202606250001.pdf"
	invoiceStatus := "paid"
	order := &dbent.PaymentOrder{
		ID:                   1,
		UserID:               2,
		Amount:               100,
		PayAmount:            108,
		FeeRate:              8,
		FeeFixed:             2,
		FeeRateAmount:        6,
		FeeAmount:            8,
		OutTradeNo:           "sub2_202606250001",
		PaymentType:          "stripe",
		OrderType:            "subscription",
		Status:               "COMPLETED",
		PaymentInvoiceID:     &invoiceID,
		PaymentInvoiceURL:    &invoiceURL,
		PaymentInvoicePdfURL: &invoicePDF,
		PaymentInvoiceStatus: &invoiceStatus,
		BillingSnapshot: map[string]any{
			"email": "buyer@example.com",
		},
		ProviderSnapshot: map[string]any{
			"schema_version": 2,
			"currency":       "USD",
			"secret":         "should-not-leak",
		},
		ExpiresAt: now,
		CreatedAt: now,
		UpdatedAt: now,
	}

	got := sanitizeAdminPaymentOrderForResponse(order)
	require.NotNil(t, got)
	if got.Currency != "USD" {
		t.Fatalf("expected currency USD, got %q", got.Currency)
	}
	if got.FeeFixed != 2 || got.FeeRateAmount != 6 || got.FeeAmount != 8 {
		t.Fatalf("expected fork fee fields to be preserved, got fixed=%v rate=%v total=%v", got.FeeFixed, got.FeeRateAmount, got.FeeAmount)
	}
	if got.PaymentInvoiceID == nil || *got.PaymentInvoiceID != invoiceID {
		t.Fatalf("expected payment invoice id %q, got %#v", invoiceID, got.PaymentInvoiceID)
	}
	if got.BillingSnapshot["email"] != "buyer@example.com" {
		t.Fatalf("expected billing snapshot to be preserved, got %#v", got.BillingSnapshot)
	}

	body, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal sanitized order: %v", err)
	}
	if strings.Contains(string(body), "provider_snapshot") || strings.Contains(string(body), "should-not-leak") {
		t.Fatalf("expected provider_snapshot to be omitted, got %s", string(body))
	}
}
