package account

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

func isOpenAIAPIKeyHealthBreakerAccount(account *Record) bool {
	return account != nil && account.Platform == capability.PlatformOpenAI && account.Type == capability.AccountTypeAPIKey && account.IsPoolMode()
}

func (s *HealthService) ApplyAPIKeyHealthFailure(ctx context.Context, account *Record, statusCode int, responseBody []byte, eligible bool) bool {
	if s == nil || s.options.APIKeyHealthCounter == nil || s.options.APIKeyHealthSettings == nil || s.accountRepo == nil || !isOpenAIAPIKeyHealthBreakerAccount(account) {
		return false
	}
	if !eligible {
		return false
	}
	settings, err := s.options.APIKeyHealthSettings(ctx)
	if err != nil {
		s.options.APIKeyHealthWarn("openai.apikey_health_breaker_settings_failed", "account_id", account.ID, "error", err)
		return false
	}
	if settings == nil || !settings.Enabled {
		return false
	}

	count, tripped, err := s.options.APIKeyHealthCounter.RecordOpenAIAPIKeyHealthFailure(ctx, account.ID, settings.WindowMinutes, settings.FailureThreshold)
	if err != nil {
		s.options.APIKeyHealthWarn("openai.apikey_health_breaker_record_failed", "account_id", account.ID, "error", err)
		return false
	}
	if !tripped {
		return false
	}

	now := s.options.Now()
	until := now.Add(time.Duration(settings.CooldownMinutes) * time.Minute)
	state := &TempUnschedState{
		UntilUnix:            until.Unix(),
		TriggeredAtUnix:      now.Unix(),
		StatusCode:           statusCode,
		MatchedKeyword:       OpenAIAPIKeyHealthBreakerReason,
		RuleIndex:            -1,
		ErrorMessage:         TruncateTempUnschedMessage(responseBody, TempUnschedMessageMaxBytes),
		TriggerCount:         count,
		TriggerThreshold:     settings.FailureThreshold,
		TriggerWindowMinutes: settings.WindowMinutes,
	}
	reasonBytes, _ := json.Marshal(state)
	reason := string(reasonBytes)
	if reason == "" {
		reason = fmt.Sprintf("%s: %d failures in %d minute(s)", OpenAIAPIKeyHealthBreakerReason, count, settings.WindowMinutes)
	}

	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	if err := s.accountRepo.SetTempUnschedulable(persistCtx, account.ID, until, reason); err != nil {
		s.options.APIKeyHealthWarn("openai.apikey_health_breaker_persist_failed", "account_id", account.ID, "error", err)
		return false
	}

	if account.TempUnschedulableUntil == nil || account.TempUnschedulableUntil.Before(until) {
		account.TempUnschedulableUntil = &until
		account.TempUnschedulableReason = reason
	}
	s.notifyAccountSchedulingBlocked(account, until, OpenAIAPIKeyHealthBreakerReason)
	if s.tempUnschedCache != nil {
		if err := s.tempUnschedCache.SetTempUnsched(persistCtx, account.ID, state); err != nil {
			s.options.APIKeyHealthWarn("openai.apikey_health_breaker_cache_failed", "account_id", account.ID, "error", err)
		}
	}
	s.options.APIKeyHealthWarn("openai.apikey_health_breaker_tripped",
		"account_id", account.ID,
		"failure_count", count,
		"failure_threshold", settings.FailureThreshold,
		"window_minutes", settings.WindowMinutes,
		"cooldown_minutes", settings.CooldownMinutes,
		"upstream_status", statusCode,
		"until", until,
	)
	return true
}

func (s *HealthService) ObserveAPIKeyHealthSuccess(context.Context, *Record) {
	// 健康失败累计在原滚动窗口中；成功不重置窗口，也不增加 Redis 往返。
}

// OpenAIAPIKeyHealthBreakerReason 保留缓存中的原状态来源。
const OpenAIAPIKeyHealthBreakerReason = "openai_apikey_health_breaker"
