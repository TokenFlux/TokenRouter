package provider

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// HasBillableGrokChatUsage 只检查聊天结算实际使用的聚合 token 桶。
// 明细字段本身不能证明响应可安全结算，至少一个聚合桶必须为正数。
func HasBillableGrokChatUsage(usage openai.ForwardUsage) bool {
	return usage.InputTokens > 0 ||
		usage.OutputTokens > 0 ||
		usage.CacheCreationInputTokens > 0 ||
		usage.CacheReadInputTokens > 0
}

// RequiresBillableGrokChatUsage 根据实际账号平台和最终模型身份识别 Grok 流量。
// Grok 可由通用 OpenAI 兼容账号承载，因此不能只检查 account.Platform；同时不使用
// 未映射的客户端模型，避免 Grok 命名别名映射到非 Grok 上游时被误判。
func RequiresBillableGrokChatUsage(account *ExecutionAccount, models ...string) bool {
	if account != nil && account.Record.Platform == capability.PlatformGrok {
		return true
	}
	for _, model := range models {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if separator := strings.LastIndex(normalized, "/"); separator >= 0 {
			normalized = strings.TrimSpace(normalized[separator+1:])
		}
		if normalized == "grok" || strings.HasPrefix(normalized, "grok-") {
			return true
		}
	}
	return false
}
