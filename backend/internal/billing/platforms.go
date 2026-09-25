package billing

import (
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// AllowedQuotaPlatforms 是允许设置 user × platform quota 的平台列表（单一权威来源）。
// ent/schema/user_platform_quota.go 的 Validate 函数独立维护（构建期约束），
// 若新增平台需同步修改该 schema。
var AllowedQuotaPlatforms = []string{
	capability.PlatformAnthropic,
	capability.PlatformOpenAI,
	capability.PlatformGemini,
	capability.PlatformAntigravity,
	capability.PlatformQoder,
	capability.PlatformGrok,
	capability.PlatformKimi,
	capability.PlatformZhipu,
	capability.PlatformDeepseek,
}

// IsAllowedQuotaPlatform 报告 s 是否为合法的 quota platform 标识。
func IsAllowedQuotaPlatform(s string) bool {
	for _, p := range AllowedQuotaPlatforms {
		if p == s {
			return true
		}
	}
	return false
}
