package service

import (
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// scheduleOllamaCloudUsageActivity 记录 Ollama Cloud API Key 账号实际尝试了上游模型请求，
// 包括 429、5xx 和传输错误。本地鉴权或校验失败不得调用；DeferredService 会合并写入。
func scheduleOllamaCloudUsageActivity(deferred *acctcore.DeferredService, account *gatewayprovider.ExecutionAccount) {
	if deferred == nil || account == nil || !IsOllamaCloudUsageAccount(account) {
		return
	}
	deferred.ScheduleLastUsedUpdate(account.Record.ID)
}

func OllamaCloudUsageStateFromAccount(account *gatewayprovider.ExecutionAccount) *acctcore.OllamaCloudUsageState {
	return acctcore.OllamaCloudUsageStateFromAccount(gatewayprovider.ExecutionRecord(account))
}

func IsOllamaCloudUsageAccount(account *gatewayprovider.ExecutionAccount) bool {
	return acctcore.IsOllamaCloudUsageAccount(gatewayprovider.ExecutionRecord(account))
}
