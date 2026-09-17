// 账单与历史收据查询复用原订单绑定和渠道端口。
package payment

import (
	"context"
	"fmt"
	"strings"

	infraerrors "github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
)

// GetOrderPaymentDocument 返回用户订单对应的 Stripe invoice 或历史 receipt。
func (s *OrderQueries) GetOrderPaymentDocument(ctx context.Context, orderID, userID int64) (*PaymentDocumentResponse, error) {
	order, err := s.GetOrder(ctx, orderID, userID)
	if err != nil {
		return nil, err
	}
	return s.GetOrderPaymentDocumentValue(ctx, order)
}

// AdminGetOrderPaymentDocument 返回管理员可查看的订单账单或收据。
func (s *OrderQueries) AdminGetOrderPaymentDocument(ctx context.Context, orderID int64) (*PaymentDocumentResponse, error) {
	order, err := s.GetOrderByID(ctx, orderID)
	if err != nil {
		return nil, err
	}
	return s.GetOrderPaymentDocumentValue(ctx, order)
}
func (s *OrderQueries) GetOrderPaymentDocumentValue(ctx context.Context, order *Order) (*PaymentDocumentResponse, error) {
	if order == nil {
		return nil, infraerrors.NotFound("NOT_FOUND", "order not found")
	}
	if GetBasePaymentType(order.PaymentType) != TypeStripe && strings.TrimSpace(refundStringValue(order.ProviderKey)) != TypeStripe {
		return nil, infraerrors.BadRequest("PAYMENT_DOCUMENT_UNSUPPORTED", "payment document is only supported for Stripe orders")
	}

	if doc := PaymentDocumentFromOrder(order); doc != nil {
		return doc, nil
	}

	prov, err := s.provider(ctx, order)
	if err != nil {
		return nil, fmt.Errorf("load order provider: %w", err)
	}
	docProvider, ok := prov.(DocumentProvider)
	if !ok {
		return nil, infraerrors.BadRequest("PAYMENT_DOCUMENT_UNSUPPORTED", "payment provider does not support documents")
	}
	doc, err := docProvider.GetPaymentDocument(ctx, refundStringValue(order.PaymentInvoiceID), strings.TrimSpace(order.PaymentTradeNo))
	if err != nil {
		return nil, fmt.Errorf("get payment document: %w", err)
	}
	if doc == nil || strings.TrimSpace(doc.URL) == "" {
		return nil, infraerrors.NotFound("PAYMENT_DOCUMENT_NOT_FOUND", "payment document is not available")
	}
	return doc, nil
}
func PaymentDocumentFromOrder(order *Order) *PaymentDocumentResponse {
	if order == nil {
		return nil
	}
	hostedURL := strings.TrimSpace(refundStringValue(order.PaymentInvoiceURL))
	pdfURL := strings.TrimSpace(refundStringValue(order.PaymentInvoicePdfURL))
	if hostedURL == "" && pdfURL == "" {
		return nil
	}
	url := hostedURL
	if url == "" {
		url = pdfURL
	}
	return &PaymentDocumentResponse{
		Type:             "invoice",
		URL:              url,
		HostedInvoiceURL: hostedURL,
		InvoicePDF:       pdfURL,
		InvoiceID:        refundStringValue(order.PaymentInvoiceID),
		InvoiceStatus:    refundStringValue(order.PaymentInvoiceStatus),
	}
}
