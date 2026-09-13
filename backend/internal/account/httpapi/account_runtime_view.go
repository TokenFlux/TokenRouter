// 本文件维护 httpapi 的所属能力；兼容入口复用唯一实现。
package httpapi

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	dto "github.com/TokenFlux/TokenRouter/internal/account/httpapi/dto"
)

// AccountWithConcurrency 保留管理端的实时并发与调度展示字段。
type AccountWithConcurrency struct {
	*dto.Account
	CurrentConcurrency int                          `json:"current_concurrency"`
	SchedulerScore     *AccountSchedulerScore       `json:"scheduler_score,omitempty"`
	SchedulerScores    []AccountSchedulerGroupScore `json:"scheduler_scores,omitempty"`
	// 以下字段仅对 Anthropic OAuth/SetupToken 账号有效，且仅在启用相应功能时返回
	CurrentWindowCost *float64 `json:"current_window_cost,omitempty"` // 当前窗口费用
	ActiveSessions    *int     `json:"active_sessions,omitempty"`     // 当前活跃会话数
	CurrentRPM        *int     `json:"current_rpm,omitempty"`         // 当前分钟 RPM 计数
}

// 调度展示值由账号管理用例拥有，JSON 保持原契约。
type AccountSchedulerScore = accountcore.AccountSchedulerScore
type AccountSchedulerGroupScore = accountcore.AccountSchedulerGroupScore

// CheckMixedChannelRequest represents check mixed channel risk request
type CheckMixedChannelRequest struct {
	Platform  string  `json:"platform" binding:"required"`
	GroupIDs  []int64 `json:"group_ids"`
	AccountID *int64  `json:"account_id"`
}
