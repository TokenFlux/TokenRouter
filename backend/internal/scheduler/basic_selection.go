package scheduler

import (
	mathrand "math/rand"
	"sort"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// BasicAccount 仅包含原基础排序使用的身份与观测；候选不能读取执行凭据。
type BasicAccount struct {
	ID               int64
	Type             string
	Priority         int
	LastUsedAt       *time.Time
	SessionWindowEnd *time.Time
}
type BasicCandidate struct {
	Account *BasicAccount
	Load    *AccountLoadInfo
}

func FilterByMinPriority(accounts []BasicCandidate) []BasicCandidate {
	if len(accounts) == 0 {
		return accounts
	}
	minPriority := accounts[0].Account.Priority
	for _, acc := range accounts[1:] {
		if acc.Account.Priority < minPriority {
			minPriority = acc.Account.Priority
		}
	}
	result := make([]BasicCandidate, 0, len(accounts))
	for _, acc := range accounts {
		if acc.Account.Priority == minPriority {
			result = append(result, acc)
		}
	}
	return result
}

func FilterByMinLoadRate(accounts []BasicCandidate) []BasicCandidate {
	if len(accounts) == 0 {
		return accounts
	}
	minLoadRate := accounts[0].Load.LoadRate
	for _, acc := range accounts[1:] {
		if acc.Load.LoadRate < minLoadRate {
			minLoadRate = acc.Load.LoadRate
		}
	}
	result := make([]BasicCandidate, 0, len(accounts))
	for _, acc := range accounts {
		if acc.Load.LoadRate == minLoadRate {
			result = append(result, acc)
		}
	}
	return result
}

func FilterBySoonestReset(accounts []BasicCandidate, now func() time.Time) []BasicCandidate {
	if len(accounts) <= 1 {
		return accounts
	}
	instant := now()
	var minEnd *time.Time
	for _, acc := range accounts {
		end := acc.Account.SessionWindowEnd
		if end == nil || !instant.Before(*end) {
			continue
		}
		if minEnd == nil || end.Before(*minEnd) {
			minEnd = end
		}
	}
	if minEnd == nil {
		// 没有任何账号拥有活跃窗口，保持原集合
		return accounts
	}
	result := make([]BasicCandidate, 0, len(accounts))
	for _, acc := range accounts {
		end := acc.Account.SessionWindowEnd
		if end != nil && instant.Before(*end) && end.Equal(*minEnd) {
			result = append(result, acc)
		}
	}
	return result
}

func SelectByLRU(accounts []BasicCandidate, preferOAuth bool) *BasicCandidate {
	if len(accounts) == 0 {
		return nil
	}
	if len(accounts) == 1 {
		return &accounts[0]
	}

	// 1. 找到最小的 LastUsedAt（nil 被视为最小）
	var minTime *time.Time
	hasNil := false
	for _, acc := range accounts {
		if acc.Account.LastUsedAt == nil {
			hasNil = true
			break
		}
		if minTime == nil || acc.Account.LastUsedAt.Before(*minTime) {
			minTime = acc.Account.LastUsedAt
		}
	}

	// 2. 收集所有具有最小 LastUsedAt 的账号索引
	var candidateIdxs []int
	for i, acc := range accounts {
		if hasNil {
			if acc.Account.LastUsedAt == nil {
				candidateIdxs = append(candidateIdxs, i)
			}
		} else {
			if acc.Account.LastUsedAt != nil && acc.Account.LastUsedAt.Equal(*minTime) {
				candidateIdxs = append(candidateIdxs, i)
			}
		}
	}

	// 3. 如果只有一个候选，直接返回
	if len(candidateIdxs) == 1 {
		return &accounts[candidateIdxs[0]]
	}

	// 4. 如果有多个候选且 preferOAuth，优先选择 OAuth 类型
	if preferOAuth {
		var oauthIdxs []int
		for _, idx := range candidateIdxs {
			if accounts[idx].Account.Type == capability.AccountTypeOAuth {
				oauthIdxs = append(oauthIdxs, idx)
			}
		}
		if len(oauthIdxs) > 0 {
			candidateIdxs = oauthIdxs
		}
	}

	// 5. 随机选择一个
	selectedIdx := candidateIdxs[mathrand.Intn(len(candidateIdxs))]
	return &accounts[selectedIdx]
}

func SortAccountsByPriorityAndLastUsed(accounts []*BasicAccount, preferOAuth bool) {
	sort.SliceStable(accounts, func(i, j int) bool {
		a, b := accounts[i], accounts[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		switch {
		case a.LastUsedAt == nil && b.LastUsedAt != nil:
			return true
		case a.LastUsedAt != nil && b.LastUsedAt == nil:
			return false
		case a.LastUsedAt == nil && b.LastUsedAt == nil:
			if preferOAuth && a.Type != b.Type {
				return a.Type == capability.AccountTypeOAuth
			}
			return false
		default:
			return a.LastUsedAt.Before(*b.LastUsedAt)
		}
	})
	ShuffleWithinPriorityAndLastUsed(accounts, preferOAuth)
}

func ShuffleWithinSortGroups(accounts []BasicCandidate) {
	if len(accounts) <= 1 {
		return
	}
	i := 0
	for i < len(accounts) {
		j := i + 1
		for j < len(accounts) && SameAccountWithLoadGroup(accounts[i], accounts[j]) {
			j++
		}
		if j-i > 1 {
			mathrand.Shuffle(j-i, func(a, b int) {
				accounts[i+a], accounts[i+b] = accounts[i+b], accounts[i+a]
			})
		}
		i = j
	}
}

func SameAccountWithLoadGroup(a, b BasicCandidate) bool {
	if a.Account.Priority != b.Account.Priority {
		return false
	}
	if a.Load.LoadRate != b.Load.LoadRate {
		return false
	}
	return SameLastUsedAt(a.Account.LastUsedAt, b.Account.LastUsedAt)
}

func ShuffleWithinPriorityAndLastUsed(accounts []*BasicAccount, preferOAuth bool) {
	if len(accounts) <= 1 {
		return
	}
	i := 0
	for i < len(accounts) {
		j := i + 1
		for j < len(accounts) && SameAccountGroup(accounts[i], accounts[j]) {
			j++
		}
		if j-i > 1 {
			if preferOAuth {
				oauth := make([]*BasicAccount, 0, j-i)
				others := make([]*BasicAccount, 0, j-i)
				for _, acc := range accounts[i:j] {
					if acc.Type == capability.AccountTypeOAuth {
						oauth = append(oauth, acc)
					} else {
						others = append(others, acc)
					}
				}
				if len(oauth) > 1 {
					mathrand.Shuffle(len(oauth), func(a, b int) { oauth[a], oauth[b] = oauth[b], oauth[a] })
				}
				if len(others) > 1 {
					mathrand.Shuffle(len(others), func(a, b int) { others[a], others[b] = others[b], others[a] })
				}
				copy(accounts[i:], oauth)
				copy(accounts[i+len(oauth):], others)
			} else {
				mathrand.Shuffle(j-i, func(a, b int) {
					accounts[i+a], accounts[i+b] = accounts[i+b], accounts[i+a]
				})
			}
		}
		i = j
	}
}

func SameAccountGroup(a, b *BasicAccount) bool {
	if a.Priority != b.Priority {
		return false
	}
	return SameLastUsedAt(a.LastUsedAt, b.LastUsedAt)
}

func SameLastUsedAt(a, b *time.Time) bool {
	switch {
	case a == nil && b == nil:
		return true
	case a == nil || b == nil:
		return false
	default:
		return a.Unix() == b.Unix()
	}
}

func SortAccountsByPriorityOnly(accounts []*BasicAccount, preferOAuth bool) {
	sort.SliceStable(accounts, func(i, j int) bool {
		a, b := accounts[i], accounts[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		if preferOAuth && a.Type != b.Type {
			return a.Type == capability.AccountTypeOAuth
		}
		return false
	})
}

func ShuffleWithinPriority(accounts []*BasicAccount, now func() time.Time) {
	if len(accounts) <= 1 {
		return
	}
	r := mathrand.New(mathrand.NewSource(now().UnixNano()))
	start := 0
	for start < len(accounts) {
		priority := accounts[start].Priority
		end := start + 1
		for end < len(accounts) && accounts[end].Priority == priority {
			end++
		}
		// 对 [start, end) 范围内的账户随机打乱
		if end-start > 1 {
			r.Shuffle(end-start, func(i, j int) {
				accounts[start+i], accounts[start+j] = accounts[start+j], accounts[start+i]
			})
		}
		start = end
	}
}
