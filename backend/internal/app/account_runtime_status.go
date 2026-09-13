// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountRuntimePresenter 直接绑定新展示实现，不再引用旧 handler。
func provideAccountRuntimePresenter(admin *account.Admin, ollama *account.OllamaCloudUsageService, concurrency *service.ConcurrencyService, usage *service.AccountUsageService, sessions service.SessionLimitCache, rpm service.RPMCache, settings *service.SettingService) *accounthttp.RuntimePresenter {
	return accounthttp.NewRuntimePresenter(account.NewRuntimeStatusReader(legacybridge.AccountRuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), admin, ollama)
}

// provideAccountManagementList 复用当前调度反馈和技术读取实例，保持列表批量查询。
func provideAccountManagementList(admin *account.Admin, ollama *account.OllamaCloudUsageService, concurrency *service.ConcurrencyService, usage *service.AccountUsageService, sessions service.SessionLimitCache, rpm service.RPMCache, settings *service.SettingService, limits *service.RateLimitService) *account.ManagementList {
	return account.NewManagementList(admin, account.NewRuntimeStatusReader(legacybridge.AccountRuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), account.NewSchedulerScoreView(admin, legacybridge.AccountSchedulerScoreOptions(concurrency, limits)), ollama)
}
