package service

import "github.com/TokenFlux/TokenRouter/internal/creative"

// CreativeSettings 只返回创作模块的同一设置读取器，兼容旧构造入口。
func (s *SettingService) CreativeSettings() *creative.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.creativeSettingsOnce.Do(func() { s.creativeSettings = creative.NewRuntimeSettings(s.settingRepo, ErrSettingNotFound) })
	return s.creativeSettings
}
