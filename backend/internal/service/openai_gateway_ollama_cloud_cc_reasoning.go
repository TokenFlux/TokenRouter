package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
)

// Ollama Cloud 的 OpenAI 兼容 /v1/chat/completions 把思维放在 reasoning / thinking，
// 而 DeepSeek/OpenAI 客户端只认 reasoning_content。仅在 raw CC 直转路径上做 wire JSON
// 双向补齐，不改 CC↔Responses / Anthropic / Grok 桥。

func isOllamaCloudRawChatCompletionsAccount(account *gatewayprovider.ExecutionAccount) bool {
	if account == nil || account.Record.Platform != capability.PlatformOpenAI || account.Record.Type != capability.AccountTypeAPIKey {
		return false
	}
	// fork 将手动模式与探测状态分开存储；两者合并后的实际协议为 Chat 时才启用桥。
	if accountcore.ResolveUpstreamTextProtocol(
		account.Record.Extra,
		accountcore.TextProtocolResponses,
	) != accountcore.TextProtocolChatCompletions {
		return false
	}
	if accountHasOllamaCloudUsageExtra(account) {
		return true
	}
	if account.Record.Credentials == nil {
		return false
	}
	baseURL, _ := account.Record.Credentials["base_url"].(string)
	return egress.IsOllamaCloudBaseURL(baseURL)
}

func accountHasOllamaCloudUsageExtra(account *gatewayprovider.ExecutionAccount) bool {
	if account == nil || account.Record.Extra == nil {
		return false
	}
	for _, key := range []string{
		accountcore.OllamaCloudUsageSessionExtraKey,
		accountcore.OllamaCloudUsageAutoRefreshExtraKey,
		accountcore.OllamaCloudUsageSnapshotExtraKey,
	} {
		if _, ok := account.Record.Extra[key]; ok {
			return true
		}
	}
	return false
}

func applyOllamaCloudRawChatCompletionsRequest(account *gatewayprovider.ExecutionAccount, body []byte) []byte {
	if !isOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	body = ollama.NormalizeOllamaCloudChatCompletionsRequest(body)
	return clampOllamaCloudMaxTokens(account, body)
}

func applyOllamaCloudRawChatCompletionsResponse(account *gatewayprovider.ExecutionAccount, body []byte) []byte {
	if !isOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	return ollama.NormalizeOllamaCloudChatCompletionsResponseJSON(body)
}

func applyOllamaCloudRawChatCompletionsSSELine(account *gatewayprovider.ExecutionAccount, line string) string {
	if !isOllamaCloudRawChatCompletionsAccount(account) || line == "" {
		return line
	}
	return ollama.NormalizeOllamaCloudChatCompletionsSSELine(line)
}
