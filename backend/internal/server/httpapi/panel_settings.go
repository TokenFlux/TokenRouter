package httpapi

import (
	"context"

	serverdto "github.com/TokenFlux/TokenRouter/internal/server/httpapi/dto"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
	"github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"
	"github.com/gin-gonic/gin"
)

// PanelSettingsStore 只提供面板管理操作，不让 HTTP 依赖旧聚合服务。
type PanelSettingsStore interface {
	GetPanelRateLimitSettings(context.Context) (*runtimeconfig.PanelRateLimitSettings, error)
	SetPanelRateLimitSettings(context.Context, *runtimeconfig.PanelRateLimitSettings) error
}

// PanelSettingsHandler 维持面板限流的独立管理契约。
type PanelSettingsHandler struct{ settingService PanelSettingsStore }

// NewPanelSettingsHandler 注入同一个面板配置实例。
func NewPanelSettingsHandler(service *runtimeconfig.PanelSettings) *PanelSettingsHandler {
	return &PanelSettingsHandler{settingService: service}
}

// GetPanelRateLimitSettings 获取面板 API 限流配置
// GET /api/v1/admin/settings/panel-rate-limit
func (h *PanelSettingsHandler) GetPanelRateLimitSettings(c *gin.Context) {
	settings, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, serverdto.PanelRateLimitSettings{
		Enabled:     settings.Enabled,
		UserRPM:     settings.UserRPM,
		HeavyRPM:    settings.HeavyRPM,
		ExemptAdmin: settings.ExemptAdmin,
		PublicIPRPM: settings.PublicIPRPM,
	})
}

// UpdatePanelRateLimitSettingsRequest 更新面板 API 限流配置请求
type UpdatePanelRateLimitSettingsRequest struct {
	Enabled     bool `json:"enabled"`
	UserRPM     int  `json:"user_rpm"`
	HeavyRPM    int  `json:"heavy_rpm"`
	ExemptAdmin bool `json:"exempt_admin"`
	PublicIPRPM int  `json:"public_ip_rpm"`
}

// UpdatePanelRateLimitSettings 更新面板 API 限流配置
// PUT /api/v1/admin/settings/panel-rate-limit
func (h *PanelSettingsHandler) UpdatePanelRateLimitSettings(c *gin.Context) {
	var req UpdatePanelRateLimitSettingsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.BadRequest(c, "Invalid request: "+err.Error())
		return
	}

	settings := &runtimeconfig.PanelRateLimitSettings{
		Enabled:     req.Enabled,
		UserRPM:     req.UserRPM,
		HeavyRPM:    req.HeavyRPM,
		ExemptAdmin: req.ExemptAdmin,
		PublicIPRPM: req.PublicIPRPM,
	}

	if err := h.settingService.SetPanelRateLimitSettings(c.Request.Context(), settings); err != nil {
		httpx.BadRequest(c, err.Error())
		return
	}

	updatedSettings, err := h.settingService.GetPanelRateLimitSettings(c.Request.Context())
	if err != nil {
		httpx.ErrorFrom(c, err)
		return
	}

	httpx.Success(c, serverdto.PanelRateLimitSettings{
		Enabled:     updatedSettings.Enabled,
		UserRPM:     updatedSettings.UserRPM,
		HeavyRPM:    updatedSettings.HeavyRPM,
		ExemptAdmin: updatedSettings.ExemptAdmin,
		PublicIPRPM: updatedSettings.PublicIPRPM,
	})
}
