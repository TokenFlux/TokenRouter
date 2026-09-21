package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	"context"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestRateLimitServiceAdvancedSchedulerScoreSnapshotUsesSharedRuntimeStats(t *testing.T) {

	weights := policy.ScoreWeights{
		Priority:  1,
		ErrorRate: 2,
		TTFT:      3,
	}
	cfg := &config.Config{}
	cfg.Gateway.AdvancedScheduler.ScoreWeights = config.GatewayAdvancedSchedulerScoreWeights{
		Priority:  weights.Priority,
		ErrorRate: weights.ErrorRate,
		TTFT:      weights.TTFT,
	}
	shared := newScoreFixtureState()
	stats := shared.Feedback
	options := accountScoreOptions(nil, shared, nil, cfg)
	group := &routing.Group{ID: 71, Platform: capability.PlatformGemini, SchedulerType: routing.GroupSchedulerTypeAdvanced}
	accounts := []*account.Record{
		{ID: 7101, Platform: capability.PlatformGemini, Priority: 1},
		{ID: 7102, Platform: capability.PlatformGemini, Priority: 1},
	}

	// 没有样本时，共享统计和 nil 统计都必须把错误率按 0% 处理。
	neutral := options.Score(context.Background(), (*accessview.GroupConfig)(group), accounts, nil)
	neutralCore, _ := scheduler.ScoreCandidates(scoreFixtureAccounts(accounts), nil, nil, weights, scheduler.ScoreInput{}, time.Now())
	require.Len(t, neutralCore, 2)
	for index, account := range accounts {
		require.InDelta(t, 4.5, neutral[account.ID].BaseScore, 0.000001)
		require.InDelta(t, neutralCore[index].Score, neutral[account.ID].BaseScore, 0.000001)
	}

	slowTTFT := 3000
	fastTTFT := 1000
	stats.Report(accounts[0].ID, false, &slowTTFT)
	stats.Report(accounts[1].ID, true, &fastTTFT)

	observed := options.Score(context.Background(), (*accessview.GroupConfig)(group), accounts, nil)
	core, _ := scheduler.ScoreCandidates(scoreFixtureAccounts(accounts), nil, stats, weights, scheduler.ScoreInput{}, time.Now())
	require.Len(t, core, 2)
	for index, account := range accounts {
		require.InDelta(t, core[index].Score, observed[account.ID].BaseScore, 0.000001)
	}
	// 零基线下首次失败把错误率 EWMA 更新为 0.2，错误率因子降至 0.8；首次成功保持 1。
	require.InDelta(t, 2.6, observed[accounts[0].ID].BaseScore, 0.000001)
	require.InDelta(t, 6.0, observed[accounts[1].ID].BaseScore, 0.000001)
	require.Less(t, observed[accounts[0].ID].BaseScore, neutral[accounts[0].ID].BaseScore)
	require.Greater(t, observed[accounts[1].ID].BaseScore, neutral[accounts[1].ID].BaseScore)
}

func TestAdvancedSchedulerScoreSnapshotUsesPreviousResponseOnlyForOpenAI(t *testing.T) {

	stickyWeighted := true
	previousWeight := 11.0
	sessionWeight := 7.0
	platforms := []string{
		capability.PlatformOpenAI,
		capability.PlatformAnthropic,
		capability.PlatformGemini,
		capability.PlatformAntigravity,
		capability.PlatformQoder,
		capability.PlatformGrok,
	}

	for index, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			group := &routing.Group{
				ID:            int64(7200 + index),
				Platform:      platform,
				SchedulerType: routing.GroupSchedulerTypeAdvanced,
				AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
					StickyWeightedEnabled:  &stickyWeighted,
					WeightPreviousResponse: &previousWeight,
					WeightSessionSticky:    &sessionWeight,
				},
			}
			value := &account.Record{ID: int64(7300 + index), Platform: platform, Priority: 1}
			snapshot := accountScoreOptions(nil, newScoreFixtureState(), nil, nil).Score(context.Background(), (*accessview.GroupConfig)(group), []*account.Record{value}, nil)[value.ID]

			expectedBonus := sessionWeight
			if platform == capability.PlatformOpenAI {
				expectedBonus += previousWeight
			}
			require.True(t, snapshot.StickyWeightedEnabled)
			require.False(t, snapshot.StickyScoreInfinity)
			require.InDelta(t, snapshot.BaseScore+expectedBonus, snapshot.StickyScore, 0.000001)
		})
	}
}

func TestAdvancedSchedulerScoreSnapshotKeepsHardStickyInfinity(t *testing.T) {

	stickyWeighted := false
	group := &routing.Group{
		ID:            7401,
		Platform:      capability.PlatformGemini,
		SchedulerType: routing.GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: &stickyWeighted,
		},
	}
	value := &account.Record{ID: 7402, Platform: capability.PlatformGemini, Priority: 1}
	snapshot := accountScoreOptions(nil, newScoreFixtureState(), nil, nil).Score(context.Background(), (*accessview.GroupConfig)(group), []*account.Record{value}, nil)[value.ID]

	require.False(t, snapshot.StickyWeightedEnabled)
	require.True(t, snapshot.StickyScoreInfinity)
	require.Zero(t, snapshot.StickyScore)
}

// 每个测试持有独立运行状态，生产装配则复用唯一 shared 对象。
func newScoreFixtureState() *schedulerSharedState {
	return &schedulerSharedState{Settings: scheduler.NewSettingsRuntime(scheduler.Diagnostics{}), Feedback: scheduler.NewRuntimeStats(time.Now)}
}
func scoreFixtureAccounts(values []*account.Record) []*scheduler.ScoreAccount {
	result := make([]*scheduler.ScoreAccount, len(values))
	for i, v := range values {
		result[i] = &scheduler.ScoreAccount{ID: v.ID, Platform: v.Platform, Priority: v.Priority, SessionWindowEnd: v.SessionWindowEnd}
	}
	return result
}
