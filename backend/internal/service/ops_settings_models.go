// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type OpsEmailNotificationConfig = native.OpsEmailNotificationConfig
type OpsEmailAlertConfig = native.OpsEmailAlertConfig
type OpsEmailReportConfig = native.OpsEmailReportConfig
type OpsEmailNotificationConfigUpdateRequest = native.OpsEmailNotificationConfigUpdateRequest
type OpsDistributedLockSettings = native.OpsDistributedLockSettings
type OpsAlertSilenceEntry = native.OpsAlertSilenceEntry
type OpsAlertSilencingSettings = native.OpsAlertSilencingSettings
type OpsMetricThresholds = native.OpsMetricThresholds
type OpsRuntimeLogConfig = native.OpsRuntimeLogConfig
type OpsAlertRuntimeSettings = native.OpsAlertRuntimeSettings
type OpsAdvancedSettings = native.OpsAdvancedSettings
type OpsOpenAIAccountQuotaAutoPauseSettings = native.OpsOpenAIAccountQuotaAutoPauseSettings
type OpsDataRetentionSettings = native.OpsDataRetentionSettings
