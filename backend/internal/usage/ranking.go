package usage

import (
	"strconv"
	"strings"
)

// UsageRankingSortBy 表示用户侧用量排行的排名指标。
type UsageRankingSortBy string

const (
	UsageRankingSortByTotalTokens UsageRankingSortBy = "total_tokens"
	UsageRankingSortByRequests    UsageRankingSortBy = "requests"
	UsageRankingSortByActualCost  UsageRankingSortBy = "actual_cost"
)

// UsageRankingSettings 是用户侧排行读取和展示共用的运行时配置。
type UsageRankingSettings struct {
	Enabled         bool
	SortBy          UsageRankingSortBy
	ShowTotalTokens bool
	ShowRequests    bool
	ShowActualCost  bool
	Limit           int
}

func IsValidUsageRankingSortBy(value string) bool {
	switch UsageRankingSortBy(strings.TrimSpace(value)) {
	case UsageRankingSortByTotalTokens, UsageRankingSortByRequests, UsageRankingSortByActualCost:
		return true
	default:
		return false
	}
}
func NormalizeUsageRankingSortByInternal(value string) UsageRankingSortBy {
	if IsValidUsageRankingSortBy(value) {
		return UsageRankingSortBy(strings.TrimSpace(value))
	}
	return UsageRankingSortByTotalTokens
}

// NormalizeUsageRankingSortBy 将历史或非法值回退到总 Token 排序。
func NormalizeUsageRankingSortBy(value string) UsageRankingSortBy {
	return NormalizeUsageRankingSortByInternal(value)
}

// NormalizeUsageRankingSettings 保证排序依据始终可见，避免用户无法理解排行名次。
func NormalizeUsageRankingSettings(settings UsageRankingSettings) UsageRankingSettings {
	settings.SortBy = NormalizeUsageRankingSortByInternal(string(settings.SortBy))
	settings.Limit = NormalizeUsageRankingLimit(settings.Limit)
	switch settings.SortBy {
	case UsageRankingSortByRequests:
		settings.ShowRequests = true
	case UsageRankingSortByActualCost:
		settings.ShowActualCost = true
	default:
		settings.ShowTotalTokens = true
	}
	return settings
}
func NormalizeUsageRankingLimit(value int) int {
	if value <= 0 {
		return DefaultUsageRankingLimit
	}
	if value > MaxUsageRankingLimit {
		return MaxUsageRankingLimit
	}
	return value
}
func NormalizeUsageRankingLimitString(raw string) int {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return DefaultUsageRankingLimit
	}
	return NormalizeUsageRankingLimit(value)
}

const DefaultUsageRankingLimit = 20

const MaxUsageRankingLimit = 100
