// 旧支付 HTTP 名称只委托新 Adapter，S15/S16 清理。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/service"

	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
)

type PaymentWebhookHandler = paymenthttp.PaymentWebhookHandler

func NewPaymentWebhookHandler(paymentService *service.PaymentService, registry *payment.Registry) *PaymentWebhookHandler {
	return paymenthttp.NewPaymentWebhookHandler(paymentService.NativeRuntime(), registry)
}
