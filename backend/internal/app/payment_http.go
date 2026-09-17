// 支付路由直接使用新 Adapter；旧聚合 Handlers 仅保留类型别名。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

func provideNativePaymentRuntime(old *service.PaymentService) *payment.Runtime {
	return old.NativeRuntime()
}
func providePaymentHTTP(runtime *payment.Runtime, cfg *payment.ConfigService, plans *billing.Plans) *paymenthttp.PaymentHandler {
	return paymenthttp.NewPaymentHandler(runtime, cfg, plans)
}
func providePaymentAdminHTTP(runtime *payment.Runtime, cfg *payment.ConfigService, plans *billing.Plans) *paymenthttp.AdminHandler {
	return paymenthttp.NewAdminHandler(runtime, cfg, plans)
}
func providePaymentWebhookHTTP(runtime *payment.Runtime, registry *payment.Registry) *paymenthttp.PaymentWebhookHandler {
	return paymenthttp.NewPaymentWebhookHandler(runtime, registry)
}
