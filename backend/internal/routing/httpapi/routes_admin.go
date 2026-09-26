package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterPricingRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterPricingRoutes(admin *gin.RouterGroup, endpoint *PricingHandler) {
	admin.GET("/pricing/defaults", endpoint.ListDefaultPricing)
	admin.GET("/pricing/defaults/model", endpoint.GetModelDefaultPricing)
	admin.GET("/pricing/defaults/models", endpoint.SyncPricingModels)
	pricingConfigs := admin.Group("/pricing/configs")
	{
		pricingConfigs.GET("", endpoint.List)
		pricingConfigs.GET("/:id", endpoint.GetByID)
		pricingConfigs.POST("", endpoint.Create)
		pricingConfigs.PUT("/:id", endpoint.Update)
		pricingConfigs.DELETE("/:id", endpoint.Delete)
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
