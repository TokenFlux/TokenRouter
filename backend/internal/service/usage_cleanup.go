// 旧契约只别名 usage 的唯一实现，S15/S16 清理。
package service

import "github.com/TokenFlux/TokenRouter/internal/usage"

type UsageCleanupFilters = usage.UsageCleanupFilters
type UsageCleanupTask = usage.UsageCleanupTask
type UsageCleanupRepository = usage.UsageCleanupRepository

const UsageCleanupStatusPending = usage.UsageCleanupStatusPending
const UsageCleanupStatusRunning = usage.UsageCleanupStatusRunning
const UsageCleanupStatusSucceeded = usage.UsageCleanupStatusSucceeded
const UsageCleanupStatusFailed = usage.UsageCleanupStatusFailed
const UsageCleanupStatusCanceled = usage.UsageCleanupStatusCanceled
