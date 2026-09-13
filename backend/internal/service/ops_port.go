// 兼容入口只引用所属模块的唯一实现，S15/S16 清理。
package service

import native "github.com/TokenFlux/TokenRouter/internal/ops"

type OpsRepository = native.OpsRepository
type OpsInsertErrorLogInput = native.OpsInsertErrorLogInput
type OpsInsertSystemMetricsInput = native.OpsInsertSystemMetricsInput
type OpsInsertSystemLogInput = native.OpsInsertSystemLogInput
type OpsSystemLogFilter = native.OpsSystemLogFilter
type OpsSystemLogCleanupFilter = native.OpsSystemLogCleanupFilter
type OpsSystemLogList = native.OpsSystemLogList
type OpsSystemLogCleanupAudit = native.OpsSystemLogCleanupAudit
type OpsSystemMetricsSnapshot = native.OpsSystemMetricsSnapshot
type OpsUpsertJobHeartbeatInput = native.OpsUpsertJobHeartbeatInput
type OpsJobHeartbeat = native.OpsJobHeartbeat
type OpsWindowStats = native.OpsWindowStats
