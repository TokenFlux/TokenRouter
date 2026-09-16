// 请求侧刷新政策保持各平台既有失败、锁等待与 TTL 差异。
package account

import "time"

// ProviderRefreshErrorAction 定义 provider 在刷新失败时的处理动作。
type ProviderRefreshErrorAction int

const (
	// ProviderRefreshErrorReturn 失败即返回错误（不降级旧 token）。
	ProviderRefreshErrorReturn ProviderRefreshErrorAction = iota
	// ProviderRefreshErrorUseExistingToken 失败后继续使用现有 token。
	ProviderRefreshErrorUseExistingToken
)

// ProviderLockHeldAction 定义 provider 在刷新锁被占用时的处理动作。
type ProviderLockHeldAction int

const (
	// ProviderLockHeldUseExistingToken 直接使用现有 token。
	ProviderLockHeldUseExistingToken ProviderLockHeldAction = iota
	// ProviderLockHeldWaitForCache 等待后重试缓存读取。
	ProviderLockHeldWaitForCache
)

// ProviderRefreshPolicy 描述 provider 的平台差异策略。
type ProviderRefreshPolicy struct {
	OnRefreshError ProviderRefreshErrorAction
	OnLockHeld     ProviderLockHeldAction
	FailureTTL     time.Duration
}

func ClaudeProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorUseExistingToken,
		OnLockHeld:     ProviderLockHeldWaitForCache,
		FailureTTL:     time.Minute,
	}
}
func OpenAIProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorUseExistingToken,
		OnLockHeld:     ProviderLockHeldWaitForCache,
		FailureTTL:     time.Minute,
	}
}
func GeminiProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorReturn,
		OnLockHeld:     ProviderLockHeldUseExistingToken,
		FailureTTL:     0,
	}
}
func AntigravityProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorReturn,
		OnLockHeld:     ProviderLockHeldUseExistingToken,
		FailureTTL:     0,
	}
}
func GrokProviderRefreshPolicy() ProviderRefreshPolicy {
	return ProviderRefreshPolicy{
		OnRefreshError: ProviderRefreshErrorReturn,
		OnLockHeld:     ProviderLockHeldWaitForCache,
		FailureTTL:     0,
	}
}
