// 本文件拥有无法解析上游窗口时的账号回避规则，原 Header 解析留供应商适配。
package account

import (
	"context"
	"time"
)

const (
	DefaultRateLimit429CooldownSeconds = 5
	MaxRateLimit429CooldownSeconds     = 7200
)

func (s *HealthService) Apply429Fallback(ctx context.Context, account *Record, reason string) {
	cooldown, enabled := s.Fallback429Cooldown(ctx, account)
	if !enabled {
		s.options.Info("rate_limit_429_fallback_ignored", "account_id", account.ID, "platform", account.Platform, "reason", reason)
		return
	}

	resetAt := s.options.Now().Add(cooldown)
	s.options.Warn("rate_limit_429_fallback_used", "account_id", account.ID, "platform", account.Platform, "reason", reason, "using_default", cooldown.String())
	s.notifyAccountSchedulingBlocked(account, resetAt, "429_fallback")
	if err := s.accountRepo.SetRateLimited(ctx, account.ID, resetAt); err != nil {
		s.options.Warn("rate_limit_set_failed", "account_id", account.ID, "error", err)
	}
}
func (s *HealthService) Fallback429Cooldown(ctx context.Context, account *Record) (time.Duration, bool) {
	if s.options.RateLimit429Settings != nil {
		settings, err := s.options.RateLimit429Settings(ctx)
		if err == nil && settings != nil {
			if !settings.Enabled {
				return 0, false
			}
			seconds := ClampRateLimit429CooldownSeconds(settings.CooldownSeconds)
			return time.Duration(seconds) * time.Second, true
		}
		s.options.Warn("rate_limit_429_settings_read_failed", "account_id", account.ID, "error", err)
	}

	seconds := DefaultRateLimit429CooldownSeconds
	seconds = ClampRateLimit429CooldownSeconds(seconds)
	return time.Duration(seconds) * time.Second, true
}
func ClampRateLimit429CooldownSeconds(seconds int) int {
	if seconds < 1 {
		return 1
	}
	if seconds > MaxRateLimit429CooldownSeconds {
		return MaxRateLimit429CooldownSeconds
	}
	return seconds
}

// RateLimit429CooldownSettings 429默认回避配置
type RateLimit429CooldownSettings struct {
	// Enabled 是否在无法解析上游重置时间时应用默认429回避
	Enabled bool `json:"enabled"`
	// CooldownSeconds 默认回避时长（秒）
	CooldownSeconds int `json:"cooldown_seconds"`
}

// DefaultRateLimit429CooldownSettings 返回默认的429回避配置（启用，5秒）
func DefaultRateLimit429CooldownSettings() *RateLimit429CooldownSettings {
	return &RateLimit429CooldownSettings{
		Enabled:         true,
		CooldownSeconds: 5,
	}
}
