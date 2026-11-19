package provider

import (
	"context"
	"time"
)

// health_thresholds.go applies usage-threshold pauses to providers or the
// Anthropic Fable model family.
// Usage thresholds can pause an entire provider or only Anthropic's Fable model
// family. The provider path compares an existing structured pause before
// deciding whether the selected record remains eligible for a new write, so the
// same threshold decision can be recognized while that record is already
// paused.
//
// A new provider pause updates the caller's record and runtime block before
// persistence; a failed write therefore still reports a handled threshold
// decision. Fable evaluation runs when no provider pause is selected and leaves
// any currently live Fable window in place. Both paths require a mutable record
// whose changes are synchronized by its caller.

// SchedulingThresholds returns the configured platform threshold map for
// read-only callers such as score diagnostics. Disabled or incomplete services
// return nil and false without modifying provider records, caches, or stores.
func (s *HealthService) SchedulingThresholds(ctx context.Context) (map[string]int, bool) {
	if s == nil || s.options.HasThresholdSettings == nil || !s.options.HasThresholdSettings() || s.options.Thresholds == nil {
		return nil, false
	}
	return s.options.Thresholds(ctx), true
}

// ApplyProviderSchedulingThreshold reports whether a provider-wide threshold
// pause is selected or already matches the supplied record. Disabled or missing
// dependencies, inactive records, and a false manual scheduling switch return
// false. When HasThresholdSettings reports true, Thresholds must be configured;
// callers supply a privately mutable record or synchronize access.
//
// An identical pause returns true before the full schedulability check. A new
// eligible pause mutates the record and installs its runtime block before
// storage; storage failure is logged and still returns true. When no
// provider-wide decision applies, Fable evaluation can write a model cooldown
// while this method returns false.
func (s *HealthService) ApplyProviderSchedulingThreshold(ctx context.Context, provider *Record) bool {
	if s == nil || s.options.HasThresholdSettings == nil || !s.options.HasThresholdSettings() || s.providerRepo == nil || provider == nil || provider.ID <= 0 {
		return false
	}
	if !provider.IsActive() || !provider.Schedulable {
		return false
	}

	now := s.options.Now().UTC()
	thresholds := s.options.Thresholds(ctx)
	decision := EvaluateProviderSchedulingThreshold(provider, thresholds, now)
	// A missing provider-wide decision still permits the separate Fable rule to
	// run. Its model-scoped side effects leave this method's provider-pause result
	// false.
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		s.applyAnthropicFableSchedulingThreshold(ctx, provider, thresholds, now)
		return false
	}

	reason := BuildDetailedProviderSchedulingThresholdReason(ProviderSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent,
		UsedPercent:      decision.UsedPercent,
		Until:            *decision.Until,
		Now:              now,
	})

	if providerHasSameSchedulingThresholdPause(provider, *decision.Until, reason) {
		return true
	}
	if !provider.IsSchedulable() {
		return false
	}

	// Publish the pause to this attempt's mutable record and runtime block before
	// storage. Those changes survive a store error and keep this decision handled.
	provider.TempUnschedulableUntil = CloneThresholdTime(decision.Until)
	provider.TempUnschedulableReason = reason
	s.notifyProviderSchedulingBlocked(provider, *decision.Until, "provider_scheduling_threshold")

	if err := s.providerRepo.SetTempUnschedulable(ctx, provider.ID, *decision.Until, reason); err != nil {
		s.options.Warn("provider_scheduling_threshold_set_temp_unsched_failed",
			"provider_id", provider.ID,
			"platform", decision.Platform,
			"window", decision.Window,
			"scope", decision.Scope,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
	} else if s.tempUnschedCache != nil {
		// Cache the structured state after a successful store call. Cache failures
		// are diagnostic only and leave the record and runtime pause already applied.
		if state := tempUnschedStateFromStoredReason(reason, decision.Until.Unix()); state != nil {
			if err := s.tempUnschedCache.SetTempUnsched(ctx, provider.ID, state); err != nil {
				s.options.Warn("provider_scheduling_threshold_cache_set_failed", "provider_id", provider.ID, "error", err)
			}
		}
	}

	s.options.Info("provider_scheduling_threshold_temp_unschedulable",
		"provider_id", provider.ID,
		"platform", decision.Platform,
		"window", decision.Window,
		"scope", decision.Scope,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
	return true
}

// applyAnthropicFableSchedulingThreshold applies a future threshold decision to
// the Fable scope on the caller's mutable record.
// ApplyProviderSchedulingThreshold invokes it only when provider-wide
// evaluation does not produce a pause, after checking the record's active state
// and manual switch.
//
// An existing Fable reset that is still future according to the service clock
// suppresses replacement, even if the newly selected reset is later. Otherwise
// the local model payload is installed before persistence. A failed write
// retains that local mutation and is logged; no provider-wide pause is
// installed here.
func (s *HealthService) applyAnthropicFableSchedulingThreshold(ctx context.Context, provider *Record, thresholds map[string]int, now time.Time) {
	decision := EvaluateAnthropicFableSchedulingThreshold(provider, thresholds, now)
	if !decision.ShouldPause || decision.Until == nil || !decision.Until.After(now) {
		return
	}
	// A live Fable cooldown is already effective for model eligibility. Keep it
	// through its reset rather than replacing its reason or extending this
	// observation.
	if reset := provider.ModelRateLimitResetAt(AnthropicFableRateLimitKey); reset != nil && s.options.Now().Before(*reset) {
		return
	}

	reason := BuildDetailedProviderSchedulingThresholdReason(ProviderSchedulingThresholdReasonInput{
		Platform:         decision.Platform,
		Window:           decision.Window,
		Scope:            decision.Scope,
		ThresholdPercent: decision.ThresholdPercent,
		UsedPercent:      decision.UsedPercent,
		Until:            *decision.Until,
		Now:              now,
	})
	// Update this attempt's model snapshot before the store call so subsequent
	// checks on the supplied record see Fable paused even if persistence fails.
	SetModelRateLimitSnapshot(provider, AnthropicFableRateLimitKey, *decision.Until, reason, now)
	if err := s.providerRepo.SetModelRateLimit(ctx, provider.ID, AnthropicFableRateLimitKey, *decision.Until, reason); err != nil {
		s.options.Warn("anthropic_fable_scheduling_threshold_set_model_limit_failed",
			"provider_id", provider.ID,
			"threshold_percent", decision.ThresholdPercent,
			"used_percent", decision.UsedPercent,
			"until", decision.Until.UTC(),
			"error", err)
		return
	}

	s.options.Info("anthropic_fable_scheduling_threshold_model_limited",
		"provider_id", provider.ID,
		"scope", AnthropicFableRateLimitKey,
		"threshold_percent", decision.ThresholdPercent,
		"used_percent", decision.UsedPercent,
		"until", decision.Until.UTC())
}

// providerHasSameSchedulingThresholdPause compares a stored provider pause with
// a proposed structured threshold reason. Both reasons must parse with the
// threshold source, and the deadlines must match at Unix-second precision.
//
// TriggeredAtUnix is cleared in each parsed payload before comparing all
// remaining fields. A fresh observation time alone therefore does not cause
// another pause write, while changed window, usage, threshold, or other payload
// data still makes the proposed pause distinct. Subsecond differences in the
// separate deadlines are also ignored.
func providerHasSameSchedulingThresholdPause(provider *Record, until time.Time, reason string) bool {
	if provider == nil || provider.TempUnschedulableUntil == nil {
		return false
	}
	// Stored pause payloads also represent reset times as Unix seconds.
	if provider.TempUnschedulableUntil.UTC().Unix() != until.UTC().Unix() {
		return false
	}

	existing, ok := parseTempUnschedReasonPayload(provider.TempUnschedulableReason)
	if !ok || existing.Source != ProviderSchedulingThresholdReasonSource {
		return false
	}
	next, ok := parseTempUnschedReasonPayload(reason)
	if !ok || next.Source != ProviderSchedulingThresholdReasonSource {
		return false
	}

	// Observation timestamps change when evaluating the same threshold again.
	// Exclude that field while retaining every other parsed payload field in the
	// equality comparison.
	existing.TriggeredAtUnix = 0
	next.TriggeredAtUnix = 0
	return existing == next
}
