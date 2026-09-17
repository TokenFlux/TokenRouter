package service

import "github.com/TokenFlux/TokenRouter/internal/identity"

// IdentitySettings 兼容旧设置入口，所有身份规则由 identity 唯一实现。
func (s *SettingService) IdentitySettings() *identity.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.identitySettingsOnce.Do(func() {
		if s.identitySettings == nil {
			s.identitySettings = identity.NewRuntimeSettings(s.settingRepo, ErrSettingNotFound)
		}
	})
	return s.identitySettings
}
