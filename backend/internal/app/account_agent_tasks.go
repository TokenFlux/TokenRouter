package app

import "github.com/TokenFlux/TokenRouter/internal/account"

// provideAgentTaskCoordinator 让所有持久账号任务入口共享同一进程内按账号锁。
// 构造没有后台启动或新的跨进程协调协议。
func provideAgentTaskCoordinator() *account.OpenAITaskCoordinator {
	return &account.OpenAITaskCoordinator{}
}
