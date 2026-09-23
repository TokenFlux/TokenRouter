package service

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 原白盒合同直接观察平台核心的无凭据结果，不构造旧评分类型。
type openAIAccountLoadPlan struct{ candidates []scheduler.CandidateScore }

func (s *defaultOpenAIAccountScheduler) buildOpenAIAccountLoadPlan(ctx context.Context, req OpenAIAccountScheduleRequest, values []*gatewayprovider.ExecutionAccount, loads map[int64]*scheduler.AccountLoadInfo) openAIAccountLoadPlan {
	core, scope := s.platformSelector()
	plan := core.BuildPlan(ctx, platformSelectionInput(req), scope.pointers(values), loads)
	var candidates []scheduler.CandidateScore
	for _, value := range plan.CandidatesSnapshot() {
		candidates = append(candidates, scheduler.CandidateScore{Account: &scheduler.ScoreAccount{ID: value.Account.ID, Priority: value.Account.Priority}, Score: value.Score})
	}
	return openAIAccountLoadPlan{candidates: candidates}
}

// fresh/DB 合同在同一次候选作用域内传入账号，避免测试包装丢失对应关系。
func (s *defaultOpenAIAccountScheduler) tryAcquireOpenAISelectionOrder(ctx context.Context, req OpenAIAccountScheduleRequest, values []*gatewayprovider.ExecutionAccount) (*gatewayprovider.SelectionResult, bool, error) {
	core, scope := s.platformSelector()
	candidates := make([]scheduler.PlatformCandidateScore, len(values))
	for i, value := range values {
		candidates[i] = scheduler.PlatformCandidateScore{Account: scope.account(value), LoadInfo: &scheduler.AccountLoadInfo{AccountID: value.Record.ID}}
	}
	result, blocked, err := core.TryOrderBounded(ctx, platformSelectionInput(req), candidates)
	return scope.restore(result), blocked, err
}
