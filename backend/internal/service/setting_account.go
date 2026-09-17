package service

import accountcore "github.com/TokenFlux/TokenRouter/internal/account"

// AccountSettings 兼容旧构造入口，返回账号模块持有的唯一规则和缓存。
func (s *SettingService) AccountSettings() *accountcore.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.accountSettingsOnce.Do(func() {
		if s.accountSettings == nil {
			s.accountSettings = accountcore.NewRuntimeSettings(s.settingRepo, ErrSettingNotFound)
		}
	})
	return s.accountSettings
}
