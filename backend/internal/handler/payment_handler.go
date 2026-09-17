// 旧支付 HTTP 名称只委托新 Adapter，S15/S16 清理。
package handler

import (
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
)

type PaymentHandler = paymenthttp.PaymentHandler

func NewPaymentHandler(paymentService *service.PaymentService, configService *service.PaymentConfigService) *PaymentHandler {
	var plans *billing.Plans
	if configService != nil {
		plans = configService.Plans()
	}
	return paymenthttp.NewPaymentHandler(paymentService.NativeRuntime(), configService.NativeConfig(), plans)
}

type CreateOrderRequest = paymenthttp.CreateOrderRequest

type RefundRequestBody = paymenthttp.RefundRequestBody
type VerifyOrderRequest = paymenthttp.VerifyOrderRequest
type ResolveOrderByResumeTokenRequest = paymenthttp.ResolveOrderByResumeTokenRequest
type PublicOrderResult = paymenthttp.PublicOrderResult
type PublicOrderVerifyResult = paymenthttp.PublicOrderVerifyResult

type PaymentOrderResult = paymenthttp.PaymentOrderResult
