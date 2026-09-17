package service

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/ops"
)

// QuotaSettings 为旧独立构造保留唯一新缓存入口，生产由 app 显式注入。
func (s *SettingService) QuotaSettings() *account.QuotaSettingsCache {
	if s == nil {
		return nil
	}
	s.quotaSettingsOnce.Do(func() {
		if s.quotaSettings == nil {
			s.quotaSettings = account.NewQuotaSettingsCache(s.settingRepo, ErrSettingNotFound, ops.ParseRuntimeQuotaAutoPauseSettings)
		}
	})
	return s.quotaSettings
}
func (s *SettingService) SetQuotaSettings(value *account.QuotaSettingsCache) { s.quotaSettings = value }
