package httpapi

import "github.com/gin-gonic/gin"

// RegisterPublicSettingsRoutes 使用 app 创建的公开限流组。
func RegisterPublicSettingsRoutes(group *gin.RouterGroup, endpoint *PublicHandler) {
	group.GET("/public", endpoint.GetPublicSettings)
}
