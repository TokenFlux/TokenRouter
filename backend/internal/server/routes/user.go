package routes

import (
	"github.com/BrandonVee/TokenRouter/internal/handler"
	"github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// RegisterUserRoutes 注册用户相关路由（需要认证）
func RegisterUserRoutes(
	v1 *gin.RouterGroup,
	h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware,
	auditLog middleware.AuditLogMiddleware,
	stepUpAuth middleware.StepUpAuthMiddleware,
	settingService *service.SettingService,
	panelRateLimiter *middleware.PanelRateLimiter,
) {
	// 数据共享下载链接只依赖短期签名票据，便于浏览器原生下载超大文件。
	v1.GET("/data-sharing/export/download", h.DataSharing.DownloadExport)

	authenticated := v1.Group("")
	authenticated.Use(gin.HandlerFunc(jwtAuth))
	authenticated.Use(middleware.BackendModeUserGuard(settingService))
	// 面板全局按用户限流：防止单个账号高频刷接口打爆数据库
	authenticated.Use(panelRateLimiter.Global())
	// 用户管理面变更类操作入审计（含 TOTP 启用/禁用、step-up 验证、密码修改等安全事件）
	authenticated.Use(gin.HandlerFunc(auditLog))
	{
		// 用户接口
		user := authenticated.Group("/user")
		{
			user.GET("/profile", h.User.GetProfile)
			user.GET("/aff", h.User.GetAffiliate)
			user.POST("/aff/transfer", h.User.TransferAffiliateQuota)
			user.PUT("/password", h.User.ChangePassword)
			user.PUT("", h.User.UpdateProfile)
			user.POST("/account-bindings/email/send-code", h.User.SendEmailBindingCode)
			user.POST("/account-bindings/email", h.User.BindEmailIdentity)
			user.DELETE("/account-bindings/:provider", h.User.UnbindIdentity)
			user.POST("/auth-identities/bind/start", h.User.StartIdentityBinding)
			user.GET("/api-keys/:id/usage/daily", panelRateLimiter.Heavy(), h.Usage.GetMyAPIKeyDailyUsage)
			user.GET("/platform-quotas", h.User.GetMyPlatformQuotas)

			// 通知邮箱管理
			notifyEmail := user.Group("/notify-email")
			{
				notifyEmail.POST("/send-code", h.User.SendNotifyEmailCode)
				notifyEmail.POST("/verify", h.User.VerifyNotifyEmail)
				notifyEmail.PUT("/toggle", h.User.ToggleNotifyEmail)
				notifyEmail.DELETE("", h.User.RemoveNotifyEmail)
			}

			// TOTP 双因素认证
			totp := user.Group("/totp")
			{
				totp.GET("/status", h.Totp.GetStatus)
				totp.GET("/verification-method", h.Totp.GetVerificationMethod)
				totp.POST("/send-code", h.Totp.SendVerifyCode)
				totp.POST("/setup", h.Totp.InitiateSetup)
				totp.POST("/enable", h.Totp.Enable)
				totp.POST("/disable", h.Totp.Disable)
				// 敏感操作二次验证：授予当前会话一段时间的 step-up 权限
				totp.POST("/step-up", h.Totp.StepUp)
			}

			passkeys := user.Group("/passkeys")
			{
				passkeys.GET("", h.Passkey.List)
				passkeys.POST("/register/begin", h.Passkey.BeginRegistration)
				passkeys.POST("/register/finish", h.Passkey.FinishRegistration)
				passkeys.PATCH("/:id", h.Passkey.Rename)
				passkeys.DELETE("/:id", h.Passkey.Delete)
			}
		}

		// API Key管理
		keys := authenticated.Group("/keys")
		{
			keys.GET("", h.APIKey.List)
			keys.GET("/billing-options", h.APIKey.GetBillingOptions)
			keys.GET("/:id", h.APIKey.GetByID)
			keys.POST("", h.APIKey.Create)
			keys.PUT("/:id", h.APIKey.Update)
			keys.DELETE("/:id", h.APIKey.Delete)
		}

		// 团队管理：所有变更均保留成员身份审计，敏感生命周期操作额外执行 step-up。
		team := authenticated.Group("/team")
		{
			team.GET("", h.Team.GetCurrent)
			team.POST("", h.Team.Create)
			team.PATCH("", h.Team.Update)
			team.PATCH("/default-member-limits", h.Team.UpdateDefaultMemberLimits)
			team.POST("/status", gin.HandlerFunc(stepUpAuth), h.Team.SetStatus)
			team.DELETE("", gin.HandlerFunc(stepUpAuth), h.Team.Dissolve)
			team.GET("/members", h.Team.ListMembers)
			team.GET("/usage", h.Team.GetUsageSummary)
			team.GET("/usage/members", h.Team.ListMemberUsageSeries)
			team.GET("/usage/logs", h.Team.ListUsageLogs)
			team.GET("/keys", h.Team.ListTeamKeys)
			team.POST("/keys/:id/disable", h.Team.DisableTeamKey)
			team.POST("/keys/:id/enable", h.Team.EnableTeamKey)
			team.DELETE("/keys/:id", h.Team.DeleteTeamKey)
			team.DELETE("/members/:user_id", h.Team.RemoveMember)
			team.PATCH("/members/:user_id/limits", h.Team.UpdateMemberLimits)
			team.POST("/members/:user_id/usage/reset", h.Team.ResetMemberUsage)
			team.POST("/leave", h.Team.Leave)
			team.GET("/invitations", h.Team.ListInvitations)
			team.POST("/invitations", h.Team.Invite)
			team.POST("/invitations/preview", h.Team.PreviewInvitation)
			team.POST("/invitations/resolve", h.Team.ResolveInvitation)
			team.POST("/invitations/:id/reissue", h.Team.ReissueInvitation)
			team.DELETE("/invitations/:id", h.Team.RevokeInvitation)
			team.POST("/ownership-transfer", gin.HandlerFunc(stepUpAuth), h.Team.StartOwnershipTransfer)
			team.POST("/ownership-transfer/resolve", gin.HandlerFunc(stepUpAuth), h.Team.ResolveOwnershipTransfer)
			team.DELETE("/ownership-transfer", gin.HandlerFunc(stepUpAuth), h.Team.CancelOwnershipTransfer)
		}

		// 用户可用分组（非管理员接口）
		groups := authenticated.Group("/groups")
		{
			groups.GET("/available", h.APIKey.GetAvailableGroups)
			groups.GET("/rates", h.APIKey.GetUserGroupRates)
		}

		// 数据共享：用户查看和签发自己被采集的 Agent session 下载票据。
		dataSharing := authenticated.Group("/data-sharing")
		{
			dataSharing.GET("/notice", h.DataSharing.GetNotice)
			dataSharing.POST("/confirm", h.DataSharing.ConfirmNotice)
			dataSharing.GET("/filter-options", h.DataSharing.FilterOptions)
			dataSharing.GET("/sessions", h.DataSharing.ListSessions)
			dataSharing.GET("/sessions/:id", h.DataSharing.GetSession)
			dataSharing.POST("/sessions/:id/export-ticket", h.DataSharing.CreateSessionExportTicket)
			dataSharing.POST("/export-ticket", h.DataSharing.CreateExportTicket)
		}

		// 使用记录（聚合统计属重查询，叠加更严格的按用户限流）
		usage := authenticated.Group("/usage")
		usage.Use(panelRateLimiter.Heavy())
		{
			usage.GET("", h.Usage.List)
			usage.GET("/ranking", h.Usage.Ranking)
			usage.GET("/errors", h.Usage.ListErrors)
			usage.GET("/errors/:id", h.Usage.GetErrorDetail)
			usage.GET("/stats", h.Usage.Stats)
			// 用户仪表盘接口
			usage.GET("/dashboard/stats", h.Usage.DashboardStats)
			usage.GET("/dashboard/trend", h.Usage.DashboardTrend)
			usage.GET("/dashboard/models", h.Usage.DashboardModels)
			usage.GET("/dashboard/snapshot-v2", h.Usage.DashboardSnapshotV2)
			usage.POST("/dashboard/api-keys-usage", h.Usage.DashboardAPIKeysUsage)
			usage.GET("/:id", h.Usage.GetByID)
		}

		// 公告（用户可见）
		announcements := authenticated.Group("/announcements")
		{
			announcements.GET("", h.Announcement.List)
			announcements.POST("/:id/read", h.Announcement.MarkRead)
		}

		// 卡密兑换
		redeem := authenticated.Group("/redeem")
		{
			redeem.POST("", h.Redeem.Redeem)
			redeem.GET("/history", h.Redeem.GetHistory)
		}

		// 用户订阅
		subscriptions := authenticated.Group("/subscriptions")
		{
			subscriptions.GET("", h.Subscription.List)
			subscriptions.GET("/active", h.Subscription.GetActive)
			subscriptions.GET("/progress", h.Subscription.GetProgress)
			subscriptions.GET("/summary", h.Subscription.GetSummary)
		}
	}
}
