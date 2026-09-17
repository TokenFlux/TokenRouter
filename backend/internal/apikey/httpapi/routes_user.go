package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes[G any](authenticated *gin.RouterGroup, endpoint *APIKeyHandler[G]) {
	keys := authenticated.Group("/keys")
	{
		keys.GET("", endpoint.List)
		keys.GET("/billing-options", endpoint.GetBillingOptions)
		keys.GET("/:id", endpoint.GetByID)
		keys.POST("", endpoint.Create)
		keys.PUT("/:id", endpoint.Update)
		keys.DELETE("/:id", endpoint.Delete)
	}
	groups := authenticated.Group("/groups")
	{
		groups.GET("/available", endpoint.GetAvailableGroups)
		groups.GET("/rates", endpoint.GetUserGroupRates)
	}
}
