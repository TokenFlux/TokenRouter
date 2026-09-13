// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	time "time"
)

// GroupAccountCapacityRow 是容量汇总所需的轻量账号投影。
type GroupAccountCapacityRow struct {
	GroupID             int64
	AccountID           int64
	Platform            string
	Concurrency         int
	Extra               map[string]any
	SessionWindowStart  *time.Time
	SessionWindowEnd    *time.Time
	SessionWindowStatus string
}

// CapacitySnapshot 不包含凭据或原始 Extra，供路由聚合独立消费。
type CapacitySnapshot struct {
	ID                        int64
	Concurrency               int
	MaxSessions               int
	SessionIdleTimeoutMinutes int
	BaseRPM                   int
	QuotaAutoPaused           bool
}

func ProjectCapacity(id int64, config RuntimeConfig, quotaAutoPaused bool) CapacitySnapshot {
	return CapacitySnapshot{ID: id, Concurrency: config.Concurrency, MaxSessions: config.GetMaxSessions(), SessionIdleTimeoutMinutes: config.GetSessionIdleTimeoutMinutes(), BaseRPM: config.GetBaseRPM(), QuotaAutoPaused: quotaAutoPaused}
}

// ProjectObservedCapacity 组合账号运行参数与纯阈值结果；调用方保留原逐行取时点。
func ProjectObservedCapacity(row GroupAccountCapacityRow, settings QuotaAutoPauseSettings, now time.Time) CapacitySnapshot {
	paused, _ := EvaluateQuotaAutoPause(row.Platform, row.Extra, settings, now)
	return ProjectCapacity(row.AccountID, RuntimeConfig{Extra: row.Extra, Concurrency: row.Concurrency, SessionWindowStart: row.SessionWindowStart, SessionWindowEnd: row.SessionWindowEnd}, paused)
}
