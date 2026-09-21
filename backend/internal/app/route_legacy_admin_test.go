// Package routes provides HTTP route registration and handlers.
package app

import (
	routeaccount "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	routeapikey "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	routeaudit "github.com/TokenFlux/TokenRouter/internal/audit/httpapi"
	routebackup "github.com/TokenFlux/TokenRouter/internal/backup/httpapi"
	routebilling "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	routecreative "github.com/TokenFlux/TokenRouter/internal/creative/httpapi"
	routeegress "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	routegateway "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	routeidentity "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	routemoderation "github.com/TokenFlux/TokenRouter/internal/moderation/httpapi"
	routenotification "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	routeops "github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	routepromotion "github.com/TokenFlux/TokenRouter/internal/promotion/httpapi"
	routerouting "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routescheduler "github.com/TokenFlux/TokenRouter/internal/scheduler/httpapi"
	routesearch "github.com/TokenFlux/TokenRouter/internal/search/httpapi"
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"
	routesettings "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
	routesite "github.com/TokenFlux/TokenRouter/internal/site/httpapi"
	routeusageadmin "github.com/TokenFlux/TokenRouter/internal/usage/httpapi/admin"

	"github.com/TokenFlux/TokenRouter/internal/server/middleware"

	"github.com/gin-gonic/gin"
)

// RegisterAdminRoutes 注册管理员路由
func RegisterAdminRoutes(
	v1 *gin.RouterGroup,
	h *routeTestHandlers,
	adminAuth middleware.AdminAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	stepUpAuth middleware.StepUpAuthMiddleware,
	panelRateLimiter *middleware.PanelRateLimiter,
	protocolCatalog gin.HandlerFunc,
) {
	admin := v1.Group("/admin")
	admin.Use(gin.HandlerFunc(adminAuth))
	// 面板全局按用户限流（默认管理员豁免，可在系统设置中关闭豁免）
	admin.Use(panelRateLimiter.Global())
	// 审计中间件挂在认证之后：所有管理面变更类操作 + 敏感读取入审计日志
	admin.Use(gin.HandlerFunc(auditLog))
	{
		// 只读能力目录：账号与分组表单共用后端定义。
		admin.GET("/protocol-capabilities", protocolCatalog)
		// 仪表盘
		registerDashboardRoutes(admin, h)

		// 用户管理
		registerUserManagementRoutes(admin, h)

		// 分组管理
		registerGroupRoutes(admin, h)

		// 账号管理
		registerAccountRoutes(admin, h, stepUpAuth)

		// 公告管理
		registerAnnouncementRoutes(admin, h)

		// OpenAI OAuth 管理
		registerOpenAIOAuthRoutes(admin, h)

		// Gemini OAuth 管理
		registerGeminiOAuthRoutes(admin, h)

		// Antigravity OAuth 管理
		registerAntigravityOAuthRoutes(admin, h)

		// Qoder OAuth 管理
		registerQoderOAuthRoutes(admin, h)

		// Grok OAuth 管理
		registerGrokOAuthRoutes(admin, h)

		// 代理管理
		registerProxyRoutes(admin, h, stepUpAuth)

		// 卡密管理
		registerRedeemCodeRoutes(admin, h)

		// 优惠码管理
		registerPromoCodeRoutes(admin, h)

		// 系统设置
		registerSettingsRoutes(admin, h)

		// 数据管理
		registerDataManagementRoutes(admin, h, stepUpAuth)

		// 数据库备份恢复
		registerBackupRoutes(admin, h, stepUpAuth)

		// 运维监控（Ops）
		registerOpsRoutes(admin, h)

		// 系统管理
		registerSystemRoutes(admin, h)

		// 订阅管理
		registerSubscriptionRoutes(admin, h)

		// 使用记录管理
		registerUsageRoutes(admin, h)

		// 用户属性管理
		registerUserAttributeRoutes(admin, h)

		// 错误透传规则管理
		registerErrorPassthroughRoutes(admin, h)

		// TLS 指纹模板管理
		registerTLSFingerprintProfileRoutes(admin, h)

		// TLS 路由器管理
		registerTLSFingerprintRouterRoutes(admin, h)

		// API Key 管理
		registerAdminAPIKeyRoutes(admin, h)

		// 定时测试计划
		registerScheduledTestRoutes(admin, h)

		// 渠道管理
		registerChannelRoutes(admin, h)

		// 风控中心
		registerContentModerationRoutes(admin, h)

		// 邀请返利
		registerAffiliateRoutes(admin, h)

		// 操作审计日志
		registerAuditLogRoutes(admin, h, stepUpAuth)

		// 团队运维管理。
		teams := admin.Group("/teams")
		{
			teams.GET("", h.Admin.Team.List)
			teams.POST("", h.Admin.Team.Create)
			teams.GET("/:id", h.Admin.Team.Get)
			teams.GET("/:id/members", h.Admin.Team.ListMembers)
			teams.GET("/:id/usage", h.Admin.Team.GetUsage)
			teams.PATCH("/:id", h.Admin.Team.Update)
			teams.POST("/:id/force-transfer", gin.HandlerFunc(stepUpAuth), h.Admin.Team.ForceTransfer)
			teams.DELETE("/:id", gin.HandlerFunc(stepUpAuth), h.Admin.Team.Dissolve)
		}
	}
}

func registerAuditLogRoutes(admin *gin.RouterGroup, h *routeTestHandlers, _ middleware.StepUpAuthMiddleware) {
	routeaudit.RegisterAuditLogRoutes(admin, h.Admin.AuditLog)
}

// registerAffiliateRoutes 注册上游邀请返利管理接口。
func registerAffiliateRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterAffiliateRoutes(admin, h.Admin.Affiliate)
}

// registerContentModerationRoutes 注册内容审计和风控审核接口。
func registerContentModerationRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routemoderation.RegisterContentModerationRoutes(admin, h.Admin.ContentModeration)
}

func registerAdminAPIKeyRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeapikey.RegisterAdminAPIKeyRoutes(admin, h.Admin.APIKey)
}

func registerOpsRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterOpsRoutes(admin, h.Admin.Ops)
}

func registerDashboardRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterDashboardRoutes(admin, h.Admin.Dashboard)
}

func registerUserManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserManagementRoutes(admin, h.Admin.User, h.Admin.UserAttribute)
	routebilling.RegisterUserQuotaRoutes(admin, h.PlatformQuota)
}

func registerGroupRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterGroupRoutes(admin, h.Admin.Group)
}

func registerAccountRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	routeaccount.RegisterAccountRoutes(admin, routeaccount.AccountRouteEndpoints{
		AccountArchive:     h.Admin.AccountArchive,
		AccountCRS:         h.Admin.AccountCRS,
		AccountCodexImport: h.Admin.AccountCodexImport,
		AccountManagement:  h.Admin.AccountManagement,
		AccountOAuthUsage:  h.Admin.AccountOAuthUsage,
		AccountOllama:      h.Admin.AccountOllama,
		AccountTests:       h.Admin.AccountTests,
		CodexInviteReset:   h.Admin.CodexInviteReset,
		OAuth:              h.Admin.OAuth,
		OpenAIOAuth:        h.Admin.OpenAIOAuth,
		UpstreamUsage:      h.Admin.UpstreamUsage,
	}, gin.HandlerFunc(stepUpAuth), func(accounts *gin.RouterGroup) {
		routescheduler.RegisterAccountDiagnostics(accounts, h.Admin.SchedulerDiagnostics)
	})
}

func registerAnnouncementRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routesite.RegisterAnnouncementRoutes(admin, h.Admin.Announcement)
}

func registerOpenAIOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterOpenAIOAuthRoutes(admin, h.Admin.OpenAIOAuth)
}

func registerGeminiOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterGeminiOAuthRoutes(admin, h.Admin.GeminiOAuth)
}

func registerAntigravityOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterAntigravityOAuthRoutes(admin, h.Admin.AntigravityOAuth)
}

func registerQoderOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterQoderOAuthRoutes(admin, h.Admin.QoderOAuth)
}

func registerGrokOAuthRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterGrokOAuthRoutes(admin, h.Admin.GrokOAuth)
}

func registerProxyRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	routeegress.RegisterProxyRoutes(admin, h.Admin.Proxy, gin.HandlerFunc(stepUpAuth))
}

func registerRedeemCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterRedeemCodeRoutes(admin, h.Admin.Redeem)
}

func registerPromoCodeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routepromotion.RegisterPromoCodeRoutes(admin, h.Admin.Promo)
}

func registerDataManagementRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	routebackup.RegisterDataManagementRoutes(admin, h.Admin.DataManagement, gin.HandlerFunc(stepUpAuth))
}

func registerBackupRoutes(admin *gin.RouterGroup, h *routeTestHandlers, stepUpAuth middleware.StepUpAuthMiddleware) {
	routebackup.RegisterBackupRoutes(admin, h.Admin.Backup, gin.HandlerFunc(stepUpAuth))
}

// requireCanonicalBackupID 防止固定管理端路径被备份详情通配路由接管。

func registerSystemRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeops.RegisterSystemRoutes(admin, h.Admin.System)
}

func registerSubscriptionRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routebilling.RegisterSubscriptionRoutes(admin, h.Admin.Subscription)
}

func registerUsageRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeusageadmin.RegisterUsageRoutes(admin, h.Admin.Usage)
}

func registerUserAttributeRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeidentity.RegisterUserAttributeRoutes(admin, h.Admin.UserAttribute)
}

func registerScheduledTestRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeaccount.RegisterScheduledTestRoutes(admin, h.Admin.ScheduledTest)
}

func registerErrorPassthroughRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routegateway.RegisterErrorPassthroughRoutes(admin, h.Admin.ErrorPassthrough)
}

func registerTLSFingerprintProfileRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintProfileRoutes(admin, h.Admin.TLSFingerprintProfile)
}

func registerTLSFingerprintRouterRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routeegress.RegisterTLSFingerprintRouterRoutes(admin, h.Admin.TLSFingerprintRouter)
}

func registerChannelRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	routerouting.RegisterChannelRoutes(admin, h.Admin.Channel)
}

// registerSettingsRoutes 使用原生端点登记路由形状；此夹具不执行设置读写。
func registerSettingsRoutes(admin *gin.RouterGroup, h *routeTestHandlers) {
	group := admin.Group("/settings")
	routesettings.RegisterSettingsSettingsRoutes(group, &routesettings.Handler{}, &routesettings.PreAggregationHandler{})
	routecreative.RegisterCreativeSettingsRoutes(group, &routecreative.SettingsHandler{})
	routeidentity.RegisterIdentitySettingsRoutes(group, &routeidentity.AdminKeySettingsHandler{})
	routeaccount.RegisterAccountSettingsRoutes(group, &routeaccount.RuntimeSettingsHandler{})
	serverhttp.RegisterPanelSettingsRoutes(group, &serverhttp.PanelSettingsHandler{})
	routegateway.RegisterGatewaySettingsRoutes(group, &routegateway.RuntimeSettingsHandler{})
	routenotification.RegisterSettingsRoutes(group, h.Notification)
	routesearch.RegisterSettingsRoutes(group, h.Search)
}
