// 旧下单入口只负责身份与配置投影；所有规则委托 payment。
package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/payment/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/servertiming"
)

func paymentBuyer(user *User) *payment.Buyer {
	if user == nil {
		return nil
	}
	return &payment.Buyer{ID: user.ID, Email: user.Email, Username: user.Username, Notes: user.Notes, Status: user.Status}
}
func (s *PaymentService) paymentCheckout() *payment.Checkout {
	if s.checkout != nil {
		return s.checkout
	}
	var remember func(context.Context, int64, string, string)
	if s.notificationEmailService != nil {
		remember = s.notificationEmailService.RememberRecipientLocale
	}
	return payment.NewCheckout(paymentpostgres.NewOrderStore(s.entClient), s.configService.paymentCoreConfig(), s.loadBalancer, s.paymentResume(), payment.CheckoutRuntime{User: func(ctx context.Context, id int64) (*payment.Buyer, error) {
		u, e := s.userRepo.GetByID(ctx, id)
		return paymentBuyer(u), e
	}, WeChatCredential: s.getWeChatPaymentOAuthCredential, CreateProvider: provider.CreateProvider, RememberLocale: remember, Audit: s.writeAuditLog, Error: slog.Error, Observe: func(ctx context.Context) func() { return servertiming.ObserveDependency(ctx, "payment") }})
}
func (s *PaymentService) BindCheckout(checkout *payment.Checkout) { s.checkout = checkout }

// BindPaymentResume 与新下单入口共享 app 投影的签名实例。
func (s *PaymentService) BindPaymentResume(resume *payment.PaymentResumeService) {
	s.resumeService = resume
}
