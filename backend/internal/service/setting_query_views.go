package service

import (
	"github.com/TokenFlux/TokenRouter/internal/audit"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// UsageSettings 返回唯一用量设置投影，兼容旧构造调用。
func (s *SettingService) UsageSettings() *usage.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.usageSettingsOnce.Do(func() {
		if s.usageSettings == nil {
			s.usageSettings = usage.NewRuntimeSettings(s.settingRepo)
		}
	})
	return s.usageSettings
}

// AuditSettings 返回审计模块自身的保留期读取器。
func (s *SettingService) AuditSettings() *audit.RetentionSettings {
	s.auditSettingsOnce.Do(func() {
		if s.auditSettings == nil {
			s.auditSettings = audit.NewRetentionSettings(s.settingRepo)
		}
	})
	return s.auditSettings
}
