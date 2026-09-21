package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
)

// scheduleOllamaCloudUsageActivity 记录 Ollama Cloud API Key 账号实际尝试了上游模型请求，
// 包括 429、5xx 和传输错误。本地鉴权或校验失败不得调用；DeferredService 会合并写入。
func scheduleOllamaCloudUsageActivity(deferred *acctcore.DeferredService, account *Account) {
	if deferred == nil || account == nil || !IsOllamaCloudUsageAccount(account) {
		return
	}
	deferred.ScheduleLastUsedUpdate(account.ID)
}

func OllamaCloudUsageStateFromAccount(account *Account) *acctcore.OllamaCloudUsageState {
	return acctcore.OllamaCloudUsageStateFromAccount(AccountRecordView(account))
}

func IsOllamaCloudUsageAccount(account *Account) bool {
	return acctcore.IsOllamaCloudUsageAccount(AccountRecordView(account))
}
