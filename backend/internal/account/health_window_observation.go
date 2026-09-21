package account

import (
	"context"
	"strconv"
	"time"
)

func shouldPersistAnthropicWindowLimit(account *Record, limit *QuotaWindowObservation, now time.Time) bool {
	if account == nil || limit == nil || !limit.ResetAt.After(now) {
		return false
	}
	if account.RateLimitResetAt == nil {
		return true
	}
	if !account.RateLimitResetAt.After(now) {
		return true
	}
	return limit.ResetAt.After(*account.RateLimitResetAt)
}

func (s *HealthService) ApplyExhaustedQuotaWindow(ctx context.Context, account *Record, limit *QuotaWindowObservation, now time.Time) bool {
	if s == nil || s.accountRepo == nil || account == nil {
		return false
	}
	if limit == nil {
		return false
	}
	if !shouldPersistAnthropicWindowLimit(account, limit, now) {
		s.options.Info("anthropic_window_rate_limit_kept",
			"account_id", account.ID,
			"window", limit.Window,
			"reset_at", limit.ResetAt,
			"existing_reset_at", account.RateLimitResetAt)
		s.updateAnthropicRejectedSessionWindow(ctx, account, limit)
		return true
	}

	s.notifyAccountSchedulingBlocked(account, limit.ResetAt, limit.Reason)
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, limit.ResetAt); err != nil {
		s.options.Warn("anthropic_window_rate_limit_set_failed",
			"account_id", account.ID,
			"window", limit.Window,
			"reset_at", limit.ResetAt,
			"error", err)
		return true
	}
	s.options.Info("anthropic_window_rate_limited",
		"account_id", account.ID,
		"window", limit.Window,
		"reset_at", limit.ResetAt,
		"reset_in", time.Until(limit.ResetAt).Truncate(time.Second))
	s.updateAnthropicRejectedSessionWindow(ctx, account, limit)
	return true
}

func (s *HealthService) updateAnthropicRejectedSessionWindow(ctx context.Context, account *Record, limit *QuotaWindowObservation) {
	if s == nil || s.accountRepo == nil || account == nil || limit == nil {
		return
	}
	// fork 需要维护 Anthropic 5h session window 状态，抢占 temp-unsched 后也不能跳过。
	windowEnd := limit.ResetAt
	if limit.FiveHourReset != nil {
		windowEnd = *limit.FiveHourReset
	}
	windowStart := windowEnd.Add(-5 * time.Hour)
	if err := s.options.SessionWindows.UpdateSessionWindow(ctx, account.ID, &windowStart, &windowEnd, "rejected"); err != nil {
		s.options.Warn("rate_limit_update_session_window_failed", "account_id", account.ID, "error", err)
	}
}

// ApplyFableQuotaWindow 在 7d_oi 耗尽时写入 Fable 家族模型限流。
// 返回 true 表示该窗口触发了 429，调用方不能继续把整个账号标记为限流。
func (s *HealthService) ApplyFableQuotaWindow(ctx context.Context, account *Record, limit *QuotaWindowObservation, passive map[string]any) bool {
	if s == nil || s.accountRepo == nil || account == nil {
		return false
	}
	if limit == nil {
		return false
	}
	// 429 响应头本身携带最新的窗口用量（7d_oi utilization=1.0）。限流期内
	// Fable 请求不再调度到该账号，若不在此处采样，7d F 进度条会冻结在
	// 限流前的旧值直到窗口重置。
	s.PersistPassiveUsage(ctx, account, passive)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, AnthropicFableRateLimitKey, limit.ResetAt, limit.Reason); err != nil {
		s.options.Warn("anthropic_fable_window_rate_limit_set_failed",
			"account_id", account.ID,
			"scope", AnthropicFableRateLimitKey,
			"reset_at", limit.ResetAt,
			"error", err)
		return true
	}
	s.options.Info("anthropic_fable_window_model_rate_limited",
		"account_id", account.ID,
		"scope", AnthropicFableRateLimitKey,
		"reset_at", limit.ResetAt,
		"reset_in", time.Until(limit.ResetAt).Truncate(time.Second))
	return true
}

// UpdateSessionWindow 根据成功响应观测更新五小时窗口，保留原独立写入顺序。
func (s *HealthService) UpdateSessionWindow(ctx context.Context, account *Record, observation SessionWindowObservation) {
	status := observation.Status
	if status == "" {
		return
	}

	// 检查是否需要初始化时间窗口
	// 对于 Setup Token 账号，首次成功请求时需要预测时间窗口
	var windowStart, windowEnd *time.Time
	needInitWindow := account.SessionWindowEnd == nil || s.options.Now().After(*account.SessionWindowEnd)

	// 优先使用响应头中的真实重置时间（比预测更准确）
	if resetStr := observation.Reset; resetStr != "" {
		if ts, err := strconv.ParseInt(resetStr, 10, 64); err == nil {
			// 检测可能的毫秒时间戳（秒级约为 1e9，毫秒约为 1e12）
			if ts > 1e11 {
				s.options.Warn("account_session_window_header_millis_detected", "account_id", account.ID, "raw_reset", resetStr)
				ts = ts / 1000
			}
			end := time.Unix(ts, 0)
			// 校验时间戳是否在合理范围内（不早于 5h 前，不晚于 7 天后）
			minAllowed := s.options.Now().Add(-5 * time.Hour)
			maxAllowed := s.options.Now().Add(7 * 24 * time.Hour)
			if end.Before(minAllowed) || end.After(maxAllowed) {
				s.options.Warn("account_session_window_header_out_of_range", "account_id", account.ID, "raw_reset", resetStr, "parsed_end", end)
			} else if needInitWindow || account.SessionWindowEnd == nil || !end.Equal(*account.SessionWindowEnd) {
				// 窗口需要初始化，或者真实重置时间与已存储的不同，则更新
				start := end.Add(-5 * time.Hour)
				windowStart = &start
				windowEnd = &end
				s.options.Info("account_session_window_from_header", "account_id", account.ID, "window_start", start, "window_end", end, "status", status)
			}
		} else {
			s.options.Warn("account_session_window_header_parse_failed", "account_id", account.ID, "raw_reset", resetStr, "error", err)
		}
	}

	// 回退：如果没有真实重置时间且需要初始化窗口，使用预测
	if windowEnd == nil && needInitWindow && (status == "allowed" || status == "allowed_warning") {
		now := s.options.Now()
		start := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
		end := start.Add(5 * time.Hour)
		windowStart = &start
		windowEnd = &end
		s.options.Info("account_session_window_initialized", "account_id", account.ID, "window_start", start, "window_end", end, "status", status)
	}

	// 窗口重置时清除旧的 utilization 和被动采样数据，避免残留上个窗口的数据
	if windowEnd != nil && needInitWindow {
		_ = s.options.SessionWindows.UpdateExtra(ctx, account.ID, map[string]any{
			"session_window_utilization":      nil,
			"passive_usage_7d_utilization":    nil,
			"passive_usage_7d_reset":          nil,
			"passive_usage_7d_oi_utilization": nil,
			"passive_usage_7d_oi_reset":       nil,
			"passive_usage_sampled_at":        nil,
		})
	}

	if err := s.options.SessionWindows.UpdateSessionWindow(ctx, account.ID, windowStart, windowEnd, status); err != nil {
		s.options.Warn("session_window_update_failed", "account_id", account.ID, "error", err)
	}

	// 被动采样：从响应头收集 5h + 7d + 7d_oi utilization，合并为一次 DB 写入
	s.PersistPassiveUsage(ctx, account, observation.Passive)

	// 如果状态为allowed且之前有限流，说明窗口已重置，清除限流状态
	if status == "allowed" && account.IsRateLimited() && s.options.ClearWindowRateLimit != nil {
		if err := s.options.ClearWindowRateLimit(ctx, account.ID); err != nil {
			s.options.Warn("rate_limit_clear_failed", "account_id", account.ID, "error", err)
		}
	}
}

// SessionWindowStore 只提供观测字段写入，不接收业务配置或资金快照。
type SessionWindowStore interface {
	UpdateSessionWindow(context.Context, int64, *time.Time, *time.Time, string) error
	UpdateExtra(context.Context, int64, map[string]any) error
}

// QuotaWindowObservation 是供应商 Adapter 提供的限流窗口观测。
type QuotaWindowObservation struct {
	Window        string
	ResetAt       time.Time
	FiveHourReset *time.Time
	Reason        string
}

// SessionWindowObservation 保留原响应字段与解析后的被动统计，不携带 HTTP 状态。
type SessionWindowObservation struct {
	Status  string
	Reset   string
	Passive map[string]any
}

// PersistPassiveUsage 保留无字段不写入及采样时取时的语义。
func (s *HealthService) PersistPassiveUsage(ctx context.Context, value *Record, fields map[string]any) {
	if len(fields) == 0 {
		return
	}
	updates := make(map[string]any, len(fields)+1)
	for key, field := range fields {
		updates[key] = field
	}
	updates["passive_usage_sampled_at"] = s.options.Now().UTC().Format(time.RFC3339)
	if err := s.options.SessionWindows.UpdateExtra(ctx, value.ID, updates); err != nil {
		s.options.Warn("passive_usage_update_failed", "account_id", value.ID, "error", err)
	}
}
