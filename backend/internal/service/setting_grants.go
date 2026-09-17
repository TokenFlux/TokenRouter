package service

import "github.com/TokenFlux/TokenRouter/internal/identity"

// SetGrantSettings 在 app 构造完成前绑定唯一身份默认配置读取器。
func (s *SettingService) SetGrantSettings(value *identity.GrantSettings) { s.grantSettings = value }

// GrantSettings 只为旧构造保留启动值投影，规则已迁入身份模块。
func (s *SettingService) GrantSettings() *identity.GrantSettings {
	s.grantSettingsOnce.Do(func() {
		if s.grantSettings != nil {
			return
		}
		options := identity.GrantSettingsOptions{ValidatePlans: s.validateDefaultSubscriptionPlans}
		if s.cfg != nil {
			options.DefaultBalance = s.cfg.Default.UserBalance
			options.DefaultConcurrency = s.cfg.Default.UserConcurrency
		}
		s.grantSettings = identity.NewGrantSettings(s.settingRepo, options)
	})
	return s.grantSettings
}
