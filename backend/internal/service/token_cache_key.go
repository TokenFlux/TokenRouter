package service

import "strconv"

// OpenAITokenCacheKey 生成 OpenAI OAuth 账号的缓存键
// 格式: "openai:account:{account_id}"
func OpenAITokenCacheKey(account *Account) string {
	return "openai:account:" + strconv.FormatInt(account.ID, 10)
}

// ClaudeTokenCacheKey 生成 Claude (Anthropic) OAuth 账号的缓存键
// 格式: "claude:account:{account_id}"
func ClaudeTokenCacheKey(account *Account) string {
	return "claude:account:" + strconv.FormatInt(account.ID, 10)
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
