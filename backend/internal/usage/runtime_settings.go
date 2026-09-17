package usage

import (
	"context"
	"fmt"
	"log/slog"
)

// 用量展示设置沿用原存储键。
const (
	SettingKeyAllowUserViewErrorRequests  = "allow_user_view_error_requests"
	SettingKeyUsageRankingEnabled         = "usage_ranking_enabled"
	SettingKeyUsageRankingLimit           = "usage_ranking_limit"
	SettingKeyUsageRankingShowActualCost  = "usage_ranking_show_actual_cost"
	SettingKeyUsageRankingShowRequests    = "usage_ranking_show_requests"
	SettingKeyUsageRankingShowTotalTokens = "usage_ranking_show_total_tokens"
	SettingKeyUsageRankingSortBy          = "usage_ranking_sort_by"
)

// RuntimeSettingsStore 保留批量查询接口，避免拆分后按字段回源。
type RuntimeSettingsStore interface {
	GetMultiple(context.Context, []string) (map[string]string, error)
}

// RuntimeSettings 解释排行和用户错误展示，不引入第二个查询缓存。
type RuntimeSettings struct{ settingRepo RuntimeSettingsStore }

// NewRuntimeSettings 构造无回源副作用的读取器。
func NewRuntimeSettings(repo RuntimeSettingsStore) *RuntimeSettings {
	return &RuntimeSettings{settingRepo: repo}
}

// DefaultRankingSettings 保留现有排行默认与字段规则。
func DefaultRankingSettings() UsageRankingSettings {
	return UsageRankingSettings{
		Enabled:         true,
		SortBy:          UsageRankingSortByTotalTokens,
		ShowTotalTokens: true,
		ShowRequests:    true,
		ShowActualCost:  true,
		Limit:           DefaultUsageRankingLimit,
	}
}

// ParseRankingSettings 保留现有排行默认与字段规则。
func ParseRankingSettings(values map[string]string) UsageRankingSettings {
	settings := DefaultRankingSettings()
	if values == nil {
		return settings
	}
	settings.Enabled = values[SettingKeyUsageRankingEnabled] != "false"
	settings.SortBy = NormalizeUsageRankingSortByInternal(values[SettingKeyUsageRankingSortBy])
	settings.ShowTotalTokens = values[SettingKeyUsageRankingShowTotalTokens] != "false"
	settings.ShowRequests = values[SettingKeyUsageRankingShowRequests] != "false"
	settings.ShowActualCost = values[SettingKeyUsageRankingShowActualCost] != "false"
	settings.Limit = NormalizeUsageRankingLimitString(values[SettingKeyUsageRankingLimit])
	return NormalizeUsageRankingSettings(settings)
}

// GetUsageRankingSettings 只使用用量展示需要的设置投影。
func (s *RuntimeSettings) GetUsageRankingSettings(ctx context.Context) (UsageRankingSettings, error) {
	if s == nil || s.settingRepo == nil {
		return DefaultRankingSettings(), nil
	}
	values, err := s.settingRepo.GetMultiple(ctx, []string{
		SettingKeyUsageRankingLimit,
		SettingKeyUsageRankingEnabled,
		SettingKeyUsageRankingSortBy,
		SettingKeyUsageRankingShowTotalTokens,
		SettingKeyUsageRankingShowRequests,
		SettingKeyUsageRankingShowActualCost,
	})
	if err != nil {
		return UsageRankingSettings{}, fmt.Errorf("get usage ranking settings: %w", err)
	}
	return ParseRankingSettings(values), nil
}

// GetUsageRankingLimit 只使用用量展示需要的设置投影。
func (s *RuntimeSettings) GetUsageRankingLimit(ctx context.Context) int {
	settings, err := s.GetUsageRankingSettings(ctx)
	if err != nil {
		return DefaultUsageRankingLimit
	}
	return settings.Limit
}

// IsUserErrorViewAllowed 只使用用量展示需要的设置投影。
func (s *RuntimeSettings) IsUserErrorViewAllowed(ctx context.Context) bool {
	vals, err := s.settingRepo.GetMultiple(ctx, []string{SettingKeyAllowUserViewErrorRequests})
	if err != nil {
		slog.Warn("failed to get allow_user_view_error_requests setting, defaulting to false", "error", err)
		return false
	}
	return vals[SettingKeyAllowUserViewErrorRequests] == "true"
}
