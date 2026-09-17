package httpapi

import "github.com/gin-gonic/gin"

// WeChatAuthEndpoints 只包含支付 OAuth 的两个回环入口。
type WeChatAuthEndpoints interface {
	WeChatPaymentOAuthStart(*gin.Context)
	WeChatPaymentOAuthCallback(*gin.Context)
}

// RegisterWeChatAuthRoutes 在身份注册组上保持原 guard/audit 和回环路径。
func RegisterWeChatAuthRoutes(auth *gin.RouterGroup, endpoint WeChatAuthEndpoints) {
	auth.GET("/oauth/wechat/payment/start", endpoint.WeChatPaymentOAuthStart)
	auth.GET("/oauth/wechat/payment/callback", endpoint.WeChatPaymentOAuthCallback)
}
