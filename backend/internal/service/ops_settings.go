// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/ops"
)

func DefaultOpsIgnoredStatusCodes() []int { return native.DefaultOpsIgnoredStatusCodes() }
func NormalizeOpsIgnoredStatusCodes(codes []int) []int {
	return native.NormalizeOpsIgnoredStatusCodes(codes)
}

const SettingKeyOpsMetricThresholds = native.SettingKeyOpsMetricThresholds
