package httpapi

import "github.com/gin-gonic/gin"

// SettingsSettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type SettingsSettingsEndpoints interface {
	GetSettings(*gin.Context)
	UpdateSettings(*gin.Context)
}

// PreAggregationEndpoints 描述独立预聚合端点。
type PreAggregationEndpoints interface {
	GetPreAggregationSettings(*gin.Context)
	UpdatePreAggregationSettings(*gin.Context)
	BackfillPreAggregation(*gin.Context)
}

// RegisterSettingsSettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterSettingsSettingsRoutes(adminSettings *gin.RouterGroup, endpoint SettingsSettingsEndpoints, pre PreAggregationEndpoints) {
	adminSettings.GET("", endpoint.GetSettings)
	adminSettings.PUT("", endpoint.UpdateSettings)
	adminSettings.GET("/pre-aggregation", pre.GetPreAggregationSettings)
	adminSettings.PUT("/pre-aggregation", pre.UpdatePreAggregationSettings)
	adminSettings.POST("/pre-aggregation/backfill", pre.BackfillPreAggregation)
}
