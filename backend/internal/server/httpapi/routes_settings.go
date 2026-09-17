package httpapi

import "github.com/gin-gonic/gin"

// ServerSettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type ServerSettingsEndpoints interface {
	GetPanelRateLimitSettings(*gin.Context)
	UpdatePanelRateLimitSettings(*gin.Context)
}

// RegisterPanelSettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterPanelSettingsRoutes(adminSettings *gin.RouterGroup, endpoint ServerSettingsEndpoints) {
	adminSettings.GET("/panel-rate-limit", endpoint.GetPanelRateLimitSettings)
	adminSettings.PUT("/panel-rate-limit", endpoint.UpdatePanelRateLimitSettings)
}
