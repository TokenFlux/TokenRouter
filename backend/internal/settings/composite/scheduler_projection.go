package composite

import (
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// SchedulerAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) SchedulerAdminSettings() scheduler.AdminSettings {
	return scheduler.AdminSettings{
		AdvancedSchedulerEWMAErrorRateAlpha:          s.AdvancedSchedulerEWMAErrorRateAlpha,
		AdvancedSchedulerEWMATTFTAlpha:               s.AdvancedSchedulerEWMATTFTAlpha,
		AdvancedSchedulerLBTopK:                      s.AdvancedSchedulerLBTopK,
		AdvancedSchedulerStickyEscapeEnabled:         s.AdvancedSchedulerStickyEscapeEnabled,
		AdvancedSchedulerStickyEscapeEnabledSet:      s.AdvancedSchedulerStickyEscapeEnabledSet,
		AdvancedSchedulerStickyEscapeErrorRate:       s.AdvancedSchedulerStickyEscapeErrorRate,
		AdvancedSchedulerStickyEscapeTTFTMs:          s.AdvancedSchedulerStickyEscapeTTFTMs,
		AdvancedSchedulerStickyWeightedEnabled:       s.AdvancedSchedulerStickyWeightedEnabled,
		AdvancedSchedulerSubscriptionPriorityEnabled: s.AdvancedSchedulerSubscriptionPriorityEnabled,
		AdvancedSchedulerWeightErrorRate:             s.AdvancedSchedulerWeightErrorRate,
		AdvancedSchedulerWeightLoad:                  s.AdvancedSchedulerWeightLoad,
		AdvancedSchedulerWeightPreviousResponse:      s.AdvancedSchedulerWeightPreviousResponse,
		AdvancedSchedulerWeightPriority:              s.AdvancedSchedulerWeightPriority,
		AdvancedSchedulerWeightQueue:                 s.AdvancedSchedulerWeightQueue,
		AdvancedSchedulerWeightQuotaHeadroom:         s.AdvancedSchedulerWeightQuotaHeadroom,
		AdvancedSchedulerWeightReset:                 s.AdvancedSchedulerWeightReset,
		AdvancedSchedulerWeightSessionSticky:         s.AdvancedSchedulerWeightSessionSticky,
		AdvancedSchedulerWeightTTFT:                  s.AdvancedSchedulerWeightTTFT,
	}
}

// ApplySchedulerAdminSettings 仅转换所属模块值，不读取设置或发布状态。
func (s *Snapshot) ApplySchedulerAdminSettings(value scheduler.AdminSettings) {
	s.AdvancedSchedulerEWMAErrorRateAlpha = value.AdvancedSchedulerEWMAErrorRateAlpha
	s.AdvancedSchedulerEWMATTFTAlpha = value.AdvancedSchedulerEWMATTFTAlpha
	s.AdvancedSchedulerLBTopK = value.AdvancedSchedulerLBTopK
	s.AdvancedSchedulerStickyEscapeEnabled = value.AdvancedSchedulerStickyEscapeEnabled
	s.AdvancedSchedulerStickyEscapeEnabledSet = value.AdvancedSchedulerStickyEscapeEnabledSet
	s.AdvancedSchedulerStickyEscapeErrorRate = value.AdvancedSchedulerStickyEscapeErrorRate
	s.AdvancedSchedulerStickyEscapeTTFTMs = value.AdvancedSchedulerStickyEscapeTTFTMs
	s.AdvancedSchedulerStickyWeightedEnabled = value.AdvancedSchedulerStickyWeightedEnabled
	s.AdvancedSchedulerSubscriptionPriorityEnabled = value.AdvancedSchedulerSubscriptionPriorityEnabled
	s.AdvancedSchedulerWeightErrorRate = value.AdvancedSchedulerWeightErrorRate
	s.AdvancedSchedulerWeightLoad = value.AdvancedSchedulerWeightLoad
	s.AdvancedSchedulerWeightPreviousResponse = value.AdvancedSchedulerWeightPreviousResponse
	s.AdvancedSchedulerWeightPriority = value.AdvancedSchedulerWeightPriority
	s.AdvancedSchedulerWeightQueue = value.AdvancedSchedulerWeightQueue
	s.AdvancedSchedulerWeightQuotaHeadroom = value.AdvancedSchedulerWeightQuotaHeadroom
	s.AdvancedSchedulerWeightReset = value.AdvancedSchedulerWeightReset
	s.AdvancedSchedulerWeightSessionSticky = value.AdvancedSchedulerWeightSessionSticky
	s.AdvancedSchedulerWeightTTFT = value.AdvancedSchedulerWeightTTFT
}
