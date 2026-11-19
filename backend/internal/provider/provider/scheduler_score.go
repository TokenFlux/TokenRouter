package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// scheduler_score.go adapts provider records and live load to administrative
// scheduler score calculations.
// Administrative scores use the same scheduler feedback and scoring function as
// request selection. Provider records are converted to score inputs while a
// pointer-keyed source map keeps their OpenAI quota state available to the
// headroom callback for those exact inputs.
//
// Each score call reads effective settings for its group and returns
// provider-keyed display values. Nil records and load entries stay absent from
// the converted data, while group presence selects the scheduler's group mode.
// An optional concurrency service supplies batch load independently, allowing
// the score view to retain its scoring callback even when live load reads are
// unavailable.

// SchedulerScoreOptions returns administrative score and optional batch-load
// callbacks backed by shared scheduler state. effective is called for each
// score request, and a nil concurrency service leaves Load unset.
//
// The score callback retains a source record for each newly allocated score
// input, letting the scheduler's quota callback consult the matching provider
// state. It converts load values and results into their module types while
// preserving provider IDs; callers keep supplied records stable through that
// synchronous score calculation.
func SchedulerScoreOptions(concurrency *scheduler.ConcurrencyService, stats *scheduler.RuntimeStats, effective func(context.Context, *accessview.GroupConfig) policy.EffectiveSettings) provider.SchedulerScoreOptions {
	out := provider.SchedulerScoreOptions{Warn: slog.Warn, Score: func(ctx context.Context, group *accessview.GroupConfig, values []*provider.Record, load map[int64]*provider.SchedulerLoad) map[int64]provider.ProviderSchedulerScore {
		settings := effective(ctx, group)
		// Group pools narrow to ChatGPT subscription candidates when subscription
		// preference is enabled and at least one subscription candidate is present,
		// matching diagnosticSubscriptionPriorityPool.
		pool := values
		if group != nil && settings.SubscriptionPriorityEnabled {
			var subscribed []*provider.Record
			for _, value := range values {
				if value != nil && value.IsOpenAIChatGPTSubscription() {
					subscribed = append(subscribed, value)
				}
			}
			if len(subscribed) > 0 {
				pool = subscribed
			}
		}
		// Retain the source by score-input pointer because the headroom callback
		// receives that converted object and needs its matching provider's quota
		// snapshot.
		projected := make([]*scheduler.ScoreProvider, len(pool))
		sources := make(map[*scheduler.ScoreProvider]*provider.Record, len(pool))
		for i, value := range pool {
			if value != nil {
				v := &scheduler.ScoreProvider{ID: value.ID, Platform: value.Platform, Priority: value.Priority, SessionWindowEnd: value.SessionWindowEnd}
				projected[i] = v
				sources[v] = value
			}
		}
		loads := make(map[int64]*scheduler.ProviderLoadInfo, len(load))
		for id, v := range load {
			if v != nil {
				loads[id] = &scheduler.ProviderLoadInfo{ProviderID: v.ProviderID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
			}
		}
		var g *scheduler.ScoreGroup
		if group != nil {
			g = &scheduler.ScoreGroup{}
		}
		scores := scheduler.BuildScoreSnapshot(projected, loads, stats, g, settings.Weights, settings.StickyWeightedEnabled, func(v *scheduler.ScoreProvider, now time.Time) float64 {
			return provider.OpenAIQuotaHeadroomFactor(sources[v], now)
		}, time.Now())
		result := make(map[int64]provider.ProviderSchedulerScore, len(scores))
		for id, v := range scores {
			result[id] = provider.ProviderSchedulerScore{BaseScore: v.BaseScore, StickyScore: v.StickyScore, StickyScoreInfinity: v.StickyScoreInfinity, StickyWeightedEnabled: v.StickyWeightedEnabled}
		}
		return result
	}}
	if concurrency != nil {
		out.Load = func(ctx context.Context, values []provider.SchedulerLoadRequest) (map[int64]*provider.SchedulerLoad, error) {
			requests := make([]scheduler.ProviderWithConcurrency, len(values))
			for i, v := range values {
				requests[i] = scheduler.ProviderWithConcurrency{ID: v.ID, MaxConcurrency: v.MaxConcurrency}
			}
			loads, err := concurrency.GetProvidersLoadBatch(ctx, requests)
			if loads == nil {
				return nil, err
			}
			result := make(map[int64]*provider.SchedulerLoad, len(loads))
			for id, v := range loads {
				if v != nil {
					result[id] = &provider.SchedulerLoad{ProviderID: v.ProviderID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
				}
			}
			return result, err
		}
	}
	return out
}
