// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	account "github.com/TokenFlux/TokenRouter/internal/account"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountModelSync 将按需查询纳入关闭等待，不在启动时发起请求。
func provideAccountModelSync(source *service.AccountTestService, manager *lifecycle.Manager) *account.ModelSyncService {
	core := account.NewModelSyncService(legacybridge.AccountModelSyncFetch(source))
	manager.Register(lifecycle.Hook{Name: "AccountModelList", StopOrder: 26, Stop: core.StopContext})
	return core
}
