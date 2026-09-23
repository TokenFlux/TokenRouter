package selection

import (
	"context"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// 原白盒合同直接观察平台核心的无凭据结果，不构造旧评分类型。
type openAIAccountLoadPlan struct{ candidates []scheduler.CandidateScore }

func (s *compatiblePicker) buildOpenAIAccountLoadPlan(ctx context.Context, req scheduler.PlatformSelectionInput, values []*gatewayprovider.ExecutionAccount, loads map[int64]*scheduler.AccountLoadInfo) openAIAccountLoadPlan {
	core, scope := s.platformSelector()
	plan := core.BuildPlan(ctx, req, scope.pointers(values), loads)
	var candidates []scheduler.CandidateScore
	for _, value := range plan.CandidatesSnapshot() {
		candidates = append(candidates, scheduler.CandidateScore{Account: &scheduler.ScoreAccount{ID: value.Account.ID, Priority: value.Account.Priority}, Score: value.Score})
	}
	return openAIAccountLoadPlan{candidates: candidates}
}
