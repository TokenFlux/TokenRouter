package account

import (
	"context"
	"time"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
)

// ApplyObservedRateLimit 保留先通知内存阻断、再持久化的原失败边界。
func (s *HealthService) ApplyObservedRateLimit(ctx context.Context, value *Record, reset time.Time) bool {
	s.notifyAccountSchedulingBlocked(value, reset, "429")
	if err := s.accountRepo.SetRateLimited(ctx, value.ID, reset); err != nil {
		s.options.Warn("rate_limit_set_failed", "account_id", value.ID, "error", err)
		return false
	}
	return true
}

// UpdateRejectedSessionWindow 保留独立五小时窗口写入，不扩大为新事务。
func (s *HealthService) UpdateRejectedSessionWindow(ctx context.Context, value *Record, end time.Time) {
	s.updateAnthropicRejectedSessionWindow(ctx, value, &QuotaWindowObservation{ResetAt: end})
}

// PersistCodexObservation 只记录响应观测；影子窗口仍由独立用量查询拥有。
func (s *HealthService) PersistCodexObservation(ctx context.Context, value *Record, snapshot *openaiprotocol.OpenAICodexUsageSnapshot) {
	if s == nil || s.accountRepo == nil || value == nil || value.IsShadow() || snapshot == nil {
		return
	}
	updates := BuildCodexUsageExtraUpdates(snapshot, s.options.Now())
	if len(updates) == 0 {
		return
	}
	if err := s.options.SessionWindows.UpdateExtra(ctx, value.ID, updates); err != nil {
		s.options.Warn("openai_codex_snapshot_persist_failed", "account_id", value.ID, "error", err)
	}
}
