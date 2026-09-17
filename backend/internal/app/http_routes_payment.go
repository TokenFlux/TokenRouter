package app

import (
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	gin "github.com/gin-gonic/gin"
)

func providePaymentRouteMount(user *paymenthttp.PaymentHandler, webhook *paymenthttp.PaymentWebhookHandler, admin *paymenthttp.AdminHandler, plans *billinghttp.PlanHandler) paymentRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		paymenthttp.RegisterRoutes(v1, user, webhook, admin, plans, paymenthttp.RouteMiddleware{JWT: security.JWT, BackendMode: security.BackendUser, Panel: security.Panel.Global(), Admin: security.Admin, Audit: security.Audit})
	}
}
