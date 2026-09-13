// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	slog "log/slog"
)

// AccountSchedulerScoreOptions 只转换旧调度器输入输出；S07 改绑执行源。
func AccountSchedulerScoreOptions(concurrency *ConcurrencyService, limits *RateLimitService) accountcore.SchedulerScoreOptions {
	out := accountcore.SchedulerScoreOptions{Warn: slog.Warn, Score: func(ctx context.Context, group *accessview.GroupConfig, values []*accountcore.Record, load map[int64]*accountcore.SchedulerLoad) map[int64]accountcore.AccountSchedulerScore {
		legacy := make([]*Account, len(values))
		for i, v := range values {
			legacy[i] = AccountFromRecord(v)
		}
		loads := make(map[int64]*AccountLoadInfo, len(load))
		for id, v := range load {
			if v != nil {
				loads[id] = &AccountLoadInfo{AccountID: v.AccountID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
			}
		}
		g := GroupFromRouting((*routing.Group)(group))
		var scores map[int64]AdvancedAccountSchedulerScoreSnapshot
		if limits != nil {
			scores = limits.BuildAdvancedAccountSchedulerScoreSnapshotForGroup(ctx, g, legacy, loads)
		} else {
			scores = BuildAdvancedAccountSchedulerScoreSnapshotForGroup(g, legacy, loads)
		}
		result := make(map[int64]accountcore.AccountSchedulerScore, len(scores))
		for id, v := range scores {
			result[id] = accountcore.AccountSchedulerScore{BaseScore: v.BaseScore, StickyScore: v.StickyScore, StickyScoreInfinity: v.StickyScoreInfinity, StickyWeightedEnabled: v.StickyWeightedEnabled}
		}
		return result
	}}
	if concurrency != nil {
		out.Load = func(ctx context.Context, values []accountcore.SchedulerLoadRequest) (map[int64]*accountcore.SchedulerLoad, error) {
			requests := make([]AccountWithConcurrency, len(values))
			for i, v := range values {
				requests[i] = AccountWithConcurrency{ID: v.ID, MaxConcurrency: v.MaxConcurrency}
			}
			loads, err := concurrency.GetAccountsLoadBatch(ctx, requests)
			if loads == nil {
				return nil, err
			}
			out := make(map[int64]*accountcore.SchedulerLoad, len(loads))
			for id, v := range loads {
				if v != nil {
					out[id] = &accountcore.SchedulerLoad{AccountID: v.AccountID, CurrentConcurrency: v.CurrentConcurrency, WaitingCount: v.WaitingCount, LoadRate: v.LoadRate}
				}
			}
			return out, err
		}
	}
	return out
}
