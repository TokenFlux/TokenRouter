package composite

import "github.com/TokenFlux/TokenRouter/internal/routing"

// RoutingAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) RoutingAdminSettings() routing.AdminSettings {
	return routing.AdminSettings{
		AllowUngroupedKeyScheduling:          s.AllowUngroupedKeyScheduling,
		EnableModelFallback:                  s.EnableModelFallback,
		FallbackModelAnthropic:               s.FallbackModelAnthropic,
		FallbackModelAntigravity:             s.FallbackModelAntigravity,
		FallbackModelGemini:                  s.FallbackModelGemini,
		FallbackModelOpenAI:                  s.FallbackModelOpenAI,
		MarketplaceAvailabilityBucketMinutes: s.MarketplaceAvailabilityBucketMinutes,
		MarketplaceAvailabilityWindowDays:    s.MarketplaceAvailabilityWindowDays,
	}
}

// ApplyRoutingAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyRoutingAdminSettings(value routing.AdminSettings) {
	s.AllowUngroupedKeyScheduling = value.AllowUngroupedKeyScheduling
	s.EnableModelFallback = value.EnableModelFallback
	s.FallbackModelAnthropic = value.FallbackModelAnthropic
	s.FallbackModelAntigravity = value.FallbackModelAntigravity
	s.FallbackModelGemini = value.FallbackModelGemini
	s.FallbackModelOpenAI = value.FallbackModelOpenAI
	s.MarketplaceAvailabilityBucketMinutes = value.MarketplaceAvailabilityBucketMinutes
	s.MarketplaceAvailabilityWindowDays = value.MarketplaceAvailabilityWindowDays
}
