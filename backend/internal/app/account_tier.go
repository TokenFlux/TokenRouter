// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
)

// provideAccountTier 使用同一个账号配置用例，停止纳入原后台预算。
func provideAccountTier(admin *account.Admin, source *account.GeminiAuthorization, manager *lifecycle.Manager) *account.TierManagement {
	core := account.NewTierManagement(admin, account.AccountTierManagementOptions(source))
	manager.Register(lifecycle.Hook{Name: "AccountTier", StopOrder: 26, Stop: core.StopContext})
	return core
}
