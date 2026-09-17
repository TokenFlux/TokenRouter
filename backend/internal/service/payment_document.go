package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentService) GetOrderPaymentDocument(ctx context.Context, orderID, userID int64) (*payment.PaymentDocumentResponse, error) {
	return s.paymentQueries().GetOrderPaymentDocument(ctx, orderID, userID)
}

func (s *PaymentService) AdminGetOrderPaymentDocument(ctx context.Context, orderID int64) (*payment.PaymentDocumentResponse, error) {
	return s.paymentQueries().AdminGetOrderPaymentDocument(ctx, orderID)
}

func paymentDocumentFromOrder(order *dbent.PaymentOrder) *payment.PaymentDocumentResponse {
	return payment.PaymentDocumentFromOrder(paymentOrderValue(order))
}
