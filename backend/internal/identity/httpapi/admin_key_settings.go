package httpapi

import (
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/gin-gonic/gin"
)

// AdminKeySettingsHandler 仅处理管理员凭据设置，不拥有通用身份或系统设置聚合。
type AdminKeySettingsHandler struct{ settingService *identity.RuntimeSettings }

// NewAdminKeySettingsHandler 接入同一身份设置实例。
func NewAdminKeySettingsHandler(settings *identity.RuntimeSettings) *AdminKeySettingsHandler {
	return &AdminKeySettingsHandler{settingService: settings}
}

// GetAdminAPIKey 保留已有管理员凭据管理响应。
func (h *AdminKeySettingsHandler) GetAdminAPIKey(c *gin.Context) {
	maskedKey, exists, err := h.settingService.GetAdminAPIKeyStatus(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, gin.H{
		"exists":     exists,
		"masked_key": maskedKey,
	})
}

// RegenerateAdminAPIKey 保留已有管理员凭据管理响应。
func (h *AdminKeySettingsHandler) RegenerateAdminAPIKey(c *gin.Context) {
	key, err := h.settingService.GenerateAdminAPIKey(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, gin.H{
		"key": key, // 完整 key 只在生成时返回一次
	})
}

// DeleteAdminAPIKey 保留已有管理员凭据管理响应。
func (h *AdminKeySettingsHandler) DeleteAdminAPIKey(c *gin.Context) {
	if err := h.settingService.DeleteAdminAPIKey(c.Request.Context()); err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, gin.H{"message": "Admin API key deleted"})
}
