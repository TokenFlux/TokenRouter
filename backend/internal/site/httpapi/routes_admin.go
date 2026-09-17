package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterAnnouncementRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterAnnouncementRoutes(admin *gin.RouterGroup, endpoint *AdminAnnouncementHandler) {
	announcements := admin.Group("/announcements")
	{
		announcements.GET("", endpoint.List)
		announcements.POST("", endpoint.Create)
		announcements.GET("/:id", endpoint.GetByID)
		announcements.PUT("/:id", endpoint.Update)
		announcements.DELETE("/:id", endpoint.Delete)
		announcements.GET("/:id/read-status", endpoint.ListReadStatus)
	}
}
