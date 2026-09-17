package service

import "github.com/TokenFlux/TokenRouter/internal/routing"

// RoutingSettings 让旧入口复用所属模块的唯一配置读取器。
func (s *SettingService) RoutingSettings() *routing.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.routingSettingsOnce.Do(func() { s.routingSettings = routing.NewRuntimeSettings(s.settingRepo) })
	return s.routingSettings
}
