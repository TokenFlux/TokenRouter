package httpapi

import (
	"time"

	"github.com/gin-gonic/gin"
)

// AuthRouteMiddleware 保留调用方已绑定的认证、审计和故障关闭限流。
type AuthRouteMiddleware struct {
	JWT, Audit, BackendAuth, BackendUser, Panel gin.HandlerFunc
	Limit                                       func(string, int, time.Duration) gin.HandlerFunc
}

// RegisterAuthenticationRoutes 只注册身份流程，支付回环由 app 注入所属模块的注册函数。
func RegisterAuthenticationRoutes(v1 *gin.RouterGroup, endpoints AuthEndpoints, passkeys *PasskeyHandler, guards AuthRouteMiddleware, registerPaymentAuth func(*gin.RouterGroup)) {
	// 公开接口
	auth := v1.Group("/auth")
	auth.Use(guards.BackendAuth)
	// 认证事件（登录/注册/2FA/token 刷新失败）入审计
	auth.Use(guards.Audit)
	{
		// 注册/登录/2FA/验证码发送均属于高风险入口，增加服务端兜底限流（Redis 故障时 fail-close）
		auth.POST("/register", guards.Limit("auth-register", 5, time.Minute), endpoints.Register)
		auth.POST("/login", guards.Limit("auth-login", 20, time.Minute), endpoints.Login)
		auth.POST("/login/2fa", guards.Limit("auth-login-2fa", 20, time.Minute), endpoints.Login2FA)
		auth.POST("/passkey/login/begin", guards.Limit("passkey-login-begin", 20, time.Minute), passkeys.BeginLogin)
		auth.POST("/passkey/login/finish", guards.Limit("passkey-login-finish", 20, time.Minute), passkeys.FinishLogin)
		auth.POST("/send-verify-code", guards.Limit("auth-send-verify-code", 5, time.Minute), endpoints.SendVerifyCode)
		// Token刷新接口添加速率限制：每分钟最多 30 次（Redis 故障时 fail-close）
		auth.POST("/refresh", guards.Limit("refresh-token", 30, time.Minute), endpoints.RefreshToken)
		// 登出接口（公开，允许未认证用户调用以撤销Refresh Token）
		auth.POST("/logout", endpoints.Logout)
		// 优惠码验证接口添加速率限制：每分钟最多 10 次（Redis 故障时 fail-close）
		auth.POST("/validate-promo-code", guards.Limit("validate-promo", 10, time.Minute), endpoints.ValidatePromoCode)
		// 邀请码验证接口添加速率限制：每分钟最多 10 次（Redis 故障时 fail-close）
		auth.POST("/validate-invitation-code", guards.Limit("validate-invitation", 10, time.Minute), endpoints.ValidateInvitationCode)
		// 忘记密码接口添加速率限制：每分钟最多 5 次（Redis 故障时 fail-close）
		auth.POST("/forgot-password", guards.Limit("forgot-password", 5, time.Minute), endpoints.ForgotPassword)
		// 重置密码接口添加速率限制：每分钟最多 10 次（Redis 故障时 fail-close）
		auth.POST("/reset-password", guards.Limit("reset-password", 10, time.Minute), endpoints.ResetPassword)
		auth.GET("/oauth/linuxdo/start", endpoints.LinuxDoOAuthStart)
		auth.POST("/oauth/linuxdo/start", guards.Limit("oauth-linuxdo-start", 20, time.Minute), endpoints.LinuxDoOAuthStart)
		auth.GET("/oauth/linuxdo/bind/start", func(c *gin.Context) {
			query := c.Request.URL.Query()
			query.Set("intent", "bind_current_user")
			c.Request.URL.RawQuery = query.Encode()
			endpoints.LinuxDoOAuthStart(c)
		})
		auth.GET("/oauth/linuxdo/callback", endpoints.LinuxDoOAuthCallback)
		auth.GET("/oauth/wechat/start", endpoints.WeChatOAuthStart)
		auth.POST("/oauth/wechat/start", guards.Limit("oauth-wechat-start", 20, time.Minute), endpoints.WeChatOAuthStart)
		auth.GET("/oauth/wechat/bind/start", func(c *gin.Context) {
			query := c.Request.URL.Query()
			query.Set("intent", "bind_current_user")
			c.Request.URL.RawQuery = query.Encode()
			endpoints.WeChatOAuthStart(c)
		})
		auth.GET("/oauth/wechat/callback", endpoints.WeChatOAuthCallback)
		registerPaymentAuth(auth)
		auth.POST("/oauth/pending/exchange",
			guards.Limit("oauth-pending-exchange", 20, time.Minute),
			endpoints.ExchangePendingOAuthCompletion,
		)
		auth.POST("/oauth/pending/send-verify-code",
			guards.Limit("oauth-pending-send-verify-code", 5, time.Minute),
			endpoints.SendPendingOAuthVerifyCode,
		)
		auth.POST("/oauth/pending/create-account",
			guards.Limit("oauth-pending-create-account", 10, time.Minute),
			endpoints.CreatePendingOAuthAccount,
		)
		auth.POST("/oauth/pending/bind-login",
			guards.Limit("oauth-pending-bind-login", 10, time.Minute),
			endpoints.BindPendingOAuthLogin,
		)
		auth.POST("/oauth/linuxdo/complete-registration",
			guards.Limit("oauth-linuxdo-complete", 10, time.Minute),
			endpoints.CompleteLinuxDoOAuthRegistration,
		)
		auth.POST("/oauth/linuxdo/bind-login",
			guards.Limit("oauth-linuxdo-bind-login", 20, time.Minute),
			endpoints.BindLinuxDoOAuthLogin,
		)
		auth.POST("/oauth/linuxdo/create-account",
			guards.Limit("oauth-linuxdo-create-account", 10, time.Minute),
			endpoints.CreateLinuxDoOAuthAccount,
		)
		auth.POST("/oauth/wechat/complete-registration",
			guards.Limit("oauth-wechat-complete", 10, time.Minute),
			endpoints.CompleteWeChatOAuthRegistration,
		)
		auth.POST("/oauth/wechat/bind-login",
			guards.Limit("oauth-wechat-bind-login", 20, time.Minute),
			endpoints.BindWeChatOAuthLogin,
		)
		auth.POST("/oauth/wechat/create-account",
			guards.Limit("oauth-wechat-create-account", 10, time.Minute),
			endpoints.CreateWeChatOAuthAccount,
		)
		auth.GET("/oauth/oidc/start", endpoints.OIDCOAuthStart)
		auth.POST("/oauth/oidc/start", guards.Limit("oauth-oidc-start", 20, time.Minute), endpoints.OIDCOAuthStart)
		auth.GET("/oauth/oidc/bind/start", func(c *gin.Context) {
			query := c.Request.URL.Query()
			query.Set("intent", "bind_current_user")
			c.Request.URL.RawQuery = query.Encode()
			endpoints.OIDCOAuthStart(c)
		})
		auth.GET("/oauth/oidc/callback", endpoints.OIDCOAuthCallback)
		auth.POST("/oauth/oidc/complete-registration",
			guards.Limit("oauth-oidc-complete", 10, time.Minute),
			endpoints.CompleteOIDCOAuthRegistration,
		)
		auth.GET("/oauth/github/start", endpoints.GitHubOAuthStart)
		auth.POST("/oauth/github/start", guards.Limit("oauth-github-start", 20, time.Minute), endpoints.GitHubOAuthStart)
		auth.GET("/oauth/github/callback", endpoints.GitHubOAuthCallback)
		auth.POST("/oauth/github/complete-registration",
			guards.Limit("oauth-github-complete", 10, time.Minute),
			endpoints.CompleteGitHubOAuthRegistration,
		)
		auth.GET("/oauth/google/start", endpoints.GoogleOAuthStart)
		auth.POST("/oauth/google/start", guards.Limit("oauth-google-start", 20, time.Minute), endpoints.GoogleOAuthStart)
		auth.GET("/oauth/google/callback", endpoints.GoogleOAuthCallback)
		auth.POST("/oauth/google/one-tap", guards.Limit("oauth-google-one-tap", 20, time.Minute), endpoints.GoogleOneTap)
		auth.POST("/oauth/google/complete-registration",
			guards.Limit("oauth-google-complete", 10, time.Minute),
			endpoints.CompleteGoogleOAuthRegistration,
		)
		auth.POST("/oauth/oidc/bind-login",
			guards.Limit("oauth-oidc-bind-login", 20, time.Minute),
			endpoints.BindOIDCOAuthLogin,
		)
		auth.POST("/oauth/oidc/create-account",
			guards.Limit("oauth-oidc-create-account", 10, time.Minute),
			endpoints.CreateOIDCOAuthAccount,
		)
		auth.GET("/oauth/dingtalk/start", endpoints.DingTalkOAuthStart)
		auth.POST("/oauth/dingtalk/start", guards.Limit("oauth-dingtalk-start", 20, time.Minute), endpoints.DingTalkOAuthStart)
		auth.GET("/oauth/dingtalk/bind/start", func(c *gin.Context) {
			query := c.Request.URL.Query()
			query.Set("intent", "bind_current_user")
			c.Request.URL.RawQuery = query.Encode()
			endpoints.DingTalkOAuthStart(c)
		})
		auth.GET("/oauth/dingtalk/callback", endpoints.DingTalkOAuthCallback)
		auth.POST("/oauth/dingtalk/complete-registration",
			guards.Limit("oauth-dingtalk-complete", 10, time.Minute),
			endpoints.CompleteDingTalkOAuthRegistration,
		)
		auth.POST("/oauth/dingtalk/bind-login",
			guards.Limit("oauth-dingtalk-bind-login", 20, time.Minute),
			endpoints.BindDingTalkOAuthLogin,
		)
		auth.POST("/oauth/dingtalk/create-account",
			guards.Limit("oauth-dingtalk-create-account", 10, time.Minute),
			endpoints.CreateDingTalkOAuthAccount,
		)
	}

}

// RegisterSessionRoutes 保留当前用户、会话撤销及绑定 Cookie 的原鉴权次序。
func RegisterSessionRoutes(v1 *gin.RouterGroup, endpoints AuthEndpoints, guards AuthRouteMiddleware) {
	authenticated := v1.Group("")
	authenticated.Use(guards.JWT)
	authenticated.Use(guards.BackendUser)
	authenticated.Use(guards.Panel)
	authenticated.GET("/auth/me", endpoints.GetCurrentUser)
	authenticated.POST("/auth/revoke-all-sessions", endpoints.RevokeAllSessions)
	authenticated.POST("/auth/oauth/bind-token", endpoints.PrepareOAuthBindAccessTokenCookie)
}
