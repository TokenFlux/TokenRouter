package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *UsageHandler, heavy gin.HandlerFunc) {
	usage := authenticated.Group("/usage")
	usage.Use(heavy)
	{
		usage.GET("", endpoint.List)
		usage.GET("/ranking", endpoint.Ranking)
		usage.GET("/errors", endpoint.ListErrors)
		usage.GET("/errors/:id", endpoint.GetErrorDetail)
		usage.GET("/stats", endpoint.Stats)
		// 用户仪表盘接口
		usage.GET("/dashboard/stats", endpoint.DashboardStats)
		usage.GET("/dashboard/trend", endpoint.DashboardTrend)
		usage.GET("/dashboard/models", endpoint.DashboardModels)
		usage.GET("/dashboard/snapshot-v2", endpoint.DashboardSnapshotV2)
		usage.POST("/dashboard/api-keys-usage", endpoint.DashboardAPIKeysUsage)
		usage.GET("/:id", endpoint.GetByID)
	}
	authenticated.GET("/user/api-keys/:id/usage/daily", heavy, endpoint.GetMyAPIKeyDailyUsage)
}
