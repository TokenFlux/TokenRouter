package app

import (
	"time"

	"github.com/gin-gonic/gin"

	apikeyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	batchhttp "github.com/TokenFlux/TokenRouter/internal/batchimage/httpapi"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	wshttp "github.com/TokenFlux/TokenRouter/internal/gateway/ws/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	notificationhttp "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
)

// http_routes.go mounts panel and gateway route families with their shared
// security middleware. Panel routes share authentication, backend-mode policy,
// limits, and audit instances assembled once by the application. Gateway
// registration uses the engine directly for protocol paths, while
// authentication, user, administrator, plugin, payment, and page registrars receive the
// /api/v1 group.

// httpRouteSecurity carries the assembled guards and limiters used by route
// families.
type httpRouteSecurity struct {
	// JWT, Admin, Audit, StepUp, BackendAuth, and BackendUser are supplied
	// once by router assembly. Each route family chooses the guards and their execution order.
	// JWT: panel bearer authentication.
	// Admin: administrator access authentication.
	// Audit: panel activity recording.
	// StepUp: sensitive action verification.
	// BackendAuth: authentication backend policy.
	// BackendUser: user backend policy.
	JWT, Admin, Audit, StepUp, BackendAuth, BackendUser gin.HandlerFunc
	Panel                                               *servermiddleware.PanelRateLimiter // panel request limits
	AuthLimiter                                         *servermiddleware.RateLimiter      // public authentication limits
}

type (
	// authRouteMount registers authentication and public panel routes with shared guards.
	authRouteMount func(*gin.RouterGroup, httpRouteSecurity)
	// userRouteMount registers authenticated user resources with shared security middleware.
	userRouteMount func(*gin.RouterGroup, httpRouteSecurity)
	// adminRouteMount registers administrator resources and the supplied protocol catalogue.
	adminRouteMount func(*gin.RouterGroup, httpRouteSecurity, gin.HandlerFunc)
	// gatewayRouteMount registers protocol endpoints directly on the Gin engine.
	gatewayRouteMount func(*gin.Engine)
	// pluginRouteMount registers plugin contributions with shared panel security middleware.
	pluginRouteMount func(*gin.RouterGroup, httpRouteSecurity)
	// paymentRouteMount registers payment routes under the supplied panel group.
	paymentRouteMount func(*gin.RouterGroup, httpRouteSecurity)
)

// provideHTTPRouteMount returns the ordered application route registration
// callback. It mounts panel families under /api/v1, passes the engine to
// gateway registration, and retains idempotency dependencies as Wire
// construction requirements.
func provideHTTPRouteMount(auth authRouteMount, user userRouteMount, admin adminRouteMount, gateway gatewayRouteMount, payment paymentRouteMount, plugins pluginRouteMount,
	_ *idempotency.IdempotencyCoordinator, _ *idempotency.IdempotencyCleanupService,
) httpRouteMount {
	return func(r *gin.Engine, security httpRouteSecurity, catalog gin.HandlerFunc, pages func(*gin.RouterGroup)) {
		v1 := r.Group("/api/v1")
		auth(v1, security)
		user(v1, security)
		admin(v1, security, catalog)
		plugins(v1, security)
		gateway(r)
		payment(v1, security)
		pages(v1)
	}
}

// provideAuthRouteMount binds authentication, public settings, unsubscribe,
// marketplace, and session routes. Its authentication limiter uses fail-close
// dependency handling, while public settings use the panel public-IP limiter
// supplied through security.
func provideAuthRouteMount(eModelMarketplace *routinghttp.MarketplaceHandler,
	ePublicSettings *sitehttp.PublicHandler,
	eNotification *notificationhttp.Handler,
	ePasskey *identityhttp.PasskeyHandler,
	eAuth *identityHTTP,
) authRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		guards := identityhttp.AuthRouteMiddleware{JWT: security.JWT, Audit: security.Audit, BackendAuth: security.BackendAuth, BackendUser: security.BackendUser, Panel: security.Panel.Global(), Limit: func(key string, n int, window time.Duration) gin.HandlerFunc {
			return security.AuthLimiter.LimitWithOptions(key, n, window, servermiddleware.RateLimitOptions{FailureMode: servermiddleware.RateLimitFailClose})
		}}
		identityhttp.RegisterAuthenticationRoutes(v1, eAuth, ePasskey, guards, func(group *gin.RouterGroup) { paymenthttp.RegisterWeChatAuthRoutes(group, eAuth) })
		public := v1.Group("/settings")
		public.Use(security.Panel.PublicIP())
		sitehttp.RegisterPublicSettingsRoutes(public, ePublicSettings)
		notificationhttp.RegisterUnsubscribeRoute(public, eNotification)
		routinghttp.RegisterPublicMarketplaceRoutes(v1, eModelMarketplace)
		identityhttp.RegisterSessionRoutes(v1, eAuth, guards)
	}
}

// provideGatewayRouteMount returns gateway registration with the assembled
// protocol handlers and middleware. Responses WS, usage, and Qoder Chat method
// callbacks are captured, and batch-image routes are added through the gateway
// registrar callback.
func provideGatewayRouteMount(eCountTokensHTTP *gatewayhttp.CountTokensHandler,
	eQoderCompatibleHTTP *gatewayhttp.QoderCompatibleHandler,
	eCompatibleTextHTTP *gatewayhttp.CompatibleTextHandler,
	eGeminiNativeHTTP *gatewayhttp.GeminiNativeHandler,
	eOpenAITextHTTP *gatewayhttp.OpenAITextHandler,
	eOpenAITokensHTTP *gatewayhttp.OpenAITokensHandler,
	eResponsesWSHTTP *wshttp.ResponsesWSHandler,
	eModelsHTTP *gatewayhttp.ModelsHandler,
	eMessagesHTTP *gatewayhttp.MessagesHandler,
	eMediaHTTP *gatewayhttp.MediaHandler,
	eAuxiliaryHTTP *gatewayhttp.AuxiliaryHandler,
	eLiveHTTP *gatewayhttp.LiveHandler,
	eSearchHTTP *gatewayhttp.SearchHandler,
	ePublicUsage *usagehttp.PublicUsageHandler,
	eQoderChat *gatewayhttp.QoderChatHandler,
	batch *batchhttp.BatchImageHandler,
	options gatewayhttp.RouteMiddleware,
) gatewayRouteMount {
	return func(r *gin.Engine) {
		gatewayhttp.RegisterGatewayRoutes(r, gatewayhttp.RouteEndpoints{CountTokens: eCountTokensHTTP, QoderCompatible: eQoderCompatibleHTTP, CompatibleText: eCompatibleTextHTTP, GeminiNative: eGeminiNativeHTTP, OpenAIText: eOpenAITextHTTP, OpenAITokens: eOpenAITokensHTTP, ResponsesWS: eResponsesWSHTTP.ResponsesWebSocket, Models: eModelsHTTP, Messages: eMessagesHTTP, Media: eMediaHTTP, Auxiliary: eAuxiliaryHTTP, Live: eLiveHTTP, Search: eSearchHTTP, PublicUsage: ePublicUsage.Usage, QoderChat: eQoderChat.ChatCompletions}, options, func(group *gin.RouterGroup) { batchhttp.RegisterGatewayRoutes(group, batch) })
	}
}

// providePaymentRouteMount binds payment handlers with user, administrator,
// backend-mode, panel, and audit guards. The returned callback passes those
// shared instances to paymenthttp.RegisterRoutes, which applies them to its
// route families.
func providePaymentRouteMount(user *paymenthttp.PaymentHandler, webhook *paymenthttp.PaymentWebhookHandler, admin *paymenthttp.AdminHandler, plans *billinghttp.PlanHandler) paymentRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		paymenthttp.RegisterRoutes(v1, user, webhook, admin, plans, paymenthttp.RouteMiddleware{JWT: security.JWT, BackendMode: security.BackendUser, Panel: security.Panel.Global(), Admin: security.Admin, Audit: security.Audit})
	}
}

// provideUserRouteMount mounts user resources after JWT, backend-mode, panel,
// and audit middleware. Team routes receive step-up protection, and usage plus
// creative registrars receive the heavy-query limiter in addition to those
// group guards.
func provideUserRouteMount(
	eUserPromotion *promotionhttp.UserHandler,
	eSubscription *billinghttp.SubscriptionHandler,
	eAnnouncement *sitehttp.AnnouncementHandler,
	eCreative *creativehttp.CreativeHandler,
	ePasskey *identityhttp.PasskeyHandler,
	eAPIKey *apikeyhttp.APIKeyHandler[dto.Group],
	eRedeem *billinghttp.RedeemHandler,
	eUsage *usagehttp.UsageHandler,
	eTeam *teamhttp.UserHandler,
	eTotp *identityhttp.TotpHandler,
	eUser *identityhttp.UserHandler,
) userRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		authenticated := v1.Group("")
		authenticated.Use(security.JWT)
		authenticated.Use(security.BackendUser)
		// Per-user panel limits bound database load from frequent requests.
		authenticated.Use(security.Panel.Global())
		// Audit user mutations, including TOTP changes, step-up verification, and password changes.
		authenticated.Use(security.Audit)
		identityhttp.RegisterUserRoutes(authenticated, eUser, eTotp, ePasskey)
		promotionhttp.RegisterUserRoutes(authenticated, eUserPromotion)
		apikeyhttp.RegisterUserRoutes(authenticated, eAPIKey)
		teamhttp.RegisterUserRoutes(authenticated, eTeam, security.StepUp)
		usagehttp.RegisterUserRoutes(authenticated, eUsage, security.Panel.Heavy())
		creativehttp.RegisterUserRoutes(authenticated, eCreative, security.Panel.Heavy())
		sitehttp.RegisterUserRoutes(authenticated, eAnnouncement)
		billinghttp.RegisterUserRoutes(authenticated, eRedeem, eSubscription)
	}
}
