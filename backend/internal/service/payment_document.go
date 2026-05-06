package service

import (
	"context"
	"fmt"
	"strings"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/errors"
)

// GetOrderPaymentDocument 返回用户订单对应的 Stripe invoice 或历史 receipt。
func (s *PaymentService) GetOrderPaymentDocument(ctx context.Context, orderID, userID int64) (*payment.PaymentDocumentResponse, error) {
	order, err := s.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	return s.getOrderPaymentDocument(ctx, order)
}

// AdminGetOrderPaymentDocument 返回管理员可查看的订单账单或收据。
func (s *PaymentService) AdminGetOrderPaymentDocument(ctx context.Context, orderID int64) (*payment.PaymentDocumentResponse, error) {
	order, err := s.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return s.getOrderPaymentDocument(ctx, order)
}

func (s *PaymentService) getOrderPaymentDocument(ctx context.Context, order *dbent.PaymentOrder) (*payment.PaymentDocumentResponse, error) {
	if order == nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if payment.GetBasePaymentType(order.PaymentType) != payment.TypeStripe && strings.TrimSpace(psStringValue(order.ProviderKey)) != payment.TypeStripe {
		return nil, infraerrors.BadRequest("PAYMENT_DOCUMENT_UNSUPPORTED", "payment document is only supported for Stripe orders")
	}

	if doc := paymentDocumentFromOrder(order); doc != nil {
		return doc, nil
	}

	prov, err := s.getOrderProvider(ctx, order)
	if err != nil {
		return nil, fmt.Errorf("load order provider: %w", err)
	}
	docProvider, ok := prov.(payment.DocumentProvider)
	if !ok {
		return nil, infraerrors.BadRequest("PAYMENT_DOCUMENT_UNSUPPORTED", "payment provider does not support documents")
	}
	doc, err := docProvider.GetPaymentDocument(ctx, psStringValue(order.PaymentInvoiceID), strings.TrimSpace(order.PaymentTradeNo))
	if err != nil {
		return nil, fmt.Errorf("get payment document: %w", err)
	}
	if doc == nil || strings.TrimSpace(doc.URL) == "" {
		return nil, infraerrors.NotFound("PAYMENT_DOCUMENT_NOT_FOUND", "payment document is not available")
	}
	return doc, nil
}

func paymentDocumentFromOrder(order *dbent.PaymentOrder) *payment.PaymentDocumentResponse {
	if order == nil {
		return nil
	}
	hostedURL := strings.TrimSpace(psStringValue(order.PaymentInvoiceURL))
	pdfURL := strings.TrimSpace(psStringValue(order.PaymentInvoicePdfURL))
	if hostedURL == "" && pdfURL == "" {
		return nil
	}
	url := hostedURL
	if url == "" {
		url = pdfURL
	}
	return &payment.PaymentDocumentResponse{
		Type:             "invoice",
		URL:              url,
		HostedInvoiceURL: hostedURL,
		InvoicePDF:       pdfURL,
		InvoiceID:        psStringValue(order.PaymentInvoiceID),
		InvoiceStatus:    psStringValue(order.PaymentInvoiceStatus),
	}
}
