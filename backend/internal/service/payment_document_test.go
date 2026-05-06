package service

import (
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

func TestPaymentDocumentFromOrderPrefersHostedInvoiceURL(t *testing.T) {
	t.Parallel()

	invoiceID := "in_hosted"
	hostedURL := " https://stripe.example/invoice/hosted "
	pdfURL := "https://stripe.example/invoice/hosted.pdf"
	status := "paid"
	doc := paymentDocumentFromOrder(&dbent.PaymentOrder{
		PaymentInvoiceID:     &invoiceID,
		PaymentInvoiceURL:    &hostedURL,
		PaymentInvoicePdfURL: &pdfURL,
		PaymentInvoiceStatus: &status,
	})

	if doc == nil {
		t.Fatal("expected invoice document, got nil")
	}
	if doc.Type != "invoice" {
		t.Fatalf("type = %q, want invoice", doc.Type)
	}
	if doc.URL != "https://stripe.example/invoice/hosted" {
		t.Fatalf("url = %q, want trimmed hosted url", doc.URL)
	}
	if doc.HostedInvoiceURL != "https://stripe.example/invoice/hosted" {
		t.Fatalf("hosted invoice url = %q", doc.HostedInvoiceURL)
	}
	if doc.InvoicePDF != "https://stripe.example/invoice/hosted.pdf" {
		t.Fatalf("invoice pdf = %q", doc.InvoicePDF)
	}
	if doc.InvoiceID != "in_hosted" {
		t.Fatalf("invoice id = %q", doc.InvoiceID)
	}
	if doc.InvoiceStatus != "paid" {
		t.Fatalf("invoice status = %q", doc.InvoiceStatus)
	}
}

func TestPaymentDocumentFromOrderFallsBackToPDFURL(t *testing.T) {
	t.Parallel()

	pdfURL := " https://stripe.example/invoice/fallback.pdf "
	doc := paymentDocumentFromOrder(&dbent.PaymentOrder{
		PaymentInvoicePdfURL: &pdfURL,
	})

	if doc == nil {
		t.Fatal("expected invoice document, got nil")
	}
	if doc.URL != "https://stripe.example/invoice/fallback.pdf" {
		t.Fatalf("url = %q, want trimmed pdf url", doc.URL)
	}
	if doc.HostedInvoiceURL != "" {
		t.Fatalf("hosted invoice url = %q, want empty", doc.HostedInvoiceURL)
	}
	if doc.InvoicePDF != "https://stripe.example/invoice/fallback.pdf" {
		t.Fatalf("invoice pdf = %q", doc.InvoicePDF)
	}
}

func TestPaymentDocumentFromOrderReturnsNilWithoutLinks(t *testing.T) {
	t.Parallel()

	invoiceID := "in_without_links"
	if doc := paymentDocumentFromOrder(&dbent.PaymentOrder{PaymentInvoiceID: &invoiceID}); doc != nil {
		t.Fatalf("expected nil document, got %#v", doc)
	}
	if doc := paymentDocumentFromOrder(nil); doc != nil {
		t.Fatalf("expected nil document for nil order, got %#v", doc)
	}
}
