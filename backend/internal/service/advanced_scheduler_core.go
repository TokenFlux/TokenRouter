// 评分、抽样和运行反馈唯一位于 scheduler；本文件只保留旧值投影及调用委托。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

func withAdvancedSchedulerNoSlotSelection(ctx context.Context) context.Context {
	return scheduler.WithSelectOnly(ctx)
}
func isAdvancedSchedulerNoSlotSelection(ctx context.Context) bool { return scheduler.IsSelectOnly(ctx) }

type advancedAccountRuntimeStats struct{ core *scheduler.RuntimeStats }
type advancedSchedulerFeedbackConfig struct {
	errorRateAlpha float64
	ttftAlpha      float64
}
type advancedAccountRuntimeFeedbackSnapshot = scheduler.FeedbackSnapshot
type advancedSchedulerCandidateFactors = scheduler.CandidateFactors
type advancedSchedulerScoreRanges = scheduler.ScoreRanges
type AdvancedAccountSchedulerScoreSnapshot = scheduler.ScoreSnapshot

const (
	defaultAdvancedSchedulerErrorRateAlpha = scheduler.DefaultErrorRateAlpha
	defaultAdvancedSchedulerTTFTAlpha      = scheduler.DefaultTTFTAlpha
)

func newAdvancedAccountRuntimeStats() *advancedAccountRuntimeStats {
	return &advancedAccountRuntimeStats{core: scheduler.NewRuntimeStats(time.Now)}
}
func schedulerStats(s *advancedAccountRuntimeStats) *scheduler.RuntimeStats {
	if s == nil {
		return nil
	}
	return s.core
}
func (s *advancedAccountRuntimeStats) reportSwitch() { schedulerStats(s).ReportSwitch() }
func (s *advancedAccountRuntimeStats) report(id int64, success bool, ttft *int, feedback ...advancedSchedulerFeedbackConfig) {
	values := make([]policy.FeedbackConfig, len(feedback))
	for i, v := range feedback {
		values[i] = policy.FeedbackConfig{ErrorRateAlpha: v.errorRateAlpha, TtftAlpha: v.ttftAlpha}
	}
	schedulerStats(s).Report(id, success, ttft, values...)
}
func (s *advancedAccountRuntimeStats) snapshot(id int64) (float64, float64, bool) {
	return schedulerStats(s).Snapshot(id)
}
func (s *advancedAccountRuntimeStats) feedbackSnapshot(id int64) advancedAccountRuntimeFeedbackSnapshot {
	return schedulerStats(s).FeedbackSnapshot(id)
}
func (s *advancedAccountRuntimeStats) size() int { return schedulerStats(s).Size() }

type advancedSchedulerCandidateScore struct {
	account            *Account
	loadInfo           *AccountLoadInfo
	loadKnown          bool
	score              float64
	baseScore          float64
	stickyBonus        float64
	previousBonus      float64
	sessionStickyBonus float64
	priority           int
	errorRate          float64
	ttft               float64
	hasTTFT            bool
	hasFeedback        bool
	feedback           advancedAccountRuntimeFeedbackSnapshot
	factors            advancedSchedulerCandidateFactors
}
type advancedSchedulerSelectionInput struct {
	GroupID                 *int64
	SessionHash             string
	PreviousResponseID      string
	RequestedModel          string
	StickyAccountID         int64
	StickyPreviousAccountID int64
	StickyWeighted          bool
	TopK                    int
	QuotaHeadroomFactor     func(*Account, time.Time) float64
}

// scoreAccounts 保留逐项指针对应关系，重复 ID 不会合并为另一条候选。
func scoreAccounts(accounts []*Account) ([]*scheduler.ScoreAccount, map[*scheduler.ScoreAccount]*Account) {
	values := make([]*scheduler.ScoreAccount, len(accounts))
	source := make(map[*scheduler.ScoreAccount]*Account, len(accounts))
	for i, a := range accounts {
		if a == nil {
			continue
		}
		values[i] = &scheduler.ScoreAccount{ID: a.ID, Platform: a.Platform, Priority: a.Priority, SessionWindowEnd: a.SessionWindowEnd}
		source[values[i]] = a
	}
	return values, source
}
func scoreInput(input advancedSchedulerSelectionInput, source map[*scheduler.ScoreAccount]*Account) scheduler.ScoreInput {
	value := scheduler.ScoreInput{GroupID: input.GroupID, SessionHash: input.SessionHash, PreviousResponseID: input.PreviousResponseID, RequestedModel: input.RequestedModel, StickyAccountID: input.StickyAccountID, StickyPreviousAccountID: input.StickyPreviousAccountID, StickyWeighted: input.StickyWeighted, TopK: input.TopK, Now: time.Now}
	if input.QuotaHeadroomFactor != nil {
		value.QuotaHeadroomFactor = func(a *scheduler.ScoreAccount, now time.Time) float64 {
			return input.QuotaHeadroomFactor(source[a], now)
		}
	}
	return value
}
func scoresFromCore(values []scheduler.CandidateScore, source map[*scheduler.ScoreAccount]*Account) []advancedSchedulerCandidateScore {
	if values == nil {
		return nil
	}
	out := make([]advancedSchedulerCandidateScore, len(values))
	for i, v := range values {
		out[i] = advancedSchedulerCandidateScore{
			loadInfo: v.LoadInfo, loadKnown: v.LoadKnown, score: v.Score, baseScore: v.BaseScore, stickyBonus: v.StickyBonus, previousBonus: v.PreviousBonus, sessionStickyBonus: v.SessionStickyBonus, priority: v.Priority, errorRate: v.ErrorRate, ttft: v.TTFT, hasTTFT: v.HasTTFT, hasFeedback: v.HasFeedback, feedback: v.Feedback, factors: v.Factors, account: source[v.Account]}
	}
	return out
}
func scoresToCore(values []advancedSchedulerCandidateScore) ([]scheduler.CandidateScore, map[*scheduler.ScoreAccount]*Account) {
	if values == nil {
		return nil, nil
	}
	accounts := make([]*Account, len(values))
	for i, v := range values {
		accounts[i] = v.account
	}
	projected, source := scoreAccounts(accounts)
	out := make([]scheduler.CandidateScore, len(values))
	for i, v := range values {
		out[i] = scheduler.CandidateScore{
			LoadInfo: v.loadInfo, LoadKnown: v.loadKnown, Score: v.score, BaseScore: v.baseScore, StickyBonus: v.stickyBonus, PreviousBonus: v.previousBonus, SessionStickyBonus: v.sessionStickyBonus, Priority: v.priority, ErrorRate: v.errorRate, TTFT: v.ttft, HasTTFT: v.hasTTFT, HasFeedback: v.hasFeedback, Feedback: v.feedback, Factors: v.factors, Account: projected[i]}
	}
	return out, source
}
func scoreAdvancedSchedulerCandidates(accounts []*Account, loads map[int64]*AccountLoadInfo, stats *advancedAccountRuntimeStats, weights GatewayAdvancedSchedulerScoreWeightsView, input advancedSchedulerSelectionInput, now time.Time) ([]advancedSchedulerCandidateScore, float64) {
	out, skew, _ := scoreAdvancedSchedulerCandidatesWithRanges(accounts, loads, stats, weights, input, now)
	return out, skew
}
func scoreAdvancedSchedulerCandidatesWithRanges(accounts []*Account, loads map[int64]*AccountLoadInfo, stats *advancedAccountRuntimeStats, weights GatewayAdvancedSchedulerScoreWeightsView, input advancedSchedulerSelectionInput, now time.Time) ([]advancedSchedulerCandidateScore, float64, advancedSchedulerScoreRanges) {
	projected, source := scoreAccounts(accounts)
	out, skew, ranges := scheduler.ScoreCandidatesWithRanges(projected, loads, schedulerStats(stats), policy.ScoreWeights(weights), scoreInput(input, source), now)
	return scoresFromCore(out, source), skew, ranges
}
func selectTopKAdvancedSchedulerCandidates(values []advancedSchedulerCandidateScore, topK int) []advancedSchedulerCandidateScore {
	out, source := scoresToCore(values)
	return scoresFromCore(scheduler.SelectTopK(out, topK), source)
}

// 比较仅投影两个必要字段，不为排序中的每次比较构造候选映射。
func isAdvancedSchedulerCandidateBetter(left, right advancedSchedulerCandidateScore) bool {
	l, r := scheduler.CandidateScore{Score: left.score}, scheduler.CandidateScore{Score: right.score}
	var la, ra scheduler.ScoreAccount
	if left.account != nil {
		la = scheduler.ScoreAccount{ID: left.account.ID, Priority: left.account.Priority}
		l.Account = &la
	}
	if right.account != nil {
		ra = scheduler.ScoreAccount{ID: right.account.ID, Priority: right.account.Priority}
		r.Account = &ra
	}
	return scheduler.CandidateBetter(l, r)
}

func buildAdvancedWeightedSelectionOrder(values []advancedSchedulerCandidateScore, input advancedSchedulerSelectionInput) []advancedSchedulerCandidateScore {
	out, source := scoresToCore(values)
	return scoresFromCore(scheduler.BuildWeightedSelectionOrder(out, scoreInput(input, source)), source)
}
func buildAdvancedSchedulerSelectionOrder(values []advancedSchedulerCandidateScore, input advancedSchedulerSelectionInput) []advancedSchedulerCandidateScore {
	out, source := scoresToCore(values)
	return scoresFromCore(scheduler.BuildSelectionOrder(out, scoreInput(input, source)), source)
}

type advancedSchedulerRNG struct{ core scheduler.SelectionRNG }

func newAdvancedSchedulerRNG(seed uint64) advancedSchedulerRNG {
	return advancedSchedulerRNG{core: scheduler.NewSelectionRNG(seed)}
}
func (r *advancedSchedulerRNG) nextUint64() uint64   { return r.core.NextUint64() }
func (r *advancedSchedulerRNG) nextFloat64() float64 { return r.core.NextFloat64() }
func deriveAdvancedSchedulerSelectionSeed(input advancedSchedulerSelectionInput) uint64 {
	return scheduler.SelectionSeed(scoreInput(input, nil))
}
func buildAdvancedAccountSchedulerScoreSnapshot(accounts []*Account, loads map[int64]*AccountLoadInfo, stats *advancedAccountRuntimeStats, group *Group, weights GatewayAdvancedSchedulerScoreWeightsView, sticky bool, quota func(*Account, time.Time) float64) map[int64]AdvancedAccountSchedulerScoreSnapshot {
	projected, source := scoreAccounts(accounts)
	var g *scheduler.ScoreGroup
	if group != nil {
		g = &scheduler.ScoreGroup{Platform: group.Platform}
	}
	input := scoreInput(advancedSchedulerSelectionInput{QuotaHeadroomFactor: quota}, source)
	return scheduler.BuildScoreSnapshot(projected, loads, schedulerStats(stats), g, policy.ScoreWeights(weights), sticky, input.QuotaHeadroomFactor, time.Now())
}
