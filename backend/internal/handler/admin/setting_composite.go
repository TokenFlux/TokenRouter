package admin

import (
	settingshttp "github.com/TokenFlux/TokenRouter/internal/settings/httpapi"
)

// compositeHTTP 为旧独立构造夹具绑定窄端口，生产由 app 构造原生处理器。
func (h *SettingHandler) compositeHTTP() *settingshttp.Handler {
	registry, err := h.compositeParticipants()
	options := settingshttp.HandlerOptions{Settings: h.settingService, Participants: registry, ParticipantError: err, Attributes: h.userAttributeService, Creative: h.creativeModelReader}
	if h.opsService != nil {
		options.Monitoring = h.opsService
	}
	if h.paymentConfigService != nil {
		options.Payment = h.paymentConfigService
	}
	if h.turnstileService != nil {
		options.Turnstile = h.turnstileService
	}
	if h.aliyunCaptchaService != nil {
		options.Aliyun = h.aliyunCaptchaService
	}
	if h.totpService != nil {
		options.Totp = h.totpService
	}
	if h.userService != nil {
		options.User = h.userService.UserService
	}
	return settingshttp.NewHandler(options)
}
