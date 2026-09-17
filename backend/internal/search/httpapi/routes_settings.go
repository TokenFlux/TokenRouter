package httpapi

import "github.com/gin-gonic/gin"

// RegisterSettingsRoutes 保留既有综合设置页面使用的管理路径。
func RegisterSettingsRoutes(adminSettings *gin.RouterGroup, endpoint *Handler) {
	adminSettings.GET("/web-search-emulation", endpoint.GetWebSearchEmulationConfig)
	adminSettings.PUT("/web-search-emulation", endpoint.UpdateWebSearchEmulationConfig)
	adminSettings.POST("/web-search-emulation/test", endpoint.TestWebSearchEmulation)
	adminSettings.POST("/web-search-emulation/reset-usage", endpoint.ResetWebSearchUsage)
}
