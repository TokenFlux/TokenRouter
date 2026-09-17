package service

import "github.com/TokenFlux/TokenRouter/internal/promotion"

// PromotionSettings 只委托推广模块，兼容旧测试直接构造聚合服务的方式。
func (s *SettingService) PromotionSettings() *promotion.RuntimeSettings {
	s.promotionSettingsOnce.Do(func() {
		if s.promotionSettings == nil {
			s.promotionSettings = promotion.NewRuntimeSettings(s.settingRepo)
		}
	})
	return s.promotionSettings
}
