package app

import (
	time "time"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	notificationhttp "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	paymenthttp "github.com/TokenFlux/TokenRouter/internal/payment/httpapi"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	servermiddleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	gin "github.com/gin-gonic/gin"
)

// provideAuthRouteMount 固定所属 HTTP 实例，只负责注册与跨模块投影。
func provideAuthRouteMount(eModelMarketplace *routinghttp.MarketplaceHandler,
	ePublicSettings *sitehttp.PublicHandler,
	eNotification *notificationhttp.Handler,
	ePasskey *identityhttp.PasskeyHandler,
	eAuth *identityHTTP) authRouteMount {
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
