package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, redeemEndpoint *RedeemHandler, subscriptionEndpoint *SubscriptionHandler, quota *QuotaHandler) {
	redeem := authenticated.Group("/redeem")
	{
		redeem.POST("", redeemEndpoint.Redeem)
		redeem.GET("/history", redeemEndpoint.GetHistory)
	}

	// 用户订阅
	subscriptions := authenticated.Group("/subscriptions")
	{
		subscriptions.GET("", subscriptionEndpoint.List)
		subscriptions.GET("/active", subscriptionEndpoint.GetActive)
		subscriptions.GET("/progress", subscriptionEndpoint.GetProgress)
		subscriptions.GET("/summary", subscriptionEndpoint.GetSummary)
		subscriptions.POST("/:id/revoke", subscriptionEndpoint.Revoke)
	}
	authenticated.GET("/user/platform-quotas", quota.GetMyPlatformQuotas)
}
