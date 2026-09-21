package provider

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// SchedulerScoreOptions 复用共享反馈与动态参数，投影不携带凭据进入评分核心。
func SchedulerScoreOptions(concurrency *scheduler.ConcurrencyService, stats *scheduler.RuntimeStats, effective func(context.Context, *accessview.GroupConfig) policy.EffectiveSettings) account.SchedulerScoreOptions {
	out := account.SchedulerScoreOptions{Warn: slog.Warn, Score: func(ctx context.Context, group *accessview.GroupConfig, values []*account.Record, load map[int64]*account.SchedulerLoad) map[int64]account.AccountSchedulerScore {
		projected := make([]*scheduler.ScoreAccount, len(values))
		sources := make(map[*scheduler.ScoreAccount]*account.Record, len(values))
		for i, value := range values {
			if value != nil {
				v := &scheduler.ScoreAccount{ID: value.ID, Platform: value.Platform, Priority: value.Priority, SessionWindowEnd: value.SessionWindowEnd}
				projected[i] = v
				sources[v] = value
			}
		}
		loads := make(map[int64]*scheduler.AccountLoadInfo, len(load))
		for id, v := range load {
			if v != nil {
				loads[id] = &scheduler.AccountLoadInfo{AccountID: v.AccountID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
			}
		}
		var g *scheduler.ScoreGroup
		if group != nil {
			g = &scheduler.ScoreGroup{Platform: group.Platform}
		}
		settings := effective(ctx, group)
		scores := scheduler.BuildScoreSnapshot(projected, loads, stats, g, settings.Weights, settings.StickyWeightedEnabled, func(v *scheduler.ScoreAccount, now time.Time) float64 {
			return account.OpenAIQuotaHeadroomFactor(sources[v], now)
		}, time.Now())
		result := make(map[int64]account.AccountSchedulerScore, len(scores))
		for id, v := range scores {
			result[id] = account.AccountSchedulerScore{BaseScore: v.BaseScore, StickyScore: v.StickyScore, StickyScoreInfinity: v.StickyScoreInfinity, StickyWeightedEnabled: v.StickyWeightedEnabled}
		}
		return result
	}}
	if concurrency != nil {
		out.Load = func(ctx context.Context, values []account.SchedulerLoadRequest) (map[int64]*account.SchedulerLoad, error) {
			requests := make([]scheduler.AccountWithConcurrency, len(values))
			for i, v := range values {
				requests[i] = scheduler.AccountWithConcurrency{ID: v.ID, MaxConcurrency: v.MaxConcurrency}
			}
			loads, err := concurrency.GetAccountsLoadBatch(ctx, requests)
			if loads == nil {
				return nil, err
			}
			result := make(map[int64]*account.SchedulerLoad, len(loads))
			for id, v := range loads {
				if v != nil {
					result[id] = &account.SchedulerLoad{AccountID: v.AccountID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
				}
			}
			return result, err
		}
	}
	return out
}
