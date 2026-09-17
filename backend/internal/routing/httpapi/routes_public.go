package httpapi

import "github.com/gin-gonic/gin"

// RegisterPublicMarketplaceRoutes 保留无鉴权的市场查询入口。
func RegisterPublicMarketplaceRoutes(v1 *gin.RouterGroup, endpoint *MarketplaceHandler) {
	marketplace := v1.Group("/marketplace")
	marketplace.GET("/models", endpoint.ListPublic)
	marketplace.GET("/stats", endpoint.StatsPublic)
}
