package service

import "github.com/TokenFlux/TokenRouter/internal/server/runtimeconfig"

// SetForwardedSettings 仅在 app 构造时绑定唯一技术设置实例。
func (s *SettingService) SetForwardedSettings(value *runtimeconfig.ForwardedSettings) {
	s.forwardedSettings = value
}

// ForwardedSettings 为旧构造保留参数投影，生产在 app 中直接提供。
func (s *SettingService) ForwardedSettings() *runtimeconfig.ForwardedSettings {
	s.forwardedSettingsOnce.Do(func() {
		if s.forwardedSettings != nil {
			return
		}
		s.forwardedSettings = runtimeconfig.NewForwardedSettings(s.settingRepo, runtimeconfig.ForwardedSettingsOptions{InitialTrust: s.cfg.Security.TrustForwardedIPForAPIKeyACL, TrustedProxiesConfigured: s.cfg.Server.TrustedProxiesConfigured, Headers: func() []string { return s.cfg.ForwardedClientIPSettings().Headers }, Publish: s.cfg.SetForwardedClientIPSettings})
	})
	return s.forwardedSettings
}
