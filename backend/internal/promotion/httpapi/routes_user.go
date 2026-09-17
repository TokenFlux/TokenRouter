package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *UserHandler) {
	user := authenticated.Group("/user")
	user.GET("/aff", endpoint.GetAffiliate)
	user.POST("/aff/transfer", endpoint.TransferAffiliateQuota)
}
