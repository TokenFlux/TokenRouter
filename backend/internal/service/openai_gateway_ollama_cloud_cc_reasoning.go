package service

import (
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"

	"github.com/TokenFlux/TokenRouter/internal/pkg/openai_compat"
)

// Ollama Cloud 的 OpenAI 兼容 /v1/chat/completions 把思维放在 reasoning / thinking，
// 而 DeepSeek/OpenAI 客户端只认 reasoning_content。仅在 raw CC 直转路径上做 wire JSON
// 双向补齐，不改 CC↔Responses / Anthropic / Grok 桥。

func isOllamaCloudRawChatCompletionsAccount(account *Account) bool {
	if account == nil || account.Platform != PlatformOpenAI || account.Type != AccountTypeAPIKey {
		return false
	}
	// fork 将手动模式与探测状态分开存储；两者合并后的实际协议为 Chat 时才启用桥。
	if openai_compat.ResolveUpstreamTextProtocol(
		account.Extra,
		openai_compat.TextProtocolResponses,
	) != openai_compat.TextProtocolChatCompletions {
		return false
	}
	if accountHasOllamaCloudUsageExtra(account) {
		return true
	}
	if account.Credentials == nil {
		return false
	}
	baseURL, _ := account.Credentials["base_url"].(string)
	return isOllamaCloudBaseURL(baseURL)
}

func accountHasOllamaCloudUsageExtra(account *Account) bool {
	if account == nil || account.Extra == nil {
		return false
	}
	for _, key := range []string{
		OllamaCloudUsageSessionExtraKey,
		OllamaCloudUsageAutoRefreshExtraKey,
		OllamaCloudUsageSnapshotExtraKey,
	} {
		if _, ok := account.Extra[key]; ok {
			return true
		}
	}
	return false
}

func applyOllamaCloudRawChatCompletionsRequest(account *Account, body []byte) []byte {
	if !isOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	body = normalizeOllamaCloudChatCompletionsRequest(body)
	return clampOllamaCloudMaxTokens(account, body)
}

func applyOllamaCloudRawChatCompletionsResponse(account *Account, body []byte) []byte {
	if !isOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	return normalizeOllamaCloudChatCompletionsResponseJSON(body)
}

func applyOllamaCloudRawChatCompletionsSSELine(account *Account, line string) string {
	if !isOllamaCloudRawChatCompletionsAccount(account) || line == "" {
		return line
	}
	return normalizeOllamaCloudChatCompletionsSSELine(line)
}

func normalizeOllamaCloudChatCompletionsRequest(body []byte) []byte {
	return ollama.NormalizeOllamaCloudChatCompletionsRequest(body)
}

func normalizeOllamaCloudChatCompletionsResponseJSON(body []byte) []byte {
	return ollama.NormalizeOllamaCloudChatCompletionsResponseJSON(body)
}

func normalizeOllamaCloudChatCompletionsSSELine(line string) string {
	return ollama.NormalizeOllamaCloudChatCompletionsSSELine(line)
}
