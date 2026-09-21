// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"

	account "github.com/TokenFlux/TokenRouter/internal/account"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/config"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideAccountRuntimePresenter 直接绑定新展示实现，不再引用旧 handler。
func provideAccountRuntimePresenter(admin *account.Admin, ollama *account.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *account.QuotaSettingsCache) *accounthttp.RuntimePresenter {
	return accounthttp.NewRuntimePresenter(account.NewRuntimeStatusReader(provider.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), admin, ollama)
}

// provideAccountManagementList 复用当前调度反馈和技术读取实例，保持列表批量查询。
func provideAccountManagementList(admin *account.Admin, ollama *account.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *account.QuotaSettingsCache, shared *schedulerSharedState, store *settingscore.Store, cfg *config.Config) *account.ManagementList {
	return account.NewManagementList(admin, account.NewRuntimeStatusReader(provider.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), account.NewSchedulerScoreView(admin, accountScoreOptions(concurrency, shared, store, cfg)), ollama)
}
