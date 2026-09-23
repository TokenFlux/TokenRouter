package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
)

// provideAccountRuntimeState 由恢复、刷新和执行入口共享，构造不读取存储或启动工作。
func provideAccountRuntimeState() *account.RuntimeBlockState {
	return account.NewRuntimeBlockState(time.Now)
}
