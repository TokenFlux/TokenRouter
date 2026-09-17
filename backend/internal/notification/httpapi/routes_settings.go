package httpapi

import "github.com/gin-gonic/gin"

// RegisterSettingsRoutes 保留既有综合设置页面使用的管理路径。
func RegisterSettingsRoutes(adminSettings *gin.RouterGroup, endpoint *Handler) {
	adminSettings.POST("/test-smtp", endpoint.TestSMTPConnection)
	adminSettings.POST("/send-test-email", endpoint.SendTestEmail)
	adminSettings.GET("/email-templates", endpoint.ListEmailTemplates)
	adminSettings.POST("/email-template-preview", endpoint.PreviewEmailTemplate)
	adminSettings.GET("/email-templates/:event/:locale", endpoint.GetEmailTemplate)
	adminSettings.PUT("/email-templates/:event/:locale", endpoint.UpdateEmailTemplate)
	adminSettings.POST("/email-templates/:event/:locale/restore-official", endpoint.RestoreOfficialEmailTemplate)
}
