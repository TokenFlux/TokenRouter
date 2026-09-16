// 账号配置投影和原日志时机留在网关适配，报文规则由 Ollama 唯一实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/TokenFlux/TokenRouter/internal/upstream/ollama"
	"go.uber.org/zap"
)

const OllamaCloudMaxTokensCapExtraKey = ollama.MaxTokensCapExtraKey

func ollamaCloudMaxTokensCap(account *Account) int64 {
	if account == nil {
		return ollama.MaxTokensCap(nil, false)
	}
	value, ok := account.Extra[OllamaCloudMaxTokensCapExtraKey]
	return ollama.MaxTokensCap(value, ok)
}
func clampOllamaCloudMaxTokens(account *Account, body []byte) []byte {
	cap := ollamaCloudMaxTokensCap(account)
	out, clamped := ollama.ClampMaxTokens(body, cap)
	if clamped && account != nil {
		logger.L().Debug("openai chat_completions raw: clamped max_tokens for ollama cloud account", zap.Int64("account_id", account.ID), zap.Int64("cap", cap))
	}
	return out
}
