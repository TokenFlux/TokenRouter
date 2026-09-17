package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *AnnouncementHandler) {
	announcements := authenticated.Group("/announcements")
	{
		announcements.GET("", endpoint.List)
		announcements.POST("/:id/read", endpoint.MarkRead)
	}
}
