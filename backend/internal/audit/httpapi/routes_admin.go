package httpapi

import (
	"github.com/gin-gonic/gin"
)

// RegisterAuditLogRoutes 注册所属管理路由；组鉴权、限流和审计由 app 预先安装。
func RegisterAuditLogRoutes(admin *gin.RouterGroup, endpoint *AuditLogHandler) {
	auditLogs := admin.Group("/audit-logs")
	{
		auditLogs.GET("", endpoint.List)
		auditLogs.GET("/:id", endpoint.Get)
		// 清空需现场 TOTP 校验（在 handler 内强制），不复用 step-up sudo 窗口
		auditLogs.POST("/clear", endpoint.Clear)
	}
}
