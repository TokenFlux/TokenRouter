package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *UserHandler, totpEndpoint *TotpHandler, passkeyEndpoint *PasskeyHandler) {
	user := authenticated.Group("/user")
	{
		user.GET("/profile", endpoint.GetProfile)
		user.PUT("/password", endpoint.ChangePassword)
		user.PUT("", endpoint.UpdateProfile)
		user.POST("/account-bindings/email/send-code", endpoint.SendEmailBindingCode)
		user.POST("/account-bindings/email", endpoint.BindEmailIdentity)
		user.DELETE("/account-bindings/:provider", endpoint.UnbindIdentity)
		user.POST("/auth-identities/bind/start", endpoint.StartIdentityBinding)

		// 通知邮箱管理
		notifyEmail := user.Group("/notify-email")
		{
			notifyEmail.POST("/send-code", endpoint.SendNotifyEmailCode)
			notifyEmail.POST("/verify", endpoint.VerifyNotifyEmail)
			notifyEmail.PUT("/toggle", endpoint.ToggleNotifyEmail)
			notifyEmail.DELETE("", endpoint.RemoveNotifyEmail)
		}

		// TOTP 双因素认证
		totp := user.Group("/totp")
		{
			totp.GET("/status", totpEndpoint.GetStatus)
			totp.GET("/verification-method", totpEndpoint.GetVerificationMethod)
			totp.POST("/send-code", totpEndpoint.SendVerifyCode)
			totp.POST("/setup", totpEndpoint.InitiateSetup)
			totp.POST("/enable", totpEndpoint.Enable)
			totp.POST("/disable", totpEndpoint.Disable)
			// 敏感操作二次验证：授予当前会话一段时间的 step-up 权限
			totp.POST("/step-up", totpEndpoint.StepUp)
		}

		passkeys := user.Group("/passkeys")
		{
			passkeys.GET("", passkeyEndpoint.List)
			passkeys.POST("/register/begin", passkeyEndpoint.BeginRegistration)
			passkeys.POST("/register/finish", passkeyEndpoint.FinishRegistration)
			passkeys.PATCH("/:id", passkeyEndpoint.Rename)
			passkeys.DELETE("/:id", passkeyEndpoint.Delete)
		}
	}
}
