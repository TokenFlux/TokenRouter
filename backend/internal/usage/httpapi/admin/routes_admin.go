package admin

import (
	"github.com/gin-gonic/gin"
)

// RegisterDashboardRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterDashboardRoutes(admin *gin.RouterGroup, endpoint *DashboardHandler) {
	dashboard := admin.Group("/dashboard")
	{
		dashboard.GET("/snapshot-v2", endpoint.GetSnapshotV2)
		dashboard.GET("/stats", endpoint.GetStats)
		dashboard.GET("/realtime", endpoint.GetRealtimeMetrics)
		dashboard.GET("/trend", endpoint.GetUsageTrend)
		dashboard.GET("/models", endpoint.GetModelStats)
		dashboard.GET("/groups", endpoint.GetGroupStats)
		dashboard.GET("/api-keys-trend", endpoint.GetAPIKeyUsageTrend)
		dashboard.GET("/users-trend", endpoint.GetUserUsageTrend)
		dashboard.GET("/users-ranking", endpoint.GetUserSpendingRanking)
		dashboard.POST("/users-usage", endpoint.GetBatchUsersUsage)
		dashboard.POST("/api-keys-usage", endpoint.GetBatchAPIKeysUsage)
		dashboard.GET("/user-breakdown", endpoint.GetUserBreakdown)
	}
}

// RegisterUsageRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterUsageRoutes(admin *gin.RouterGroup, endpoint *UsageHandler) {
	usage := admin.Group("/usage")
	{
		usage.GET("", endpoint.List)
		usage.GET("/stats", endpoint.Stats)
		usage.GET("/search-users", endpoint.SearchUsers)
		usage.GET("/search-api-keys", endpoint.SearchAPIKeys)
		usage.GET("/cleanup-tasks", endpoint.ListCleanupTasks)
		usage.POST("/cleanup-tasks", endpoint.CreateCleanupTask)
		usage.POST("/cleanup-tasks/:id/cancel", endpoint.CancelCleanupTask)
	}
}
