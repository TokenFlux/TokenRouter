// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/ops"
)

func defaultOpsAdvancedSettings() *OpsAdvancedSettings {
	return native.CompatDefaultOpsAdvancedSettings()
}
func normalizeOpsAdvancedSettings(cfg *OpsAdvancedSettings) {
	native.CompatNormalizeOpsAdvancedSettings(cfg)
}
func clampOpsQuotaAutoPauseThreshold(value float64) float64 {
	return native.CompatClampOpsQuotaAutoPauseThreshold(value)
}

func DefaultOpsIgnoredStatusCodes() []int { return native.DefaultOpsIgnoredStatusCodes() }
func NormalizeOpsIgnoredStatusCodes(codes []int) []int {
	return native.NormalizeOpsIgnoredStatusCodes(codes)
}

const SettingKeyOpsMetricThresholds = native.SettingKeyOpsMetricThresholds
