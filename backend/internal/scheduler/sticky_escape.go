package scheduler

import "github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

// ShouldEscapeSticky 共用本次固化策略与唯一反馈，保留 TTFT 优先。
func ShouldEscapeSticky(stats *RuntimeStats, accountID int64, cfg policy.StickyEscapeConfig) (reason string, errorRate float64, ttft float64, shouldEscape bool) {
	if !cfg.Enabled || stats == nil || accountID <= 0 {
		return "", 0, 0, false
	}
	errorRate, ttft, hasTTFT := stats.Snapshot(accountID)
	if hasTTFT && ttft > cfg.TtftMs {
		return "ttft", errorRate, ttft, true
	}
	if errorRate > cfg.ErrorRate {
		return "error_rate", errorRate, ttft, true
	}
	return "", errorRate, ttft, false
}
