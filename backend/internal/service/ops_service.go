// 兼容入口只委托 Ops 的唯一实现，S15/S16 清理。
package service

import (
	native "github.com/TokenFlux/TokenRouter/internal/ops"
)

var ErrOpsDisabled = native.ErrOpsDisabled

const OpsErrorLogQueueBodyMaxBytes = native.OpsErrorLogQueueBodyMaxBytes

type OpsRuntimeSettingsRefreshHealth = native.OpsRuntimeSettingsRefreshHealth
type OpsService = native.OpsService
type CleanupReloader = native.CleanupReloader

func SanitizeOpsErrorBodyForQueue(raw string) (string, bool) {
	return native.SanitizeOpsErrorBodyForQueue(raw)
}
func SanitizeOpsUpstreamErrorsForQueue(entry *OpsInsertErrorLogInput) error {
	return native.SanitizeOpsUpstreamErrorsForQueue(entry)
}

func shallowCopyMap(m map[string]any) map[string]any { return native.CompatShallowCopyMap(m) }
