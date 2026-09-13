// 本文件拥有累计禁止响应的冷却、计数和升级；HTML/供应商错误解析由外层提供。
package account

import (
	"context"
	"fmt"
	"time"
)

const (
	OpenAI403CooldownMinutesDefault      = 10
	OpenAI403DisableThresholdDefault     = 3
	OpenAI403CounterWindowMinutesDefault = 180
)

func (s *HealthService) ApplyForbidden(ctx context.Context, account *Record, msg string) (shouldDisable bool) {
	settings := s.ForbiddenSettings(ctx, account.ID)
	if !settings.Enabled {
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	}

	thresholdCount := settings.ThresholdCount
	if thresholdCount <= 0 {
		thresholdCount = OpenAI403DisableThresholdDefault
	}
	thresholdWindowMinutes := settings.ThresholdWindowMinutes
	if thresholdWindowMinutes <= 0 {
		thresholdWindowMinutes = OpenAI403CounterWindowMinutesDefault
	}

	var count int64
	if settings.ErrorOnThresholdEnabled {
		if s.options.ForbiddenCounter == nil {
			s.ApplyAuthenticationFailure(ctx, account, msg)
			return true
		}

		var err error
		count, err = s.options.ForbiddenCounter.IncrementOpenAI403Count(ctx, account.ID, thresholdWindowMinutes)
		if err != nil {
			s.options.Warn("openai_403_increment_failed", "account_id", account.ID, "error", err)
			s.ApplyAuthenticationFailure(ctx, account, msg)
			return true
		}

		if count >= int64(thresholdCount) {
			msg = fmt.Sprintf("%s | consecutive_403=%d/%d", msg, count, thresholdCount)
			s.ApplyAuthenticationFailure(ctx, account, msg)
			return true
		}
	} else {
		s.ResetForbiddenCounter(ctx, account.ID)
	}

	cooldownMinutes := settings.CooldownMinutes
	if cooldownMinutes <= 0 {
		cooldownMinutes = OpenAI403CooldownMinutesDefault
	}

	until := s.options.Now().Add(time.Duration(cooldownMinutes) * time.Minute)
	platformLabel := "OpenAI"
	if account.IsCNProvider() {
		platformLabel = account.Platform
	}
	reason := fmt.Sprintf("%s 403 temporary cooldown: %s", platformLabel, msg)
	if settings.ErrorOnThresholdEnabled {
		reason = fmt.Sprintf("%s 403 temporary cooldown (%d/%d): %s", platformLabel, count, thresholdCount, msg)
	}
	s.notifyAccountSchedulingBlocked(account, until, "openai_403_temp")
	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, until, reason); err != nil {
		s.options.Warn("openai_403_set_temp_unschedulable_failed", "account_id", account.ID, "error", err)
		s.ApplyAuthenticationFailure(ctx, account, msg)
		return true
	}

	s.options.Warn(
		"openai_403_temp_unschedulable",
		"account_id", account.ID,
		"until", until,
		"count", count,
		"threshold", thresholdCount,
		"threshold_window_minutes", thresholdWindowMinutes,
		"error_on_threshold_enabled", settings.ErrorOnThresholdEnabled,
	)
	return true
}
func (s *HealthService) ForbiddenSettings(ctx context.Context, accountID int64) *OpenAI403CooldownSettings {
	settings := DefaultOpenAI403CooldownSettings()
	if settings.CooldownMinutes <= 0 {
		settings.CooldownMinutes = OpenAI403CooldownMinutesDefault
	}
	if s == nil || s.options.ForbiddenSettings == nil {
		return settings
	}

	loaded, err := s.options.ForbiddenSettings(ctx)
	if err != nil {
		s.options.Warn("openai_403_settings_read_failed", "account_id", accountID, "error", err)
		return settings
	}
	if loaded == nil {
		return settings
	}
	if loaded.CooldownMinutes <= 0 {
		loaded.CooldownMinutes = settings.CooldownMinutes
	}
	return loaded
}
func (s *HealthService) ResetForbiddenCounter(ctx context.Context, accountID int64) {
	if s == nil || s.options.ForbiddenCounter == nil || accountID <= 0 {
		return
	}
	if err := s.options.ForbiddenCounter.ResetOpenAI403Count(ctx, accountID); err != nil {
		s.options.Warn("openai_403_reset_failed", "account_id", accountID, "error", err)
	}
}

// OpenAI403CooldownSettings OpenAI OAuth 403 冷却配置
type OpenAI403CooldownSettings struct {
	// Enabled 是否在 ChatGPT 账号收到 403 时暂停调度
	Enabled bool `json:"enabled"`
	// CooldownMinutes 冷却时长（分钟）
	CooldownMinutes int `json:"cooldown_minutes"`
	// ErrorOnThresholdEnabled 是否在统计窗口内达到 403 阈值后标记账号错误
	ErrorOnThresholdEnabled bool `json:"error_on_threshold_enabled"`
	// ThresholdCount 统计窗口内触发错误状态的 403 次数阈值
	ThresholdCount int `json:"threshold_count"`
	// ThresholdWindowMinutes 403 次数统计窗口（分钟）
	ThresholdWindowMinutes int `json:"threshold_window_minutes"`
}

// DefaultOpenAI403CooldownSettings 返回默认的 OpenAI OAuth 403 冷却配置（启用，10分钟，3次/180分钟转错误）
func DefaultOpenAI403CooldownSettings() *OpenAI403CooldownSettings {
	return &OpenAI403CooldownSettings{
		Enabled:                 true,
		CooldownMinutes:         OpenAI403CooldownMinutesDefault,
		ErrorOnThresholdEnabled: true,
		ThresholdCount:          OpenAI403DisableThresholdDefault,
		ThresholdWindowMinutes:  OpenAI403CounterWindowMinutesDefault,
	}
}

// OpenAI403CounterCache 追踪 OpenAI 账号连续 403 失败次数。
type OpenAI403CounterCache interface {
	// IncrementOpenAI403Count 原子递增 403 计数并返回当前值。
	IncrementOpenAI403Count(ctx context.Context, accountID int64, windowMinutes int) (int64, error)
	// ResetOpenAI403Count 成功后清零计数器。
	ResetOpenAI403Count(ctx context.Context, accountID int64) error
}
