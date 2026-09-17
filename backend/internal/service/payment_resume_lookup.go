package service

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
)

func (s *PaymentService) GetPublicOrderByResumeToken(ctx context.Context, token string) (*dbent.PaymentOrder, error) {
	v, e := s.paymentOrderLifecycle().GetPublicOrderByResumeToken(ctx, token)
	return paymentOrderEntity(v), e
}

func (s *PaymentService) ParseWeChatPaymentResumeToken(token string) (*WeChatPaymentResumeClaims, error) {
	return s.paymentOrderLifecycle().ParseWeChatPaymentResumeToken(token)
}
