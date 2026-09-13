package service

import "context"

// 原白盒分数断言只从新核心读取，不保存算法或状态。
type openAIAccountLoadPlan struct{ candidates []openAIAccountCandidateScore }

func (s *defaultOpenAIAccountScheduler) buildOpenAIAccountLoadPlan(ctx context.Context, req OpenAIAccountScheduleRequest, values []*Account, loads map[int64]*AccountLoadInfo) openAIAccountLoadPlan {
	core, scope := s.platformSelector()
	plan := core.BuildPlan(ctx, platformSelectionInput(req), scope.pointers(values), loads)
	return openAIAccountLoadPlan{candidates: scope.legacyPlatformScores(plan.CandidatesSnapshot())}
}
func (s *defaultOpenAIAccountScheduler) tryAcquireOpenAISelectionOrder(ctx context.Context, req OpenAIAccountScheduleRequest, values []openAIAccountCandidateScore) (*AccountSelectionResult, bool, error) {
	core, scope := s.platformSelector()
	v, blocked, err := core.TryOrderBounded(ctx, platformSelectionInput(req), scope.platformScores(values))
	return scope.restore(v), blocked, err
}
