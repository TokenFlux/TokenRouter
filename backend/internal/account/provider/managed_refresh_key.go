package provider

import "github.com/TokenFlux/TokenRouter/internal/account"

// ManagedRefreshCacheKey 保持管理、后台及请求入口共享原有平台命名空间。
func ManagedRefreshCacheKey(value *account.Record) string {
	if value.IsQoderCosy() {
		return account.QoderTokenCacheKey(value)
	}
	switch value.Platform {
	case account.PlatformOpenAI:
		return account.OpenAITokenCacheKey(value)
	case account.PlatformGemini:
		return GeminiTokenCacheKey(value)
	case account.PlatformAntigravity:
		return account.AntigravityTokenCacheKey(value)
	case account.PlatformGrok:
		return account.GrokTokenCacheKey(value)
	default:
		return account.ClaudeTokenCacheKey(value)
	}
}
