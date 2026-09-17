package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"
)

func (s *PaymentService) GetWebhookProvider(ctx context.Context, providerKey, outTradeNo string) (payment.Provider, error) {
	return s.paymentBindings().GetWebhookProvider(ctx, providerKey, outTradeNo)
}

func (s *PaymentService) GetWebhookProviders(ctx context.Context, providerKey, outTradeNo string) ([]payment.Provider, error) {
	return s.paymentBindings().GetWebhookProviders(ctx, providerKey, outTradeNo)
}
