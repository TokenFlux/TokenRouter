package app

import (
	apikeyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	billinghttp "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	creativehttp "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	promotionhttp "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	teamhttp "github.com/TokenFlux/TokenRouter/internal/team/httpapi"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	gin "github.com/gin-gonic/gin"
)

// provideUserRouteMount 固定所属 HTTP 实例，只负责注册与跨模块投影。
func provideUserRouteMount(ePlatformQuota *billinghttp.QuotaHandler,
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
	eUser *identityhttp.UserHandler) userRouteMount {
	return func(v1 *gin.RouterGroup, security httpRouteSecurity) {
		authenticated := v1.Group("")
		authenticated.Use(security.JWT)
		authenticated.Use(security.BackendUser)
		// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
		authenticated.Use(security.Panel.Global())
		// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
		authenticated.Use(security.Audit)
		identityhttp.RegisterUserRoutes(authenticated, eUser, eTotp, ePasskey)
		promotionhttp.RegisterUserRoutes(authenticated, eUserPromotion)
		apikeyhttp.RegisterUserRoutes(authenticated, eAPIKey)
		teamhttp.RegisterUserRoutes(authenticated, eTeam, security.StepUp)
		usagehttp.RegisterUserRoutes(authenticated, eUsage, security.Panel.Heavy())
		creativehttp.RegisterUserRoutes(authenticated, eCreative, security.Panel.Heavy())
		sitehttp.RegisterUserRoutes(authenticated, eAnnouncement)
		billinghttp.RegisterUserRoutes(authenticated, eRedeem, eSubscription, ePlatformQuota)

	}
}
