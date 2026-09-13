// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

const OpsAlertStatusFiring = native.OpsAlertStatusFiring
const OpsAlertStatusResolved = native.OpsAlertStatusResolved
const OpsAlertStatusManualResolved = native.OpsAlertStatusManualResolved

type OpsAlertRule = native.OpsAlertRule
type OpsAlertEvent = native.OpsAlertEvent
type OpsAlertSilence = native.OpsAlertSilence
type OpsAlertEventFilter = native.OpsAlertEventFilter
