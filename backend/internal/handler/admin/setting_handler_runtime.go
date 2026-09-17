package admin

import (
	accountSettingsHTTP "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	gatewaySettingsHTTP "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	identitySettingsHTTP "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"

	searchhttp "github.com/TokenFlux/TokenRouter/internal/search/httpapi"

	"github.com/gin-gonic/gin"
)

// GetAdminAPIKey 获取管理员 API Key 状态
// GET /api/v1/admin/settings/admin-api-key
func (h *SettingHandler) GetAdminAPIKey(c *gin.Context) {
	identitySettingsHTTP.NewAdminKeySettingsHandler(h.settingService.IdentitySettings()).GetAdminAPIKey(c)
}

// RegenerateAdminAPIKey 生成/重新生成管理员 API Key
// POST /api/v1/admin/settings/admin-api-key/regenerate
func (h *SettingHandler) RegenerateAdminAPIKey(c *gin.Context) {
	identitySettingsHTTP.NewAdminKeySettingsHandler(h.settingService.IdentitySettings()).RegenerateAdminAPIKey(c)
}

// DeleteAdminAPIKey 删除管理员 API Key
// DELETE /api/v1/admin/settings/admin-api-key
func (h *SettingHandler) DeleteAdminAPIKey(c *gin.Context) {
	identitySettingsHTTP.NewAdminKeySettingsHandler(h.settingService.IdentitySettings()).DeleteAdminAPIKey(c)
}

// GetOverloadCooldownSettings 获取529过载冷却配置
// GET /api/v1/admin/settings/overload-cooldown
func (h *SettingHandler) GetOverloadCooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetOverloadCooldownSettings(c)
}

// UpdateOverloadCooldownSettingsRequest 更新529过载冷却配置请求
type UpdateOverloadCooldownSettingsRequest = accountSettingsHTTP.UpdateOverloadCooldownSettingsRequest

// UpdateOverloadCooldownSettings 更新529过载冷却配置
// PUT /api/v1/admin/settings/overload-cooldown
func (h *SettingHandler) UpdateOverloadCooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateOverloadCooldownSettings(c)
}

// GetRateLimit429CooldownSettings 获取429默认回避配置
// GET /api/v1/admin/settings/rate-limit-429-cooldown
func (h *SettingHandler) GetRateLimit429CooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetRateLimit429CooldownSettings(c)
}

// UpdateRateLimit429CooldownSettingsRequest 更新429默认回避配置请求
type UpdateRateLimit429CooldownSettingsRequest = accountSettingsHTTP.UpdateRateLimit429CooldownSettingsRequest

// UpdateRateLimit429CooldownSettings 更新429默认回避配置
// PUT /api/v1/admin/settings/rate-limit-429-cooldown
func (h *SettingHandler) UpdateRateLimit429CooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateRateLimit429CooldownSettings(c)
}

func (h *SettingHandler) GetOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetOpenAIImagesOAuthUnavailableCooldownSettings(c)
}

type UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest = accountSettingsHTTP.UpdateOpenAIImagesOAuthUnavailableCooldownSettingsRequest

func (h *SettingHandler) UpdateOpenAIImagesOAuthUnavailableCooldownSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateOpenAIImagesOAuthUnavailableCooldownSettings(c)
}

// GetPanelRateLimitSettings 保留旧测试和兼容调用，生产路由直接绑定 server。
func (h *SettingHandler) GetPanelRateLimitSettings(c *gin.Context) {
	serverhttp.NewPanelSettingsHandler(h.settingService.PanelSettings()).GetPanelRateLimitSettings(c)
}

// UpdatePanelRateLimitSettings 委托唯一 HTTP 实现。
func (h *SettingHandler) UpdatePanelRateLimitSettings(c *gin.Context) {
	serverhttp.NewPanelSettingsHandler(h.settingService.PanelSettings()).UpdatePanelRateLimitSettings(c)
}

// UpdatePanelRateLimitSettingsRequest 保留旧输入名称。
type UpdatePanelRateLimitSettingsRequest = serverhttp.UpdatePanelRateLimitSettingsRequest

// GetStreamTimeoutSettings 获取流超时处理配置
// GET /api/v1/admin/settings/stream-timeout
func (h *SettingHandler) GetStreamTimeoutSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).GetStreamTimeoutSettings(c)
}

// GetRectifierSettings 获取请求整流器配置
// GET /api/v1/admin/settings/rectifier
func (h *SettingHandler) GetRectifierSettings(c *gin.Context) {
	gatewaySettingsHTTP.NewRuntimeSettingsHandler(h.settingService.GatewaySettings()).GetRectifierSettings(c)
}

// UpdateRectifierSettingsRequest 更新整流器配置请求
type UpdateRectifierSettingsRequest = gatewaySettingsHTTP.UpdateRectifierSettingsRequest

// UpdateRectifierSettings 更新请求整流器配置
// PUT /api/v1/admin/settings/rectifier
func (h *SettingHandler) UpdateRectifierSettings(c *gin.Context) {
	gatewaySettingsHTTP.NewRuntimeSettingsHandler(h.settingService.GatewaySettings()).UpdateRectifierSettings(c)
}

// GetBetaPolicySettings 获取 Beta 策略配置
// GET /api/v1/admin/settings/beta-policy
func (h *SettingHandler) GetBetaPolicySettings(c *gin.Context) {
	gatewaySettingsHTTP.NewRuntimeSettingsHandler(h.settingService.GatewaySettings()).GetBetaPolicySettings(c)
}

// UpdateBetaPolicySettingsRequest 更新 Beta 策略配置请求
type UpdateBetaPolicySettingsRequest = gatewaySettingsHTTP.UpdateBetaPolicySettingsRequest

// UpdateBetaPolicySettings 更新 Beta 策略配置
// PUT /api/v1/admin/settings/beta-policy
func (h *SettingHandler) UpdateBetaPolicySettings(c *gin.Context) {
	gatewaySettingsHTTP.NewRuntimeSettingsHandler(h.settingService.GatewaySettings()).UpdateBetaPolicySettings(c)
}

// UpdateStreamTimeoutSettingsRequest 更新流超时配置请求
type UpdateStreamTimeoutSettingsRequest = accountSettingsHTTP.UpdateStreamTimeoutSettingsRequest

// UpdateStreamTimeoutSettings 更新流超时处理配置
// PUT /api/v1/admin/settings/stream-timeout
func (h *SettingHandler) UpdateStreamTimeoutSettings(c *gin.Context) {
	accountSettingsHTTP.NewRuntimeSettingsHandler(h.settingService.AccountSettings()).UpdateStreamTimeoutSettings(c)
}

func (h *SettingHandler) GetWebSearchEmulationConfig(c *gin.Context) {
	searchhttp.New(h.settingService.SearchRuntime()).GetWebSearchEmulationConfig(c)
}

func (h *SettingHandler) UpdateWebSearchEmulationConfig(c *gin.Context) {
	searchhttp.New(h.settingService.SearchRuntime()).UpdateWebSearchEmulationConfig(c)
}

func (h *SettingHandler) ResetWebSearchUsage(c *gin.Context) {
	searchhttp.New(h.settingService.SearchRuntime()).ResetWebSearchUsage(c)
}

func (h *SettingHandler) TestWebSearchEmulation(c *gin.Context) {
	searchhttp.New(h.settingService.SearchRuntime()).TestWebSearchEmulation(c)
}
