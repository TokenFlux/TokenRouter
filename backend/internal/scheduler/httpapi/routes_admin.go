package httpapi

import "github.com/gin-gonic/gin"

// RegisterAccountDiagnostics 在账号路径下注册唯一评分诊断，组权限由 app 安装。
func RegisterAccountDiagnostics(accounts *gin.RouterGroup, endpoint *DiagnosticsHandler) {
	accounts.GET("/:id/advanced-scheduler-score", endpoint.GetAdvancedSchedulerScore)
	accounts.POST("/:id/advanced-scheduler-score/preview", endpoint.PreviewAdvancedSchedulerScore)
}
