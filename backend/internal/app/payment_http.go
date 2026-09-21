// 支付路由直接使用新 Adapter，HTTP 构造与注册由 app 分别装配。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

func providePaymentHTTP(runtime *payment.Runtime, cfg *payment.ConfigService, plans *billing.Plans) *paymenthttp.PaymentHandler {
	return paymenthttp.NewPaymentHandler(runtime, cfg, plans)
}
func providePaymentAdminHTTP(runtime *payment.Runtime, cfg *payment.ConfigService, plans *billing.Plans, calendar timezone.Calendar) *paymenthttp.AdminHandler {
	return paymenthttp.NewAdminHandler(runtime, cfg, plans, calendar)
}
func providePaymentWebhookHTTP(runtime *payment.Runtime, registry *payment.Registry) *paymenthttp.PaymentWebhookHandler {
	return paymenthttp.NewPaymentWebhookHandler(runtime, registry)
}
