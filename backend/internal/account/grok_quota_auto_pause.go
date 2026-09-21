package account

import (
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/usageview"
)

// EvaluateGrokQuotaAutoPause 保留先解析快照、再取时的原判断顺序，不写入健康状态。
func EvaluateGrokQuotaAutoPause(account *Record, clock func() time.Time) (bool, QuotaAutoPauseDecision) {
	if account == nil || !account.IsGrok() || account.Type != capability.AccountTypeOAuth {
		return false, QuotaAutoPauseDecision{}
	}
	snapshot, err := GrokQuotaSnapshotFromExtra(account.Extra)
	if err != nil || snapshot == nil {
		return false, QuotaAutoPauseDecision{}
	}
	now := clock()
	if grokQuotaSnapshotStaleForPause(snapshot, now) {
		return false, QuotaAutoPauseDecision{}
	}
	if grokQuotaRetryAfterActive(snapshot, now) {
		return true, QuotaAutoPauseDecision{Window: "retry_after", Threshold: 1, Utilization: 1}
	}
	if paused, decision := GrokQuotaWindowAutoPause("requests", snapshot.Requests, now); paused {
		return true, decision
	}
	if paused, decision := GrokQuotaWindowAutoPause("tokens", snapshot.Tokens, now); paused {
		return true, decision
	}
	return false, QuotaAutoPauseDecision{}
}

func grokQuotaRetryAfterActive(snapshot *xai.QuotaSnapshot, now time.Time) bool {
	if snapshot == nil || snapshot.RetryAfterSeconds == nil || *snapshot.RetryAfterSeconds <= 0 {
		return false
	}
	if strings.TrimSpace(snapshot.UpdatedAt) == "" {
		return true
	}
	updatedAt, err := ParseUsageTime(snapshot.UpdatedAt)
	if err != nil {
		return true
	}
	retryAfterUntil := updatedAt.Add(time.Duration(*snapshot.RetryAfterSeconds) * time.Second)
	return now.Before(retryAfterUntil)
}

// GrokQuotaWindowAutoPause 根据观测窗口判断是否暂时停止调度。
func GrokQuotaWindowAutoPause(name string, window *xai.QuotaWindow, now time.Time) (bool, QuotaAutoPauseDecision) {
	if window == nil || window.Limit == nil || window.Remaining == nil || *window.Limit <= 0 {
		return false, QuotaAutoPauseDecision{}
	}
	if window.ResetUnix != nil && *window.ResetUnix > 0 && !now.Before(time.Unix(*window.ResetUnix, 0)) {
		return false, QuotaAutoPauseDecision{}
	}
	utilization := float64(*window.Limit-*window.Remaining) / float64(*window.Limit)
	if *window.Remaining <= 0 || utilization >= 1 {
		return true, QuotaAutoPauseDecision{Window: name, Threshold: 1, Utilization: utilization}
	}
	return false, QuotaAutoPauseDecision{}
}

func grokQuotaSnapshotStaleForPause(snapshot *xai.QuotaSnapshot, now time.Time) bool {
	if snapshot == nil || strings.TrimSpace(snapshot.UpdatedAt) == "" {
		return false
	}
	updatedAt, err := ParseUsageTime(snapshot.UpdatedAt)
	if err != nil {
		return false
	}
	return now.Sub(updatedAt) >= CodexAutoPauseStaleAfter
}
