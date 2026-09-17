package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterAffiliateRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterAffiliateRoutes(admin *gin.RouterGroup, endpoint *AffiliateHandler) {
	affiliates := admin.Group("/affiliates")
	{
		affiliates.GET("/users", endpoint.ListUsers)
		affiliates.GET("/users/lookup", endpoint.LookupUsers)
		affiliates.PUT("/users/:user_id", endpoint.UpdateUserSettings)
		affiliates.DELETE("/users/:user_id", endpoint.ClearUserSettings)
		affiliates.POST("/users/batch-rate", endpoint.BatchSetRate)
		affiliates.GET("/users/:user_id/overview", endpoint.GetUserOverview)
		affiliates.GET("/invites", endpoint.ListInviteRecords)
		affiliates.GET("/rebates", endpoint.ListRebateRecords)
		affiliates.GET("/transfers", endpoint.ListTransferRecords)
	}
}

// RegisterPromoCodeRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterPromoCodeRoutes(admin *gin.RouterGroup, endpoint *PromoHandler) {
	promoCodes := admin.Group("/promo-codes")
	{
		promoCodes.GET("", endpoint.List)
		promoCodes.GET("/:id", endpoint.GetByID)
		promoCodes.POST("", endpoint.Create)
		promoCodes.PUT("/:id", endpoint.Update)
		promoCodes.DELETE("/:id", endpoint.Delete)
		promoCodes.GET("/:id/usages", endpoint.GetUsages)
	}
}
