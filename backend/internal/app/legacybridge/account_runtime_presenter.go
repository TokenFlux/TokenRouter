// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// AccountRuntimeStatusOptions 仅转交 S07/S08 读取端口，健康阈值规则已在 account。
func AccountRuntimeStatusOptions(concurrency *service.ConcurrencyService, usage *service.AccountUsageService, sessions service.SessionLimitCache, rpm service.RPMCache, settings *service.SettingService) account.RuntimeStatusOptions {
	return service.AccountRuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)
}

// AccountSchedulerScoreOptions 只转交 S07 评分能力。
func AccountSchedulerScoreOptions(concurrency *service.ConcurrencyService, limits *service.RateLimitService) account.SchedulerScoreOptions {
	return service.AccountSchedulerScoreOptions(concurrency, limits)
}
