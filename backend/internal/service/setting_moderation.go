package service

import "github.com/TokenFlux/TokenRouter/internal/moderation"

// ModerationSettings 返回唯一审核设置缓存；旧入口仅委托。
func (s *SettingService) ModerationSettings() *moderation.RuntimeSettings {
	if s == nil {
		return nil
	}
	s.moderationSettingsOnce.Do(func() {
		if s.moderationSettings == nil {
			s.moderationSettings = moderation.NewRuntimeSettings(s.settingRepo, ErrSettingNotFound)
		}
	})
	return s.moderationSettings
}
