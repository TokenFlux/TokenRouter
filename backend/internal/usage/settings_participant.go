package usage

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// RankingSettingsUpdate 保留管理请求的字段存在性。
type RankingSettingsUpdate struct {
	Limit           int
	Enabled         *bool
	SortBy          *string
	ShowTotalTokens *bool
	ShowRequests    *bool
	ShowActualCost  *bool
}

// ResolveRankingSettings 保留旧客户端省略时回退以及显式排序必须可见的规则。
func ResolveRankingSettings(current UsageRankingSettings, input RankingSettingsUpdate) (UsageRankingSettings, error) {
	if input.Limit > 0 {
		current.Limit = input.Limit
	}
	if current.Limit > MaxUsageRankingLimit {
		current.Limit = MaxUsageRankingLimit
	}
	if input.Enabled != nil {
		current.Enabled = *input.Enabled
	}
	if input.SortBy != nil {
		if !IsValidUsageRankingSortBy(*input.SortBy) {
			return UsageRankingSettings{}, rankingSortError{}
		}
		current.SortBy = UsageRankingSortBy(*input.SortBy)
	}
	if input.ShowTotalTokens != nil {
		current.ShowTotalTokens = *input.ShowTotalTokens
	}
	if input.ShowRequests != nil {
		current.ShowRequests = *input.ShowRequests
	}
	if input.ShowActualCost != nil {
		current.ShowActualCost = *input.ShowActualCost
	}
	return NormalizeUsageRankingSettings(current), nil
}

// PrepareRankingSettings 只生成原格式的持久值，供综合与旧直接入口复用。
func PrepareRankingSettings(value UsageRankingSettings) (UsageRankingSettings, map[string]string) {
	value = NormalizeUsageRankingSettings(value)
	return value, map[string]string{
		SettingKeyUsageRankingLimit:           strconv.Itoa(value.Limit),
		SettingKeyUsageRankingEnabled:         strconv.FormatBool(value.Enabled),
		SettingKeyUsageRankingSortBy:          string(value.SortBy),
		SettingKeyUsageRankingShowTotalTokens: strconv.FormatBool(value.ShowTotalTokens),
		SettingKeyUsageRankingShowRequests:    strconv.FormatBool(value.ShowRequests),
		SettingKeyUsageRankingShowActualCost:  strconv.FormatBool(value.ShowActualCost),
	}
}

// SettingsParticipant 接收已经按原存在性合并的展示投影；不修改分析或资金事实。
func SettingsParticipant() settings.Participant {
	fields := []string{"usage_ranking_limit", "usage_ranking_enabled", "usage_ranking_sort_by", "usage_ranking_show_total_tokens", "usage_ranking_show_requests", "usage_ranking_show_actual_cost", "allow_user_view_error_requests"}
	keys := []string{SettingKeyUsageRankingLimit, SettingKeyUsageRankingEnabled, SettingKeyUsageRankingSortBy, SettingKeyUsageRankingShowTotalTokens, SettingKeyUsageRankingShowRequests, SettingKeyUsageRankingShowActualCost, SettingKeyAllowUserViewErrorRequests}
	return settings.Participant{Module: "usage", Fields: fields, Keys: keys, Prepare: func(_ context.Context, input settings.Fields, _ map[string]string) (settings.PreparedChange, error) {
		if len(input) == 0 {
			return settings.PreparedChange{}, nil
		}
		var projected struct {
			Limit           int                `json:"usage_ranking_limit"`
			Enabled         bool               `json:"usage_ranking_enabled"`
			SortBy          UsageRankingSortBy `json:"usage_ranking_sort_by"`
			ShowTotalTokens bool               `json:"usage_ranking_show_total_tokens"`
			ShowRequests    bool               `json:"usage_ranking_show_requests"`
			ShowActualCost  bool               `json:"usage_ranking_show_actual_cost"`
			ErrorsAllowed   bool               `json:"allow_user_view_error_requests"`
		}
		raw, err := json.Marshal(input)
		if err != nil {
			return settings.PreparedChange{}, err
		}
		if err = json.Unmarshal(raw, &projected); err != nil {
			return settings.PreparedChange{}, err
		}
		_, values := PrepareRankingSettings(UsageRankingSettings{Limit: projected.Limit, Enabled: projected.Enabled, SortBy: projected.SortBy, ShowTotalTokens: projected.ShowTotalTokens, ShowRequests: projected.ShowRequests, ShowActualCost: projected.ShowActualCost})
		values[SettingKeyAllowUserViewErrorRequests] = strconv.FormatBool(projected.ErrorsAllowed)
		for i, field := range fields {
			if _, ok := input[field]; !ok {
				delete(values, keys[i])
			}
		}
		return settings.PreparedChange{Values: values}, nil
	}}
}

// rankingSortError 保留历史 HTTP 文案，错误身份由领域校验表达。
type rankingSortError struct{}

func (rankingSortError) Error() string { return "Invalid usage ranking sort field" }
