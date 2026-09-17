package httpapi

import "github.com/gin-gonic/gin"

// RegisterUserRoutes 注册所属用户接口，共享鉴权、限流和审计已由 app 安装。
func RegisterUserRoutes(authenticated *gin.RouterGroup, endpoint *CreativeHandler, heavy gin.HandlerFunc) {
	creative := authenticated.Group("/creative")
	{
		creative.GET("/capabilities", endpoint.ListCapabilities)
		creative.GET("/models", endpoint.ListModels)
		creative.POST("/runs", heavy, endpoint.CreateRun)
		creative.GET("/runs/active", endpoint.ListActiveRuns)
		creative.GET("/runs", endpoint.ListRuns)
		creative.GET("/runs/:id", endpoint.GetRun)
		creative.GET("/runs/:id/outputs/:index/content", endpoint.GetOutputContent)
		creative.POST("/runs/:id/outputs/:index/ack", endpoint.AckOutput)
	}
}
