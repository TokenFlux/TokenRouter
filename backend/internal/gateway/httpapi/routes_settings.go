package httpapi

import "github.com/gin-gonic/gin"

// GatewaySettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type GatewaySettingsEndpoints interface {
	GetRectifierSettings(*gin.Context)
	UpdateRectifierSettings(*gin.Context)
	GetBetaPolicySettings(*gin.Context)
	UpdateBetaPolicySettings(*gin.Context)
}

// RegisterGatewaySettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterGatewaySettingsRoutes(adminSettings *gin.RouterGroup, endpoint GatewaySettingsEndpoints) {
	adminSettings.GET("/rectifier", endpoint.GetRectifierSettings)
	adminSettings.PUT("/rectifier", endpoint.UpdateRectifierSettings)
	adminSettings.GET("/beta-policy", endpoint.GetBetaPolicySettings)
	adminSettings.PUT("/beta-policy", endpoint.UpdateBetaPolicySettings)
}
