package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterChannelRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterChannelRoutes(admin *gin.RouterGroup, endpoint *ChannelHandler) {
	channels := admin.Group("/channels")
	{
		channels.GET("", endpoint.List)
		channels.GET("/model-pricing", endpoint.GetModelDefaultPricing)
		channels.GET("/pricing/sync-models", endpoint.SyncPricingModels)
		channels.GET("/:id", endpoint.GetByID)
		channels.POST("", endpoint.Create)
		channels.PUT("/:id", endpoint.Update)
		channels.DELETE("/:id", endpoint.Delete)
	}
}

// RegisterGroupRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterGroupRoutes(admin *gin.RouterGroup, endpoint *GroupHandler) {
	groups := admin.Group("/groups")
	{
		groups.GET("", endpoint.List)
		groups.GET("/all", endpoint.GetAll)
		groups.GET("/usage-summary", endpoint.GetUsageSummary)
		groups.GET("/capacity-summary", endpoint.GetCapacitySummary)
		groups.GET("/live-capability", endpoint.GetLiveCapability)
		groups.PUT("/sort-order", endpoint.UpdateSortOrder)
		groups.GET("/:id/models-list-candidates", endpoint.GetModelsListCandidates)
		groups.GET("/:id", endpoint.GetByID)
		groups.POST("", endpoint.Create)
		groups.POST("/:id/duplicate", endpoint.Duplicate)
		groups.PUT("/:id", endpoint.Update)
		groups.DELETE("/:id", endpoint.Delete)
		groups.GET("/:id/stats", endpoint.GetStats)
		groups.GET("/:id/rate-multipliers", endpoint.GetGroupRateMultipliers)
		groups.PUT("/:id/rate-multipliers", endpoint.BatchSetGroupRateMultipliers)
		groups.DELETE("/:id/rate-multipliers", endpoint.ClearGroupRateMultipliers)
		groups.PUT("/:id/rpm-overrides", endpoint.BatchSetGroupRPMOverrides)
		groups.DELETE("/:id/rpm-overrides", endpoint.ClearGroupRPMOverrides)
		groups.GET("/:id/api-keys", endpoint.GetGroupAPIKeys)
	}
}
