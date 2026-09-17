package composite

import "github.com/TokenFlux/TokenRouter/internal/routing"

// ApplyRoutingAdminReadSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplyRoutingAdminReadSettings(value *routing.AdminReadSettings) {
	s.AllowUngroupedKeyScheduling = value.AllowUngroupedKeyScheduling
	s.EnableModelFallback = value.EnableModelFallback
	s.FallbackModelAnthropic = value.FallbackModelAnthropic
	s.FallbackModelAntigravity = value.FallbackModelAntigravity
	s.FallbackModelGemini = value.FallbackModelGemini
	s.FallbackModelOpenAI = value.FallbackModelOpenAI
	s.MarketplaceAvailabilityBucketMinutes = value.MarketplaceAvailabilityBucketMinutes
	s.MarketplaceAvailabilityWindowDays = value.MarketplaceAvailabilityWindowDays
}
