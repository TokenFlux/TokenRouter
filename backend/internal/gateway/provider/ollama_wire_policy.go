package provider

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
	"go.uber.org/zap"
)

func IsOllamaCloudRawChatCompletionsAccount(account *ExecutionAccount) bool {
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
	if AccountHasOllamaCloudUsageExtra(account) {
		return true
	}
	if account.Record.Credentials == nil {
		return false
	}
	baseURL, _ := account.Record.Credentials["base_url"].(string)
	return egress.IsOllamaCloudBaseURL(baseURL)
}

func AccountHasOllamaCloudUsageExtra(account *ExecutionAccount) bool {
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

func ApplyOllamaCloudRawChatCompletionsRequest(account *ExecutionAccount, body []byte) []byte {
	if !IsOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	body = ollama.NormalizeOllamaCloudChatCompletionsRequest(body)
	return ClampOllamaCloudMaxTokens(account, body)
}

func ApplyOllamaCloudRawChatCompletionsResponse(account *ExecutionAccount, body []byte) []byte {
	if !IsOllamaCloudRawChatCompletionsAccount(account) || len(body) == 0 {
		return body
	}
	return ollama.NormalizeOllamaCloudChatCompletionsResponseJSON(body)
}

func ApplyOllamaCloudRawChatCompletionsSSELine(account *ExecutionAccount, line string) string {
	if !IsOllamaCloudRawChatCompletionsAccount(account) || line == "" {
		return line
	}
	return ollama.NormalizeOllamaCloudChatCompletionsSSELine(line)
}

func OllamaCloudMaxTokensCap(account *ExecutionAccount) int64 {
	if account == nil {
		return ollama.MaxTokensCap(nil, false)
	}
	value, ok := account.Record.Extra[ollama.MaxTokensCapExtraKey]
	return ollama.MaxTokensCap(value, ok)
}

func ClampOllamaCloudMaxTokens(account *ExecutionAccount, body []byte) []byte {
	cap := OllamaCloudMaxTokensCap(account)
	out, clamped := ollama.ClampMaxTokens(body, cap)
	if clamped && account != nil {
		logging.L().Debug("openai chat_completions raw: clamped max_tokens for ollama cloud account", zap.Int64("account_id", account.Record.ID), zap.Int64("cap", cap))
	}
	return out
}
