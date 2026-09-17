package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterContentModerationRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterContentModerationRoutes(admin *gin.RouterGroup, endpoint *ContentModerationHandler) {
	risk := admin.Group("/risk-control")
	{
		risk.GET("/config", endpoint.GetConfig)
		risk.PUT("/config", endpoint.UpdateConfig)
		risk.POST("/api-keys/test", endpoint.TestAPIKeys)
		risk.GET("/status", endpoint.GetStatus)
		risk.GET("/logs", endpoint.ListLogs)
		risk.GET("/logs/:id", endpoint.GetLog)
		risk.GET("/cyber-warnings", endpoint.ListCyberWarnings)
		risk.GET("/cyber-warnings/:id", endpoint.GetCyberWarning)
		risk.GET("/media/:id/content", endpoint.GetMediaContent)
		risk.GET("/cyber-summary", endpoint.GetCyberSummary)
		risk.POST("/users/:user_id/unban", endpoint.UnbanUser)
		risk.DELETE("/hashes", endpoint.DeleteFlaggedHash)
		risk.DELETE("/hashes/all", endpoint.ClearFlaggedHashes)
	}
}
