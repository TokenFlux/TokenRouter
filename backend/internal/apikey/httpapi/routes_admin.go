package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterAdminAPIKeyRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterAdminAPIKeyRoutes[G any](admin *gin.RouterGroup, endpoint *AdminAPIKeyHandler[G]) {
	apiKeys := admin.Group("/api-keys")
	{
		apiKeys.PUT("/:id", endpoint.UpdateGroup)
	}
}
