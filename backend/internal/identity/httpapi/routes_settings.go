package httpapi

import "github.com/gin-gonic/gin"

// IdentitySettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type IdentitySettingsEndpoints interface {
	GetAdminAPIKey(*gin.Context)
	RegenerateAdminAPIKey(*gin.Context)
	DeleteAdminAPIKey(*gin.Context)
}

// RegisterIdentitySettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterIdentitySettingsRoutes(adminSettings *gin.RouterGroup, endpoint IdentitySettingsEndpoints) {
	adminSettings.GET("/admin-api-key", endpoint.GetAdminAPIKey)
	adminSettings.POST("/admin-api-key/regenerate", endpoint.RegenerateAdminAPIKey)
	adminSettings.DELETE("/admin-api-key", endpoint.DeleteAdminAPIKey)
}
