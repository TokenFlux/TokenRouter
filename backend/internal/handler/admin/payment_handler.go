// 旧支付 HTTP 名称只委托新 Adapter，S15/S16 清理。
package admin

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
)

type PaymentHandler = paymenthttp.AdminHandler

func NewPaymentHandler(paymentService *service.PaymentService, configService *service.PaymentConfigService) *PaymentHandler {
	var plans *billing.Plans
	if configService != nil {
		plans = configService.Plans()
	}
	return paymenthttp.NewAdminHandler(paymentService.NativeRuntime(), configService.NativeConfig(), plans)
}

type ForceExpireOrderRequest = paymenthttp.ForceExpireOrderRequest
type AdminPaymentOrderResult = paymenthttp.AdminPaymentOrderResult

func sanitizeAdminPaymentOrderForResponse(order *dbent.PaymentOrder) *AdminPaymentOrderResult {
	return paymenthttp.AdminSanitizeAdminPaymentOrderForResponse(service.PaymentOrderSnapshot(order))
}

type AdminProcessRefundRequest = paymenthttp.AdminProcessRefundRequest
