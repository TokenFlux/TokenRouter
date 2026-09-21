package account

import "time"

// ModelRateLimitActive 按原读取顺序检查窗口，使用账号记录的运行时钟。
func (a *Record) ModelRateLimitActive(key string) bool {
	reset := a.ModelRateLimitResetAt(key)
	return reset != nil && a.now().Before(*reset)
}

// ModelRateLimitRemaining 保留非正数归零的原规则。
func (a *Record) ModelRateLimitRemaining(key string) time.Duration {
	reset := a.ModelRateLimitResetAt(key)
	if reset == nil {
		return 0
	}
	remaining := reset.Sub(a.now())
	if remaining > 0 {
		return remaining
	}
	return 0
}

// ModelRateLimitAllows 仅判断已选模型的资金窗口资格，不重新判断账号状态。
func (a *Record) ModelRateLimitAllows(keys []string) bool {
	if a == nil {
		return false
	}
	for _, key := range keys {
		if a.ModelRateLimitActive(key) {
			if a.Platform == PlatformAntigravity && a.IsOveragesEnabled() && !a.ModelRateLimitActive(CreditsExhaustedKey) {
				return true
			}
			return false
		}
	}
	return true
}
