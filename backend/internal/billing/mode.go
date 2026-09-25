package billing

import (
	strings "strings"
)

// API Key 结算模式常量。auto 保持存量 Key 的订阅优先、余额补足行为。
const (
	APIKeyBillingModeAuto         = "auto"
	APIKeyBillingModeSubscription = "subscription"
	APIKeyBillingModeBalance      = "balance"
)

// NormalizeAPIKeyBillingMode 校验并规范化 API Key 结算模式。
// 空值兼容旧客户端，按自动选择处理。
func NormalizeAPIKeyBillingMode(value string) (string, bool) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	switch normalized {
	case "", APIKeyBillingModeAuto:
		return APIKeyBillingModeAuto, true
	case APIKeyBillingModeSubscription, APIKeyBillingModeBalance:
		return normalized, true
	default:
		return "", false
	}
}
