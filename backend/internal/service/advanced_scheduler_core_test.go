package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/BrandonVee/TokenRouter/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func advancedSchedulerTestWeights() GatewayAdvancedSchedulerScoreWeightsView {
	return GatewayAdvancedSchedulerScoreWeightsView{
		Priority:  1,
		Load:      1,
		Queue:     1,
		ErrorRate: 1,
		TTFT:      1,
	}
}

func TestAdvancedSchedulerCoreUsesRuntimeFeedbackAndNeutralOptionalSignals(t *testing.T) {
	accounts := []*Account{
		{ID: 11, Priority: 1, Platform: PlatformGemini},
		{ID: 12, Priority: 1, Platform: PlatformGemini},
	}
	loadMap := map[int64]*AccountLoadInfo{
		11: {AccountID: 11, LoadRate: 20, WaitingCount: 0},
		12: {AccountID: 12, LoadRate: 20, WaitingCount: 0},
	}
	stats := newAdvancedAccountRuntimeStats()
	for range 8 {
		stats.report(11, false, nil)
		stats.report(12, true, nil)
	}

	candidates, _ := scoreAdvancedSchedulerCandidates(
		accounts,
		loadMap,
		stats,
		advancedSchedulerTestWeights(),
		advancedSchedulerSelectionInput{},
		time.Now(),
	)
	require.Len(t, candidates, 2)
	require.Greater(t, candidates[1].score, candidates[0].score)
	require.False(t, candidates[0].hasTTFT)
	require.False(t, candidates[1].hasTTFT)
}

func TestAdvancedSchedulerCoreTreatsMissingErrorRateAsZero(t *testing.T) {
	accounts := []*Account{
		{ID: 21, Priority: 1, Platform: PlatformGemini},
		{ID: 22, Priority: 1, Platform: PlatformGemini},
	}
	weights := GatewayAdvancedSchedulerScoreWeightsView{
		Load:      1,
		ErrorRate: 1,
		TTFT:      1,
		Reset:     1,
	}

	// 账号 21 缺失负载，账号 22 的已知负载恰好处于中性位置。两者均没有
	// 错误反馈、TTFT 或窗口信息，因此错误率都按 0% 处理且最终分数一致。
	candidates, skew := scoreAdvancedSchedulerCandidates(
		accounts,
		map[int64]*AccountLoadInfo{22: {AccountID: 22, LoadRate: 50}},
		nil,
		weights,
		advancedSchedulerSelectionInput{},
		time.Now(),
	)

	require.Len(t, candidates, 2)
	require.InDelta(t, candidates[0].score, candidates[1].score, 0.000001)
	require.Equal(t, 0.0, skew, "只有一个已知负载样本时不能计算出偏斜")
	require.False(t, candidates[0].loadKnown)
	require.True(t, candidates[1].loadKnown)
	require.InDelta(t, 1.0, candidates[0].factors.ErrorRate, 0.000001)
	require.InDelta(t, 1.0, candidates[1].factors.ErrorRate, 0.000001)
}

func TestAdvancedSchedulerCoreTopKUsesStableOrderForMixedKnownAndUnknownLoads(t *testing.T) {
	base, _ := scoreAdvancedSchedulerCandidates(
		[]*Account{
			{ID: 1, Priority: 5},
			{ID: 2, Priority: 5},
			{ID: 3, Priority: 5},
			{ID: 4, Priority: 5},
		},
		map[int64]*AccountLoadInfo{
			1: {AccountID: 1, LoadRate: 99, WaitingCount: 9},
			3: {AccountID: 3, LoadRate: 1, WaitingCount: 0},
		},
		nil,
		GatewayAdvancedSchedulerScoreWeightsView{},
		advancedSchedulerSelectionInput{},
		time.Now(),
	)
	require.Len(t, base, 4)
	require.True(t, base[0].loadKnown)
	require.False(t, base[1].loadKnown)
	require.True(t, base[2].loadKnown)
	require.False(t, base[3].loadKnown)

	// 遍历全部输入排列，确保全零权重下已知/未知负载混排不会改变同分 Top-K。
	permutation := []int{0, 1, 2, 3}
	var verifyPermutations func(int)
	verifyPermutations = func(position int) {
		if position == len(permutation) {
			candidates := make([]advancedSchedulerCandidateScore, 0, len(permutation))
			for _, index := range permutation {
				candidates = append(candidates, base[index])
			}
			topK := selectTopKAdvancedSchedulerCandidates(candidates, 2)
			require.Len(t, topK, 2)
			require.Equal(t, []int64{1, 2}, []int64{topK[0].account.ID, topK[1].account.ID}, "输入顺序=%v", permutation)
			return
		}
		for index := position; index < len(permutation); index++ {
			permutation[position], permutation[index] = permutation[index], permutation[position]
			verifyPermutations(position + 1)
			permutation[position], permutation[index] = permutation[index], permutation[position]
		}
	}
	verifyPermutations(0)
}

func TestAdvancedSchedulerCoreRanksFirstFailureBelowUnknownAccount(t *testing.T) {
	failed := &Account{ID: 31, Priority: 1, Platform: PlatformGemini}
	unknown := &Account{ID: 32, Priority: 1, Platform: PlatformGemini}
	stats := newAdvancedAccountRuntimeStats()
	stats.report(failed.ID, false, nil)

	candidates, _ := scoreAdvancedSchedulerCandidates(
		[]*Account{failed, unknown},
		nil,
		stats,
		GatewayAdvancedSchedulerScoreWeightsView{ErrorRate: 1},
		advancedSchedulerSelectionInput{},
		time.Now(),
	)

	require.Len(t, candidates, 2)
	require.Less(t, candidates[0].score, candidates[1].score)
	require.Equal(t, unknown.ID, candidates[1].account.ID)
}

func TestAdvancedSchedulerCoreSelectsNonOpenAIGroupAndMarksResult(t *testing.T) {
	groupID := int64(42)
	group := &Group{ID: groupID, Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeAdvanced}
	ctx := context.WithValue(context.Background(), ctxkey.Group, group)
	service := &GatewayService{}

	selection, selected, err := service.tryAcquireByAdvancedScheduler(ctx, &groupID, "session", []accountWithLoad{
		{
			account:  &Account{ID: 101, Platform: PlatformGemini, Priority: 1, Schedulable: true, Status: StatusActive},
			loadInfo: &AccountLoadInfo{AccountID: 101, LoadRate: 0},
		},
	})

	require.NoError(t, err)
	require.True(t, selected)
	require.NotNil(t, selection)
	require.Equal(t, int64(101), selection.Account.ID)
	require.True(t, selection.AdvancedScheduler)

	basicCtx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 43, Platform: PlatformGemini, SchedulerType: GroupSchedulerTypeBasic})
	basicSelection, err := service.newSelectionResult(basicCtx, &Account{ID: 102}, true, func() {}, nil)
	require.NoError(t, err)
	require.False(t, basicSelection.AdvancedScheduler)
}

func TestAdvancedSchedulerCoreUsesWeightedSamplingForStickyCandidate(t *testing.T) {
	candidates := []advancedSchedulerCandidateScore{
		{account: &Account{ID: 1, Priority: 1}, loadInfo: &AccountLoadInfo{}, score: 10},
		{account: &Account{ID: 2, Priority: 1}, loadInfo: &AccountLoadInfo{}, score: 9},
		{account: &Account{ID: 3, Priority: 1}, loadInfo: &AccountLoadInfo{}, score: 1},
	}

	var observedSticky, observedNonSticky bool
	for index := 0; index < 128; index++ {
		order := buildAdvancedSchedulerSelectionOrder(candidates, advancedSchedulerSelectionInput{
			SessionHash:     fmt.Sprintf("weighted-sticky-%d", index),
			StickyWeighted:  true,
			StickyAccountID: 2,
			TopK:            2,
		})

		require.Len(t, order, 2)
		require.ElementsMatch(t, []int64{1, 2}, []int64{order[0].account.ID, order[1].account.ID})
		observedSticky = observedSticky || order[0].account.ID == 2
		observedNonSticky = observedNonSticky || order[0].account.ID == 1
	}
	require.True(t, observedSticky, "粘性加分账号仍应有机会被抽中")
	require.True(t, observedNonSticky, "粘性加权不能退化为强制置首")
}
