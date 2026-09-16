// 旧设置入口委托通知 HTTP Adapter，生产路由直接绑定新 handler。
package admin

import (
	"github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) notificationHTTP() *httpapi.Handler {
	return httpapi.New(h.emailService.Mailer, h.notificationEmailService, h.settingService)
}

type TestSMTPRequest = httpapi.TestSMTPRequest
type SendTestEmailRequest = httpapi.SendTestEmailRequest

func (h *SettingHandler) TestSMTPConnection(c *gin.Context) {
	h.notificationHTTP().TestSMTPConnection(c)
}
func (h *SettingHandler) SendTestEmail(c *gin.Context) { h.notificationHTTP().SendTestEmail(c) }
func (h *SettingHandler) ListEmailTemplates(c *gin.Context) {
	h.notificationHTTP().ListEmailTemplates(c)
}
func (h *SettingHandler) GetEmailTemplate(c *gin.Context) { h.notificationHTTP().GetEmailTemplate(c) }
func (h *SettingHandler) UpdateEmailTemplate(c *gin.Context) {
	h.notificationHTTP().UpdateEmailTemplate(c)
}
func (h *SettingHandler) RestoreOfficialEmailTemplate(c *gin.Context) {
	h.notificationHTTP().RestoreOfficialEmailTemplate(c)
}
func (h *SettingHandler) PreviewEmailTemplate(c *gin.Context) {
	h.notificationHTTP().PreviewEmailTemplate(c)
}
