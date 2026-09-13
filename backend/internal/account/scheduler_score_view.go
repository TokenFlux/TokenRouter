// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

// AccountSchedulerScore 表示管理端展示的账号调度评分。
type AccountSchedulerScore struct {
	BaseScore             float64 `json:"base_score"`
	StickyScore           float64 `json:"sticky_score"`
	StickyScoreInfinity   bool    `json:"sticky_score_infinity"`
	StickyWeightedEnabled bool    `json:"sticky_weighted_enabled"`
}

// AccountSchedulerGroupScore 表示账号在指定分组中的调度评分。
type AccountSchedulerGroupScore struct {
	GroupID   *int64 `json:"group_id"`
	GroupName string `json:"group_name,omitempty"`
	AccountSchedulerScore
}
