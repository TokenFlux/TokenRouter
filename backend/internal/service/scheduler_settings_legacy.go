package service

import (
	"sync/atomic"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

var schedulerSettingsRuntime atomic.Pointer[scheduler.SettingsRuntime]

// SchedulerSettingsRuntime 返回 app 注入的唯一实例；旧独立构造器按需创建兼容实例。
func SchedulerSettingsRuntime() *scheduler.SettingsRuntime {
	if value := schedulerSettingsRuntime.Load(); value != nil {
		return value
	}
	candidate := scheduler.NewSettingsRuntime(LegacySchedulerDiagnostics())
	if schedulerSettingsRuntime.CompareAndSwap(nil, candidate) {
		return candidate
	}
	return schedulerSettingsRuntime.Load()
}

// BindSchedulerSettingsRuntime 只用于组合根装配，不复制设置缓存或 singleflight。
func BindSchedulerSettingsRuntime(value *scheduler.SettingsRuntime) {
	schedulerSettingsRuntime.Store(value)
}
func schedulerRuntimeSettings(v advancedSchedulerRuntimeSettings) policy.RuntimeSettings {
	return policy.RuntimeSettings{
		StickyWeightedEnabled:       v.stickyWeightedEnabled,
		SubscriptionPriorityEnabled: v.subscriptionPriorityEnabled,
		LbTopKOverride:              v.lbTopKOverride,
		WeightOverrides:             v.weightOverrides,
		EwmaErrorRateAlpha:          v.ewmaErrorRateAlpha,
		EwmaErrorRateAlphaSet:       v.ewmaErrorRateAlphaSet,
		EwmaTTFTAlpha:               v.ewmaTTFTAlpha,
		EwmaTTFTAlphaSet:            v.ewmaTTFTAlphaSet,
		StickyEscapeEnabled:         v.stickyEscapeEnabled,
		StickyEscapeEnabledSet:      v.stickyEscapeEnabledSet,
		StickyEscapeTTFTMs:          v.stickyEscapeTTFTMs,
		StickyEscapeTTFTMsSet:       v.stickyEscapeTTFTMsSet,
		StickyEscapeErrorRate:       v.stickyEscapeErrorRate,
		StickyEscapeErrorRateSet:    v.stickyEscapeErrorRateSet,
		StickyEscape:                policy.StickyEscapeConfig{Enabled: v.stickyEscape.enabled, TtftMs: v.stickyEscape.ttftMs, ErrorRate: v.stickyEscape.errorRate},
	}
}
func legacySchedulerRuntimeSettings(v policy.RuntimeSettings) advancedSchedulerRuntimeSettings {
	return advancedSchedulerRuntimeSettings{
		stickyWeightedEnabled:       v.StickyWeightedEnabled,
		subscriptionPriorityEnabled: v.SubscriptionPriorityEnabled,
		lbTopKOverride:              v.LbTopKOverride,
		weightOverrides:             v.WeightOverrides,
		ewmaErrorRateAlpha:          v.EwmaErrorRateAlpha,
		ewmaErrorRateAlphaSet:       v.EwmaErrorRateAlphaSet,
		ewmaTTFTAlpha:               v.EwmaTTFTAlpha,
		ewmaTTFTAlphaSet:            v.EwmaTTFTAlphaSet,
		stickyEscapeEnabled:         v.StickyEscapeEnabled,
		stickyEscapeEnabledSet:      v.StickyEscapeEnabledSet,
		stickyEscapeTTFTMs:          v.StickyEscapeTTFTMs,
		stickyEscapeTTFTMsSet:       v.StickyEscapeTTFTMsSet,
		stickyEscapeErrorRate:       v.StickyEscapeErrorRate,
		stickyEscapeErrorRateSet:    v.StickyEscapeErrorRateSet,
		stickyEscape:                advancedStickyEscapeConfig{enabled: v.StickyEscape.Enabled, ttftMs: v.StickyEscape.TtftMs, errorRate: v.StickyEscape.ErrorRate},
	}
}
