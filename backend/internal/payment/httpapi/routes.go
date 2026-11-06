package httpapi

import (
	"github.com/gin-gonic/gin"

	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/server/adminapi"
)

// routes.go registers buyer, public recovery, webhook, and administrative
// payment endpoints with their supplied guards.
// Payment routes expose authenticated buyer operations, anonymous recovery and
// provider callbacks under separate groups. RegisterRoutes applies the
// supplied buyer and administrator guard sequences, while public recovery
// reads and webhook signature verification are handled by their endpoints;
// payment OAuth is attached separately to the identity route group.

// PlanEndpoints supplies plan browsing and administrator mutations mounted by
// payment route registration.
type PlanEndpoints interface {
	// GetPlans writes the available purchase plans for the buyer payment page.
	GetPlans(*gin.Context)
	// ListPlans writes the administrator listing of persisted subscription plans.
	ListPlans(*gin.Context)
	// CreatePlan validates and persists a subscription plan from the
	// administrator request.
	CreatePlan(*gin.Context)
	// UpdatePlan applies the administrator request to its addressed subscription
	// plan.
	UpdatePlan(*gin.Context)
	// DeletePlan removes the addressed subscription plan according to billing
	// rules.
	DeletePlan(*gin.Context)
}

// RouteMiddleware contains the supplied guards applied to buyer and
// administrator payment route groups.
type RouteMiddleware struct {
	// JWT, BackendMode and Panel run in that order for buyer payment routes.
	// Admin, Panel and Audit run in that order for administrator payment routes.
	// Public/callback groups add none of these guards here.
	//
	// JWT: buyer authentication handler. BackendMode: buyer backend access
	// policy. Panel: panel request limiter. Admin: administrator access guard.
	// Audit: administrator request audit handler.
	JWT, BackendMode, Panel, Admin, Audit gin.HandlerFunc
}

// WeChatAuthEndpoints supplies payment-specific OAuth redirects mounted on the
// identity route group.
type WeChatAuthEndpoints interface {
	// WeChatPaymentOAuthStart stores payment OAuth context and redirects to
	// WeChat authorization. It uses the request's payment method/order context;
	// account login authentication remains separate from this route operation.
	WeChatPaymentOAuthStart(*gin.Context)
	// WeChatPaymentOAuthCallback validates payment OAuth callback context and
	// returns a signed payment continuation to the result page. The endpoint owns
	// its cookie cleanup and redirects; registration supplies no additional
	// middleware here.
	WeChatPaymentOAuthCallback(*gin.Context)
}

// RegisterRoutes mounts buyer payment, public recovery, provider callback and
// administrator handlers beneath v1. Supply nonnil handlers, plan endpoints
// and usable guards; any middleware already on v1 also applies. Buyer routes
// receive JWT/BackendMode/Panel, administrator routes receive
// Admin/Panel/Audit, and public/callback groups rely on their endpoint rules.
func RegisterRoutes(
	v1 *gin.RouterGroup,
	paymentHandler *PaymentHandler,
	webhookHandler *PaymentWebhookHandler,
	adminPaymentHandler *AdminHandler,
	planHandler PlanEndpoints,
	guards RouteMiddleware,
) {
	authenticated := v1.Group("/payment")
	authenticated.Use(guards.JWT)
	authenticated.Use(guards.BackendMode)
	authenticated.Use(guards.Panel)
	{
		authenticated.GET("/config", paymentHandler.GetPaymentConfig)
		authenticated.GET("/checkout-info", paymentHandler.GetCheckoutInfo)
		authenticated.GET("/plans", planHandler.GetPlans)
		authenticated.GET("/limits", paymentHandler.GetLimits)

		orders := authenticated.Group("/orders")
		{
			orders.POST("", paymentHandler.CreateOrder)
			orders.POST("/verify", paymentHandler.VerifyOrder)
			orders.GET("/my", paymentHandler.GetMyOrders)
			orders.GET("/:id", paymentHandler.GetOrder)
			orders.GET("/:id/invoice", paymentHandler.GetOrderInvoice)
			orders.POST("/:id/cancel", paymentHandler.CancelOrder)
			orders.POST("/:id/refund-request", paymentHandler.RequestRefund)
			orders.GET("/refund-eligible-providers", paymentHandler.GetRefundEligibleProviders)
		}
	}

	// Public token resolution and legacy merchant-number verification bypass
	// buyer guards in this registration. Their handlers return selected saved
	// fields according to each recovery input's validation.
	public := v1.Group("/payment/public")
	{
		public.POST("/orders/verify", paymentHandler.VerifyOrderPublic)
		public.POST("/orders/resolve", paymentHandler.ResolveOrderPublicByResumeToken)
	}

	// Callback groups are public here because provider verification authenticates
	// their payloads. EasyPay retains GET compatibility while the other listed
	// provider callbacks read POST bodies.
	webhook := v1.Group("/payment/webhook")
	{
		webhook.GET("/easypay", webhookHandler.EasyPayNotify)
		webhook.POST("/easypay", webhookHandler.EasyPayNotify)
		webhook.POST("/epusdt", webhookHandler.EPUSDTNotify)
		webhook.GET("/epusdt", webhookHandler.EPUSDTNotify)
		webhook.POST("/alipay", webhookHandler.AlipayNotify)
		webhook.POST("/wxpay", webhookHandler.WxpayNotify)
		webhook.POST("/stripe", webhookHandler.StripeWebhook)
		webhook.POST("/airwallex", webhookHandler.AirwallexWebhook)
	}

	adminGroup := v1.Group("/admin/payment")
	adminGroup.Use(guards.Admin)
	adminGroup.Use(guards.Panel)
	adminGroup.Use(guards.Audit)
	{
		adminapi.Register(adminGroup, ManagementOperations["payment.dashboard.list"], adminPaymentHandler.GetDashboard)

		adminapi.Register(adminGroup, ManagementOperations["payment.config.list"], adminPaymentHandler.GetConfig)
		adminapi.Register(adminGroup, ManagementOperations["payment.config.update"], adminPaymentHandler.UpdateConfig)

		adminOrders := adminGroup.Group("/orders")
		{
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.list"], adminPaymentHandler.ListOrders)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.get"], adminPaymentHandler.GetOrderDetail)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.invoice.list"], adminPaymentHandler.GetOrderInvoice)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.cancel.create"], adminPaymentHandler.CancelOrder)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.force_expire.create"], adminPaymentHandler.ForceExpireOrder)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.retry.create"], adminPaymentHandler.RetryFulfillment)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.refund.create"], adminPaymentHandler.ProcessRefund)
			adminapi.Register(adminOrders, ManagementOperations["payment.orders.refund.query.create"], adminPaymentHandler.QueryAndFinalizeRefund)
		}

		plans := adminGroup.Group("/plans")
		{
			adminapi.Register(plans, billinghttpapi.ManagementOperations["payment.plans.list"], planHandler.ListPlans)
			adminapi.Register(plans, billinghttpapi.ManagementOperations["payment.plans.create"], planHandler.CreatePlan)
			adminapi.Register(plans, billinghttpapi.ManagementOperations["payment.plans.update"], planHandler.UpdatePlan)
			adminapi.Register(plans, billinghttpapi.ManagementOperations["payment.plans.delete"], planHandler.DeletePlan)
		}

		providers := adminGroup.Group("/providers")
		{
			adminapi.Register(providers, ManagementOperations["payment.providers.list"], adminPaymentHandler.ListProviders)
			adminapi.Register(providers, ManagementOperations["payment.providers.create"], adminPaymentHandler.CreateProvider)
			adminapi.Register(providers, ManagementOperations["payment.providers.test.create"], adminPaymentHandler.TestProvider)
			adminapi.Register(providers, ManagementOperations["payment.providers.update"], adminPaymentHandler.UpdateProvider)
			adminapi.Register(providers, ManagementOperations["payment.providers.delete"], adminPaymentHandler.DeleteProvider)
		}
	}
}

// RegisterWeChatAuthRoutes mounts payment OAuth start/callback on the supplied
// identity group. Supply a usable endpoint; registration inherits existing
// group middleware and adds no guard handlers of its own.
func RegisterWeChatAuthRoutes(auth *gin.RouterGroup, endpoint WeChatAuthEndpoints) {
	auth.GET("/oauth/wechat/payment/start", endpoint.WeChatPaymentOAuthStart)
	auth.GET("/oauth/wechat/payment/callback", endpoint.WeChatPaymentOAuthCallback)
}
