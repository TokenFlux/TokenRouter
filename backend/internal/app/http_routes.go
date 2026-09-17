package app

import (
	idempotency "github.com/TokenFlux/TokenRouter/internal/idempotency"
	middleware "github.com/TokenFlux/TokenRouter/internal/server/middleware"
	gin "github.com/gin-gonic/gin"
)

// httpRouteSecurity 保持各路由族原有 middleware 顺序，所有实例由组合根提供。
type httpRouteSecurity struct {
	JWT, Admin, Audit, StepUp, BackendAuth, BackendUser gin.HandlerFunc
	Panel                                               *middleware.PanelRateLimiter
	AuthLimiter                                         *middleware.RateLimiter
}

type authRouteMount func(*gin.RouterGroup, httpRouteSecurity)
type userRouteMount func(*gin.RouterGroup, httpRouteSecurity)
type adminRouteMount func(*gin.RouterGroup, httpRouteSecurity, gin.HandlerFunc)
type gatewayRouteMount func(*gin.Engine)
type paymentRouteMount func(*gin.RouterGroup, httpRouteSecurity)

// provideHTTPRouteMount 只组合注册函数，没有全局 handler 聚合或字符串分派。
func provideHTTPRouteMount(auth authRouteMount, user userRouteMount, admin adminRouteMount, gateway gatewayRouteMount, payment paymentRouteMount,
	_ *idempotency.IdempotencyCoordinator, _ *idempotency.IdempotencyCleanupService) httpRouteMount {
	return func(r *gin.Engine, security httpRouteSecurity, catalog gin.HandlerFunc, pages func(*gin.RouterGroup)) {
		v1 := r.Group("/api/v1")
		auth(v1, security)
		user(v1, security)
		admin(v1, security, catalog)
		gateway(r)
		payment(v1, security)
		pages(v1)
	}
}
