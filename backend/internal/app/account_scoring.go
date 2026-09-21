package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// accountScoreOptions 投影静态参数并使用生产选择共享的设置缓存和反馈实例。
func accountScoreOptions(concurrency *scheduler.ConcurrencyService, shared *schedulerSharedState, store *settings.Store, cfg *config.Config) account.SchedulerScoreOptions {
	topK := 7
	weights := policy.ScoreWeights{Priority: 1, Load: 1, Queue: 0.7, ErrorRate: 0.8, TTFT: 0.5, Previous: 5, SessionSticky: 3}
	defaults := policy.RuntimeSettings{EwmaErrorRateAlpha: scheduler.DefaultErrorRateAlpha, EwmaTTFTAlpha: scheduler.DefaultTTFTAlpha, StickyEscape: policy.StickyEscapeConfig{Enabled: true, TtftMs: 15000, ErrorRate: 0.5}}
	if cfg != nil {
		v := cfg.Gateway.AdvancedScheduler
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
	return provider.SchedulerScoreOptions(concurrency, shared.Feedback, func(ctx context.Context, group *accessview.GroupConfig) policy.EffectiveSettings {
		if ctx == nil {
			ctx = context.Background()
		}
		var overrides policy.GroupAdvancedSchedulerOverrides
		if group != nil && group.SchedulerType == "advanced" {
			overrides = group.AdvancedSchedulerOverrides
		}
		return policy.ResolveEffective(topK, weights, shared.Settings.Load(ctx, source, defaults), overrides)
	})
}
