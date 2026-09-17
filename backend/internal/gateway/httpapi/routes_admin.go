package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterErrorPassthroughRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterErrorPassthroughRoutes(admin *gin.RouterGroup, endpoint *ErrorPassthroughHandler) {
	rules := admin.Group("/error-passthrough-rules")
	{
		rules.GET("", endpoint.List)
		rules.GET("/:id", endpoint.GetByID)
		rules.POST("", endpoint.Create)
		rules.PUT("/:id", endpoint.Update)
		rules.DELETE("/:id", endpoint.Delete)
	}
}
