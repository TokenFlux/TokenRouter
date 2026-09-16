package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
)

func OpenAITokenCacheKey(account *Account) string {
	return accountcore.OpenAITokenCacheKey(AccountRecordView(account))
}

func ClaudeTokenCacheKey(account *Account) string {
	return accountcore.ClaudeTokenCacheKey(AccountRecordView(account))
}

// ManagedRefreshCacheKey 使管理、后台与请求入口共用原平台锁命名空间，S09 随具体平台改绑。
func ManagedRefreshCacheKey(value *Account) string {
	if value.IsQoderCosy() {
		return QoderTokenCacheKey(value)
	}
	switch value.Platform {
	case PlatformOpenAI:
		return OpenAITokenCacheKey(value)
	case PlatformGemini:
		return GeminiTokenCacheKey(value)
	case PlatformAntigravity:
		return AntigravityTokenCacheKey(value)
	case PlatformGrok:
		return GrokTokenCacheKey(value)
	default:
		return ClaudeTokenCacheKey(value)
	}
}
