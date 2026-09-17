package httpapi

import "github.com/gin-gonic/gin"

// AccountSettingsEndpoints 只描述所属设置的 HTTP 操作，规则由对应模块实现。
type AccountSettingsEndpoints interface {
	GetStreamTimeoutSettings(*gin.Context)
	UpdateStreamTimeoutSettings(*gin.Context)
	GetOverloadCooldownSettings(*gin.Context)
	UpdateOverloadCooldownSettings(*gin.Context)
	GetOpenAI403CooldownSettings(*gin.Context)
	UpdateOpenAI403CooldownSettings(*gin.Context)
	GetRateLimit429CooldownSettings(*gin.Context)
	UpdateRateLimit429CooldownSettings(*gin.Context)
	GetOpenAIOAuthImportDefaults(*gin.Context)
	UpdateOpenAIOAuthImportDefaults(*gin.Context)
	GetOpenAIImagesOAuthUnavailableCooldownSettings(*gin.Context)
	UpdateOpenAIImagesOAuthUnavailableCooldownSettings(*gin.Context)
}

// RegisterAccountSettingsRoutes 在已经鉴权和审计的设置组中注册原路径。
func RegisterAccountSettingsRoutes(adminSettings *gin.RouterGroup, endpoint AccountSettingsEndpoints) {
	adminSettings.GET("/stream-timeout", endpoint.GetStreamTimeoutSettings)
	adminSettings.PUT("/stream-timeout", endpoint.UpdateStreamTimeoutSettings)
	adminSettings.GET("/overload-cooldown", endpoint.GetOverloadCooldownSettings)
	adminSettings.PUT("/overload-cooldown", endpoint.UpdateOverloadCooldownSettings)
	adminSettings.GET("/openai-403-cooldown", endpoint.GetOpenAI403CooldownSettings)
	adminSettings.PUT("/openai-403-cooldown", endpoint.UpdateOpenAI403CooldownSettings)
	adminSettings.GET("/rate-limit-429-cooldown", endpoint.GetRateLimit429CooldownSettings)
	adminSettings.PUT("/rate-limit-429-cooldown", endpoint.UpdateRateLimit429CooldownSettings)
	adminSettings.GET("/openai-oauth-import-defaults", endpoint.GetOpenAIOAuthImportDefaults)
	adminSettings.PUT("/openai-oauth-import-defaults", endpoint.UpdateOpenAIOAuthImportDefaults)
	adminSettings.GET("/openai-images-oauth-unavailable-cooldown", endpoint.GetOpenAIImagesOAuthUnavailableCooldownSettings)
	adminSettings.PUT("/openai-images-oauth-unavailable-cooldown", endpoint.UpdateOpenAIImagesOAuthUnavailableCooldownSettings)
}
