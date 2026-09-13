package scheduler

import (
	"context"
	"time"
)

// SessionBinding 只包含本次调度允许管理的空闲会话参数。
type SessionBinding struct {
	AccountID   int64
	SessionID   string
	Enabled     bool
	Limit       int
	IdleTimeout time.Duration
}

// RegisterSession 保留已有会话刷新、容量限制与 Redis 错误放行。
func RegisterSession(ctx context.Context, cache SessionLimitCache, input SessionBinding) bool {
	if !input.Enabled || input.Limit <= 0 || input.SessionID == "" || cache == nil {
		return true
	}
	allowed, err := cache.RegisterSession(ctx, input.AccountID, input.SessionID, input.Limit, input.IdleTimeout)
	return err != nil || allowed
}

// FinishSession 成功及可结算部分结果保留空闲窗口，其余完成结果立即注销。
func FinishSession(ctx context.Context, cache SessionLimitCache, input SessionBinding, outcome AttemptOutcome, diagnostics Diagnostics) {
	if outcome.Served || !input.Enabled || input.Limit <= 0 || input.SessionID == "" || cache == nil {
		return
	}
	if err := cache.UnregisterSession(ctx, input.AccountID, input.SessionID); err != nil {
		diagnostics.event("debug", "session_limit.release_failed", "account_id", input.AccountID, "error", err)
	}
}
