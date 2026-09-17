package scheduler

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// RuntimeSettingsFromAdmin 保留每项独立默认、存在性和原解析顺序，不创建第二个运行实例。
func RuntimeSettingsFromAdmin(value AdminSettings, defaults AdminDefaults) policy.RuntimeSettings {
	errorAlpha, _ := ParseAdvancedSchedulerAlphaOverride(value.AdvancedSchedulerEWMAErrorRateAlpha, defaults.Process.EwmaErrorRateAlpha)
	ttftAlpha, _ := ParseAdvancedSchedulerAlphaOverride(value.AdvancedSchedulerEWMATTFTAlpha, defaults.Process.EwmaTTFTAlpha)
	stickyTTFT, _ := ParseAdvancedSchedulerPositiveFloatOverride(value.AdvancedSchedulerStickyEscapeTTFTMs, defaults.Process.StickyEscape.TtftMs)
	stickyRate, _ := ParseAdvancedSchedulerRateOverride(value.AdvancedSchedulerStickyEscapeErrorRate, defaults.Process.StickyEscape.ErrorRate)
	return policy.RuntimeSettings{
		StickyWeightedEnabled: value.AdvancedSchedulerStickyWeightedEnabled, SubscriptionPriorityEnabled: value.AdvancedSchedulerSubscriptionPriorityEnabled, LbTopKOverride: ParsePositiveIntOverride(value.AdvancedSchedulerLBTopK),
		EwmaErrorRateAlpha: errorAlpha, EwmaErrorRateAlphaSet: strings.TrimSpace(value.AdvancedSchedulerEWMAErrorRateAlpha) != "", EwmaTTFTAlpha: ttftAlpha, EwmaTTFTAlphaSet: strings.TrimSpace(value.AdvancedSchedulerEWMATTFTAlpha) != "",
		StickyEscapeEnabled: value.AdvancedSchedulerStickyEscapeEnabled, StickyEscapeEnabledSet: value.AdvancedSchedulerStickyEscapeEnabledSet, StickyEscapeTTFTMs: stickyTTFT, StickyEscapeTTFTMsSet: strings.TrimSpace(value.AdvancedSchedulerStickyEscapeTTFTMs) != "", StickyEscapeErrorRate: stickyRate, StickyEscapeErrorRateSet: strings.TrimSpace(value.AdvancedSchedulerStickyEscapeErrorRate) != "", StickyEscape: policy.StickyEscapeConfig{Enabled: value.AdvancedSchedulerStickyEscapeEnabled, TtftMs: stickyTTFT, ErrorRate: stickyRate},
		WeightOverrides: ParseAdvancedSchedulerWeightOverrides(map[string]string{
			SettingKeyAdvancedSchedulerWeightErrorRate:        value.AdvancedSchedulerWeightErrorRate,
			SettingKeyAdvancedSchedulerWeightLoad:             value.AdvancedSchedulerWeightLoad,
			SettingKeyAdvancedSchedulerWeightPreviousResponse: value.AdvancedSchedulerWeightPreviousResponse,
			SettingKeyAdvancedSchedulerWeightPriority:         value.AdvancedSchedulerWeightPriority,
			SettingKeyAdvancedSchedulerWeightQueue:            value.AdvancedSchedulerWeightQueue,
			SettingKeyAdvancedSchedulerWeightQuotaHeadroom:    value.AdvancedSchedulerWeightQuotaHeadroom,
			SettingKeyAdvancedSchedulerWeightReset:            value.AdvancedSchedulerWeightReset,
			SettingKeyAdvancedSchedulerWeightSessionSticky:    value.AdvancedSchedulerWeightSessionSticky,
			SettingKeyAdvancedSchedulerWeightTTFT:             value.AdvancedSchedulerWeightTTFT,
		})}
}
