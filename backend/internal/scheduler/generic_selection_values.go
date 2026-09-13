package scheduler

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// 以下函数只转换基础与高级评分的独立投影，不保留第二套算法。
func flowBasic(a *FlowAccount) *BasicAccount {
	if a == nil {
		return nil
	}
	return &BasicAccount{ID: a.ID, Type: a.Type, Priority: a.Priority, LastUsedAt: a.LastUsedAt, SessionWindowEnd: a.SessionWindowEnd}
}
func flowBasics(values []*FlowAccount) ([]*BasicAccount, map[*BasicAccount]*FlowAccount) {
	out := make([]*BasicAccount, len(values))
	m := make(map[*BasicAccount]*FlowAccount, len(values))
	for i, a := range values {
		out[i] = flowBasic(a)
		m[out[i]] = a
	}
	return out, m
}
func flowBasicLoads(values []FlowLoad) ([]BasicCandidate, map[*BasicAccount]FlowLoad) {
	out := make([]BasicCandidate, len(values))
	m := make(map[*BasicAccount]FlowLoad, len(values))
	for i, a := range values {
		v := flowBasic(a.Account)
		out[i] = BasicCandidate{Account: v, Load: a.LoadInfo}
		m[v] = a
	}
	return out, m
}
func restoreFlowLoads(values []BasicCandidate, m map[*BasicAccount]FlowLoad) []FlowLoad {
	if values == nil {
		return nil
	}
	out := make([]FlowLoad, len(values))
	for i, a := range values {
		out[i] = m[a.Account]
	}
	return out
}
func flowFilterByMinPriority(values []FlowLoad) []FlowLoad {
	v, m := flowBasicLoads(values)
	return restoreFlowLoads(FilterByMinPriority(v), m)
}
func flowFilterByMinLoadRate(values []FlowLoad) []FlowLoad {
	v, m := flowBasicLoads(values)
	return restoreFlowLoads(FilterByMinLoadRate(v), m)
}
func flowFilterBySoonestReset(values []FlowLoad, now func() time.Time) []FlowLoad {
	v, m := flowBasicLoads(values)
	return restoreFlowLoads(FilterBySoonestReset(v, now), m)
}
func flowSelectByLRU(values []FlowLoad, preferOAuth bool) *FlowLoad {
	v, _ := flowBasicLoads(values)
	selected := SelectByLRU(v, preferOAuth)
	if selected == nil {
		return nil
	}
	for i := range v {
		if v[i].Account == selected.Account {
			return &values[i]
		}
	}
	return nil
}
func flowShuffleWithinSortGroups(values []FlowLoad) {
	v, m := flowBasicLoads(values)
	ShuffleWithinSortGroups(v)
	copy(values, restoreFlowLoads(v, m))
}
func flowSortAccountsByPriorityAndLastUsed(values []*FlowAccount, preferOAuth bool) {
	v, m := flowBasics(values)
	SortAccountsByPriorityAndLastUsed(v, preferOAuth)
	for i, a := range v {
		values[i] = m[a]
	}
}
func flowSortAccountsByPriorityOnly(values []*FlowAccount, preferOAuth bool) {
	v, m := flowBasics(values)
	SortAccountsByPriorityOnly(v, preferOAuth)
	for i, a := range v {
		values[i] = m[a]
	}
}
func flowShuffleWithinPriority(values []*FlowAccount, now func() time.Time) {
	v, m := flowBasics(values)
	ShuffleWithinPriority(v, now)
	for i, a := range v {
		values[i] = m[a]
	}
}

type flowScore struct {
	Account *FlowAccount
	score   CandidateScore
}

func flowScoreCandidates(values []*FlowAccount, loads map[int64]*AccountLoadInfo, stats *RuntimeStats, weights policy.ScoreWeights, input ScoreInput, now time.Time) ([]flowScore, float64) {
	projected := make([]*ScoreAccount, len(values))
	source := map[*ScoreAccount]*FlowAccount{}
	for i, a := range values {
		if a != nil {
			projected[i] = &ScoreAccount{ID: a.ID, Name: a.Name, Platform: a.Platform, Priority: a.Priority, SessionWindowEnd: a.SessionWindowEnd}
			source[projected[i]] = a
		}
	}
	scores, skew := ScoreCandidates(projected, loads, stats, weights, input, now)
	out := make([]flowScore, len(scores))
	for i, c := range scores {
		out[i] = flowScore{Account: source[c.Account], score: c}
	}
	return out, skew
}
func flowBuildSelectionOrder(values []flowScore, input ScoreInput) []flowScore {
	scores := make([]CandidateScore, len(values))
	source := map[*ScoreAccount]*FlowAccount{}
	for i, v := range values {
		scores[i] = v.score
		source[v.score.Account] = v.Account
	}
	order := BuildSelectionOrder(scores, input)
	out := make([]flowScore, len(order))
	for i, c := range order {
		out[i] = flowScore{Account: source[c.Account], score: c}
	}
	return out
}
