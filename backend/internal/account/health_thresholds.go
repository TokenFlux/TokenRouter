// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"time"
)

// ApplyAccountSchedulingThreshold 评估管理员配置的平台用量阈值。
// 超出阈值时，将账号临时设为不可调度直到命中的窗口重置；
// 账号刚被阻断或已因同一阈值原因暂停时均返回 true。
func (s *HealthService) ApplyAccountSchedulingThreshold(ctx context.Context, account *Record) bool {
	if s == nil || s.options.HasThresholdSettings == nil || !s.options.HasThresholdSettings() || s.accountRepo == nil || account == nil || account.ID <= 0 {
		return false
	}
	if !account.IsActive() || !account.Schedulable {
		return false
	}

	now := s.options.Now().UTC()
	thresholds := s.options.Thresholds(ctx)
	decision := EvaluateAccountSchedulingThreshold(account, thresholds, now)
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		s.applyAnthropicFableSchedulingThreshold(ctx, account, thresholds, now)
		return false
	}

	reason := BuildDetailedAccountSchedulingThresholdReason(AccountSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent,
		UsedPercent:      decision.UsedPercent,
		Until:            *decision.Until,
		Now:              now,
	})

	if accountHasSameSchedulingThresholdPause(account, *decision.Until, reason) {
		return true
	}
	if !account.IsSchedulable() {
		return false
	}

	account.TempUnschedulableUntil = CloneThresholdTime(decision.Until)
	account.TempUnschedulableReason = reason
	s.notifyAccountSchedulingBlocked(account, *decision.Until, "account_scheduling_threshold")

	if err := s.accountRepo.SetTempUnschedulable(ctx, account.ID, *decision.Until, reason); err != nil {
		s.options.Warn("account_scheduling_threshold_set_temp_unsched_failed",
			"account_id", account.ID,
			"platform", decision.Platform,
			"window", decision.Window,
			"scope", decision.Scope,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
	} else if s.tempUnschedCache != nil {
		if state := tempUnschedStateFromStoredReason(reason, decision.Until.Unix()); state != nil {
			if err := s.tempUnschedCache.SetTempUnsched(ctx, account.ID, state); err != nil {
				s.options.Warn("account_scheduling_threshold_cache_set_failed", "account_id", account.ID, "error", err)
			}
		}
	}

	s.options.Info("account_scheduling_threshold_temp_unschedulable",
		"account_id", account.ID,
		"platform", decision.Platform,
		"window", decision.Window,
		"scope", decision.Scope,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
	return true
}

func (s *HealthService) applyAnthropicFableSchedulingThreshold(ctx context.Context, account *Record, thresholds map[string]int, now time.Time) {
	decision := EvaluateAnthropicFableSchedulingThreshold(account, thresholds, now)
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		return
	}
	if reset := account.ModelRateLimitResetAt(AnthropicFableRateLimitKey); reset != nil && s.options.Now().Before(*reset) {
		return
	}

	reason := BuildDetailedAccountSchedulingThresholdReason(AccountSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent,
		UsedPercent:      decision.UsedPercent,
		Until:            *decision.Until,
		Now:              now,
	})
	SetModelRateLimitSnapshot(account, AnthropicFableRateLimitKey, *decision.Until, reason, now)
	if err := s.accountRepo.SetModelRateLimit(ctx, account.ID, AnthropicFableRateLimitKey, *decision.Until, reason); err != nil {
		s.options.Warn("anthropic_fable_scheduling_threshold_set_model_limit_failed",
			"account_id", account.ID,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
		return
	}

	s.options.Info("anthropic_fable_scheduling_threshold_model_limited",
		"account_id", account.ID,
		"scope", AnthropicFableRateLimitKey,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
}

func accountHasSameSchedulingThresholdPause(account *Record, until time.Time, reason string) bool {
	if account == nil || account.TempUnschedulableUntil == nil {
		return false
	}
	if account.TempUnschedulableUntil.UTC().Unix() != until.UTC().Unix() {
		return false
	}

	existing, ok := parseTempUnschedReasonPayload(account.TempUnschedulableReason)
	if !ok || existing.Source != AccountSchedulingThresholdReasonSource {
		return false
	}
	next, ok := parseTempUnschedReasonPayload(reason)
	if !ok || next.Source != AccountSchedulingThresholdReasonSource {
		return false
	}

	existing.TriggeredAtUnix = 0
	next.TriggeredAtUnix = 0
	return existing == next
}
