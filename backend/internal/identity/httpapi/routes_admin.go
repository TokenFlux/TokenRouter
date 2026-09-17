package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterUserAttributeRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterUserAttributeRoutes(admin *gin.RouterGroup, endpoint *UserAttributeHandler) {
	attrs := admin.Group("/user-attributes")
	{
		attrs.GET("", endpoint.ListDefinitions)
		attrs.POST("", endpoint.CreateDefinition)
		attrs.POST("/batch", endpoint.GetBatchUserAttributes)
		attrs.PUT("/reorder", endpoint.ReorderDefinitions)
		attrs.PUT("/:id", endpoint.UpdateDefinition)
		attrs.DELETE("/:id", endpoint.DeleteDefinition)
	}
}

// RegisterUserManagementRoutes 保留用户管理与属性读取的原 URL。
func RegisterUserManagementRoutes[K any](admin *gin.RouterGroup, endpoint *AdminUserHandler[K], attributes *UserAttributeHandler) {
	users := admin.Group("/users")
	{
		users.POST("/batch-concurrency", endpoint.BatchUpdateConcurrency)
		users.POST("/batch-limits", endpoint.BatchUpdateLimits)
		users.GET("", endpoint.List)
		users.GET("/:id", endpoint.GetByID)
		users.POST("/:id/auth-identities", endpoint.BindAuthIdentity)
		users.POST("", endpoint.Create)
		users.PUT("/:id", endpoint.Update)
		users.DELETE("/:id", endpoint.Delete)
		users.POST("/:id/balance", endpoint.UpdateBalance)
		users.GET("/:id/api-keys", endpoint.GetUserAPIKeys)
		users.GET("/:id/usage", endpoint.GetUserUsage)
		users.GET("/:id/balance-history", endpoint.GetBalanceHistory)
		users.POST("/:id/replace-group", endpoint.ReplaceGroup)
		users.GET("/:id/rpm-status", endpoint.GetUserRPMStatus)

		// 用户属性值
		users.GET("/:id/attributes", attributes.GetUserAttributes)
		users.PUT("/:id/attributes", attributes.UpdateUserAttributes)
	}
}
