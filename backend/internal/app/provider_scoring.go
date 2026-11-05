package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provider_scoring.go resolves administrator scoring inputs from deployment
// defaults, runtime settings, and eligible group overrides. Administrator score
// snapshots use the same feedback and runtime settings as provider selection.
// Process configuration supplies initial weights and escape policy, but group
// overrides are read only for advanced groups, keeping basic group display
// independent of stale advanced settings.
//
// Missing process configuration keeps built-in inputs, and a nonpositive
// configured Top-K retains seven candidates. The callback loads runtime
// settings with those defaults before resolving eligible group overrides. A
// nil callback context becomes Background so the shared settings reader has a
// usable context for that lookup.

// providerScoreOptions binds diagnostic scoring to shared feedback and
// effective scheduler settings. Nil cfg supplies built-in inputs; a positive
// configured Top-K overrides seven, and the callback uses group overrides only
// for advanced groups while replacing nil context with Background.
func providerScoreOptions(concurrency *scheduler.ConcurrencyService, shared *schedulerSharedState, store *settings.Store, cfg *config.Config) provider.SchedulerScoreOptions {
	// Diagnostic candidate scoring starts with seven Top-K choices before settings overrides.
	topK := 7
	weights := policy.ScoreWeights{Priority: 1, Load: 1, Queue: 0.7, ErrorRate: 0.8, TTFT: 0.5, Previous: 5, SessionSticky: 3}
	defaults := policy.RuntimeSettings{EwmaErrorRateAlpha: scheduler.DefaultErrorRateAlpha, EwmaTTFTAlpha: scheduler.DefaultTTFTAlpha, StickyEscape: policy.StickyEscapeConfig{Enabled: true, TtftMs: 15000, ErrorRate: 0.5}}
	if cfg != nil {
		v := cfg.Scheduling.Advanced
		if v.LBTopK > 0 {
			topK = v.LBTopK
		}
		weights = policy.ScoreWeights{Priority: v.ScoreWeights.Priority, Load: v.ScoreWeights.Load, Queue: v.ScoreWeights.Queue, ErrorRate: v.ScoreWeights.ErrorRate, TTFT: v.ScoreWeights.TTFT, Reset: v.ScoreWeights.Reset, QuotaHeadroom: v.ScoreWeights.QuotaHeadroom, Previous: v.ScoreWeights.PreviousResponse, SessionSticky: v.ScoreWeights.SessionSticky}
		defaults.EwmaErrorRateAlpha = v.EWMAErrorRateAlpha
		defaults.EwmaTTFTAlpha = v.EWMATTFTAlpha
		defaults.StickyEscape = policy.NormalizeStickyEscape(policy.StickyEscapeConfig{Enabled: v.StickyEscapeEnabled, TtftMs: float64(v.StickyEscapeTTFTMs), ErrorRate: v.StickyEscapeErrorRate})
	}
	var source scheduler.RuntimeSettingSource
	if store != nil {
		source = store
	}
	return provideradapter.SchedulerScoreOptions(concurrency, shared.Feedback, func(ctx context.Context, group *accessview.GroupConfig) policy.EffectiveSettings {
		if ctx == nil {
			ctx = context.Background()
		}
		var overrides policy.GroupAdvancedSchedulerOverrides
		// Basic groups can retain old advanced configuration. Admit overrides
		// only for an advanced group so those retained fields cannot alter its basic score display.
		if group != nil && group.SchedulerType == "advanced" {
			overrides = group.AdvancedSchedulerOverrides
		}
		return policy.ResolveEffective(topK, weights, shared.Settings.Load(ctx, source, defaults), overrides)
	})
}
