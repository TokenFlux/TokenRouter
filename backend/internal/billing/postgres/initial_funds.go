// 本文件维护 postgres 的所属能力；兼容入口复用唯一实现。
package postgres

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/billing"
)

// ApplyInitialUserFunds 将初始资金加入同一条用户 INSERT，不另行提交或刷新缓存。
func ApplyInitialUserFunds(create *dbent.UserCreate, funds billing.InitialUserFunds) {
	create.SetBalance(funds.Balance)
}
