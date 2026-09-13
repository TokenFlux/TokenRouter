package scheduler

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// 分数与请求候选之间仅做投影，计算、堆和随机顺序继续复用纯评分实现。
func platformScores(values []PlatformCandidateScore) ([]CandidateScore, map[*ScoreAccount]*FlowAccount) {
	out := make([]CandidateScore, len(values))
	source := map[*ScoreAccount]*FlowAccount{}
	for i, v := range values {
		var a *ScoreAccount
		if v.Account != nil {
			a = &ScoreAccount{ID: v.Account.ID, Name: v.Account.Name, Platform: v.Account.Platform, Priority: v.Account.Priority, SessionWindowEnd: v.Account.SessionWindowEnd, ProjectionID: v.Account.ProjectionID}
			source[a] = v.Account
		}
		out[i] = CandidateScore{Account: a, LoadInfo: v.LoadInfo, LoadKnown: v.LoadKnown, Score: v.Score, BaseScore: v.BaseScore, StickyBonus: v.StickyBonus, PreviousBonus: v.PreviousBonus, SessionStickyBonus: v.SessionStickyBonus, Priority: v.Priority, ErrorRate: v.ErrorRate, TTFT: v.TTFT, HasTTFT: v.HasTTFT, HasFeedback: v.HasFeedback, Feedback: v.Feedback, Factors: v.Factors}
	}
	return out, source
}
func platformScoresFrom(values []CandidateScore, source map[*ScoreAccount]*FlowAccount) []PlatformCandidateScore {
	if values == nil {
		return nil
	}
	out := make([]PlatformCandidateScore, len(values))
	for i, v := range values {
		out[i] = PlatformCandidateScore{Account: source[v.Account], LoadInfo: v.LoadInfo, LoadKnown: v.LoadKnown, Score: v.Score, BaseScore: v.BaseScore, StickyBonus: v.StickyBonus, PreviousBonus: v.PreviousBonus, SessionStickyBonus: v.SessionStickyBonus, Priority: v.Priority, ErrorRate: v.ErrorRate, TTFT: v.TTFT, HasTTFT: v.HasTTFT, HasFeedback: v.HasFeedback, Feedback: v.Feedback, Factors: v.Factors}
	}
	return out
}
func (s *PlatformSelector) scoreCandidates(values []*FlowAccount, loads map[int64]*AccountLoadInfo, stats *RuntimeStats, weights policy.ScoreWeights, input ScoreInput, now time.Time) ([]PlatformCandidateScore, float64) {
	seed := make([]PlatformCandidateScore, len(values))
	for i, a := range values {
		seed[i] = PlatformCandidateScore{Account: a}
	}
	projected, source := platformScores(seed)
	accounts := make([]*ScoreAccount, len(projected))
	for i, v := range projected {
		accounts[i] = v.Account
	}
	scores, skew := ScoreCandidates(accounts, loads, stats, weights, input, now)
	return platformScoresFrom(scores, source), skew
}
func platformTopK(values []PlatformCandidateScore, k int) []PlatformCandidateScore {
	scores, source := platformScores(values)
	return platformScoresFrom(SelectTopK(scores, k), source)
}
func (s *PlatformSelector) weightedOrder(values []PlatformCandidateScore, req PlatformSelectionInput) []PlatformCandidateScore {
	scores, source := platformScores(values)
	order := BuildWeightedSelectionOrder(scores, ScoreInput{GroupID: req.GroupID, SessionHash: req.SessionHash, PreviousResponseID: req.PreviousResponseID, RequestedModel: req.RequestedModel, StickyAccountID: req.StickyAccountID, StickyPreviousAccountID: req.StickyPreviousAccountID, StickyWeighted: req.StickyWeighted, Now: s.now})
	return platformScoresFrom(order, source)
}
func (s *PlatformSelector) partitionSubscription(accounts []*FlowAccount) ([]*FlowAccount, []*FlowAccount) {
	subscriptions := make([]*FlowAccount, 0, len(accounts))
	regular := make([]*FlowAccount, 0, len(accounts))
	for _, a := range accounts {
		if a != nil && s.ports.IsSubscription(a) {
			subscriptions = append(subscriptions, a)
		} else {
			regular = append(regular, a)
		}
	}
	return subscriptions, regular
}

// TryOrderBounded 保留原专用入口的 64 次预算；通用池按其原有预算对象执行。
func (s *PlatformSelector) TryOrderBounded(ctx context.Context, req PlatformSelectionInput, order []PlatformCandidateScore) (*FlowSelection, bool, error) {
	budget := NewProbeBudget()
	budget.enableLimit()
	return s.TryOrder(ctx, req, order, budget)
}
func (s *PlatformSelector) TryOrder(ctx context.Context, req PlatformSelectionInput, order []PlatformCandidateScore, budget *ProbeBudget) (*FlowSelection, bool, error) {
	records := map[uint64]*FlowAccount{}
	project := func(a *FlowAccount) SelectionCandidate {
		if a == nil {
			return SelectionCandidate{}
		}
		records[a.ProjectionID] = a
		return SelectionCandidate{ProjectionID: a.ProjectionID, Snapshot: &account.AccountSnapshot{ID: a.ID, Platform: a.Platform, Type: a.Type, Concurrency: a.Concurrency}, Plan: a.Plan}
	}
	candidates := make([]SelectionCandidate, len(order))
	for i, v := range order {
		candidates[i] = project(v.Account)
		candidates[i].Load = v.LoadInfo
		candidates[i].LoadKnown = v.LoadKnown
	}
	owner := NewLease(ctx, ReleaseOnCompletion)
	if parent := RequestLease(ctx); parent != nil {
		parent.Own(owner.Release)
	}
	output, selected, err := owner.Select(ctx, SelectionInput{Candidates: candidates, RequireCompact: req.RequireCompact, SessionHash: req.SessionHash, PreserveStickyBinding: req.PreserveStickyBinding}, AttemptSelectionPorts{
		Acquire: func(ctx context.Context, id int64, limit int) (*AcquireResult, bool, error) {
			if s.concurrency != nil && limit > 0 && !budget.recordAcquire(id) {
				return nil, false, nil
			}
			v, err := s.ports.Acquire(ctx, id, limit)
			return v, true, err
		},
		Fresh: func(ctx context.Context, c SelectionCandidate) (SelectionCandidate, bool) {
			a := s.ports.Fresh(ctx, records[c.ProjectionID], req.Platform, req.routingModel(), false, req.RequiredCapability)
			if a == nil || !s.ports.TransportCompatible(a, req.RequiredTransport) || !s.requestCompatible(ctx, a, req) {
				return SelectionCandidate{}, false
			}
			return project(a), true
		},
		CanRecheck: func() bool { return s.canRecheck(budget) },
		Recheck: func(ctx context.Context, c SelectionCandidate) (SelectionCandidate, bool) {
			a := s.ports.Recheck(ctx, records[c.ProjectionID], req.GroupID, req.Platform, req.routingModel(), false, req.RequiredCapability)
			if a == nil || !s.ports.TransportCompatible(a, req.RequiredTransport) || !s.requestCompatible(ctx, a, req) {
				return SelectionCandidate{}, false
			}
			return project(a), true
		},
		CompactAllowed: func(c SelectionCandidate) bool { return s.ports.CompactAllowed(records[c.ProjectionID]) },
		BindSticky: func(ctx context.Context, c SelectionCandidate) {
			_ = s.ports.BindSticky(ctx, req.GroupID, req.SessionHash, c.Snapshot.ID)
		},
	})
	if err != nil || !selected {
		owner.Release()
		return nil, output.CompactBlocked, err
	}
	return &FlowSelection{Account: records[output.Candidate.ProjectionID], Acquired: true, ReleaseFunc: owner.Release}, output.CompactBlocked, nil
}

// CandidatesSnapshot 用于只读诊断和原行为夹具，不修改排序或共享反馈。
func (p PlatformLoadPlan) CandidatesSnapshot() []PlatformCandidateScore {
	return append([]PlatformCandidateScore(nil), p.candidates...)
}
