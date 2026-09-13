//go:build unit

package service

import account "github.com/TokenFlux/TokenRouter/internal/account"

// 保留旧等级别名测试入口，不复制规范化规则。
func canonicalGeminiTierID(raw string) string { return account.CanonicalGeminiTierID(raw) }
