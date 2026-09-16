package protocol

// ApplyCacheTTLOverride 将所有 cache creation tokens 归入指定的 TTL 类型。
// target 仅精确匹配 "1h"，其它值沿用 5m；返回值只报告 TTL 重分类，
// 聚合值补入缺失的 5m 明细本身不把返回值改为 true。
func ApplyCacheTTLOverride(usage *TokenUsage, target string) bool {
	// 兼容回退： 如果只有聚合字段但无 5m/1h 明细，将聚合字段归入 5m 默认类别
	if usage.CacheCreation5mTokens == 0 && usage.CacheCreation1hTokens == 0 && usage.CacheCreationInputTokens > 0 {
		usage.CacheCreation5mTokens = usage.CacheCreationInputTokens
	}

	total := usage.CacheCreation5mTokens + usage.CacheCreation1hTokens
	if total == 0 {
		return false
	}
	switch target {
	case "1h":
		if usage.CacheCreation1hTokens == total {
			return false // 已经全是 1h
		}
		usage.CacheCreation1hTokens = total
		usage.CacheCreation5mTokens = 0
	default: // "5m"
		if usage.CacheCreation5mTokens == total {
			return false // 已经全是 5m
		}
		usage.CacheCreation5mTokens = total
		usage.CacheCreation1hTokens = 0
	}
	return true
}
