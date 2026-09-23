package app

import (
	apikeyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/admission"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *routeTestHandlers,
	jwtAuth identityhttp.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	stepUpAuth identityhttp.StepUpAuthMiddleware,
	settingService *admission.BackendMode,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(identityhttp.BackendModeUserGuard(legacyBackendModeReader(settingService)))
	// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
	authenticated.Use(panelRateLimiter.Global())
	// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
	authenticated.Use(gin.HandlerFunc(auditLog))
	identityhttp.RegisterUserRoutes(authenticated, h.User, h.Totp, h.Passkey)
	promotionhttp.RegisterUserRoutes(authenticated, h.PromotionUser)
	apikeyhttp.RegisterUserRoutes(authenticated, h.APIKey)
	teamhttp.RegisterUserRoutes(authenticated, h.Team, gin.HandlerFunc(stepUpAuth))
	usagehttp.RegisterUserRoutes(authenticated, h.Usage, panelRateLimiter.Heavy())
	creativehttp.RegisterUserRoutes(authenticated, h.Creative, panelRateLimiter.Heavy())
	sitehttp.RegisterUserRoutes(authenticated, h.Announcement)
	billinghttp.RegisterUserRoutes(authenticated, h.Redeem, h.Subscription, h.PlatformQuota)
}
