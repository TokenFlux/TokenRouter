package service

import (
	"context"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/config"
	"github.com/stretchr/testify/require"
)

func TestRateLimitServiceAdvancedSchedulerScoreSnapshotUsesSharedRuntimeStats(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetAdvancedSchedulerSettingCacheForTest)

	weights := GatewayAdvancedSchedulerScoreWeightsView{
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
	rateLimits := NewRateLimitService(nil, nil, cfg, nil, nil)
	stats := rateLimits.AdvancedSchedulerRuntimeStats()
	group := &Group{ID: 71, Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	accounts := []*Account{
		{ID: 7101, Platform: PlatformGemini, Priority: 1},
		{ID: 7102, Platform: PlatformGemini, Priority: 1},
	}

	// 没有样本时，共享统计和 nil 统计都必须把错误率按 0% 处理。
	neutral := rateLimits.BuildAdvancedAccountSchedulerScoreSnapshotForGroup(context.Background(), group, accounts, nil)
	neutralCore, _ := scoreAdvancedSchedulerCandidates(accounts, nil, nil, weights, advancedSchedulerSelectionInput{
		QuotaHeadroomFactor: openAIQuotaHeadroomFactor,
	}, time.Now())
	require.Len(t, neutralCore, 2)
	for index, account := range accounts {
		require.InDelta(t, 4.5, neutral[account.ID].BaseScore, 0.000001)
		require.InDelta(t, neutralCore[index].score, neutral[account.ID].BaseScore, 0.000001)
	}

	slowTTFT := 3000
	fastTTFT := 1000
	stats.report(accounts[0].ID, false, &slowTTFT)
	stats.report(accounts[1].ID, true, &fastTTFT)

	observed := rateLimits.BuildAdvancedAccountSchedulerScoreSnapshotForGroup(context.Background(), group, accounts, nil)
	core, _ := scoreAdvancedSchedulerCandidates(accounts, nil, stats, weights, advancedSchedulerSelectionInput{
		QuotaHeadroomFactor: openAIQuotaHeadroomFactor,
	}, time.Now())
	require.Len(t, core, 2)
	for index, account := range accounts {
		require.InDelta(t, core[index].score, observed[account.ID].BaseScore, 0.000001)
	}
	// 零基线下首次失败把错误率 EWMA 更新为 0.2，错误率因子降至 0.8；首次成功保持 1。
	require.InDelta(t, 2.6, observed[accounts[0].ID].BaseScore, 0.000001)
	require.InDelta(t, 6.0, observed[accounts[1].ID].BaseScore, 0.000001)
	require.Less(t, observed[accounts[0].ID].BaseScore, neutral[accounts[0].ID].BaseScore)
	require.Greater(t, observed[accounts[1].ID].BaseScore, neutral[accounts[1].ID].BaseScore)
}

func TestAdvancedSchedulerScoreSnapshotUsesPreviousResponseOnlyForOpenAI(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetAdvancedSchedulerSettingCacheForTest)

	stickyWeighted := true
	previousWeight := 11.0
	sessionWeight := 7.0
	platforms := []string{
		PlatformOpenAI,
		PlatformAnthropic,
		PlatformGemini,
		PlatformAntigravity,
		PlatformQoder,
		PlatformGrok,
	}

	for index, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			group := &Group{
				ID:            int64(7200 + index),
				Platform:      platform,
				SchedulerType: GroupSchedulerTypeAdvanced,
				AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
					StickyWeightedEnabled:  &stickyWeighted,
					WeightPreviousResponse: &previousWeight,
					WeightSessionSticky:    &sessionWeight,
				},
			}
			account := &Account{ID: int64(7300 + index), Platform: platform, Priority: 1}
			snapshot := BuildAdvancedAccountSchedulerScoreSnapshotForGroup(group, []*Account{account}, nil)[account.ID]

			expectedBonus := sessionWeight
			if platform == PlatformOpenAI {
				expectedBonus += previousWeight
			}
			require.True(t, snapshot.StickyWeightedEnabled)
			require.False(t, snapshot.StickyScoreInfinity)
			require.InDelta(t, snapshot.BaseScore+expectedBonus, snapshot.StickyScore, 0.000001)
		})
	}
}

func TestAdvancedSchedulerScoreSnapshotKeepsHardStickyInfinity(t *testing.T) {
	resetAdvancedSchedulerSettingCacheForTest()
	t.Cleanup(resetAdvancedSchedulerSettingCacheForTest)

	stickyWeighted := false
	group := &Group{
		ID:            7401,
		Platform:      PlatformGemini,
		SchedulerType: GroupSchedulerTypeAdvanced,
		AdvancedSchedulerOverrides: GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: &stickyWeighted,
		},
	}
	account := &Account{ID: 7402, Platform: PlatformGemini, Priority: 1}
	snapshot := BuildAdvancedAccountSchedulerScoreSnapshotForGroup(group, []*Account{account}, nil)[account.ID]

	require.False(t, snapshot.StickyWeightedEnabled)
	require.True(t, snapshot.StickyScoreInfinity)
	require.Zero(t, snapshot.StickyScore)
}
