package handler

import (
	notificationhttp "github.com/TokenFlux/TokenRouter/internal/notification/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitehttp "github.com/TokenFlux/TokenRouter/internal/site/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/service"

	"github.com/gin-gonic/gin"
)

// SettingHandler 公开设置处理器（无需认证）
type SettingHandler struct {
	settingService           *service.SettingService
	notificationEmailService *service.NotificationEmailService
	version                  string
}

// NewSettingHandler 创建公开设置处理器
func NewSettingHandler(settingService *service.SettingService, version string) *SettingHandler {
	return &SettingHandler{
		settingService: settingService,
		version:        version,
	}
}

// SetNotificationEmailService 注入公开退订入口需要的通知邮件服务，并保持既有构造函数签名不变。
func (h *SettingHandler) SetNotificationEmailService(notificationEmailService *service.NotificationEmailService) {
	h.notificationEmailService = notificationEmailService
}

func (h *SettingHandler) GetPublicSettings(c *gin.Context) {
	sitehttp.NewPublicHandler(site.NewPublicService(h.settingService), h.version).GetPublicSettings(c)
}

func (h *SettingHandler) UnsubscribeNotificationEmail(c *gin.Context) {
	notificationhttp.New(nil, h.notificationEmailService, nil).UnsubscribeNotificationEmail(c)
}
