// 本文件维护 routing 的所属能力；兼容入口复用唯一实现。
package routing

import (
	context "context"
	slices "slices"
	time "time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

type GroupCapacitySummary = accessview.GroupCapacitySummary

// GetAllGroupCapacity 返回全部活跃分组的容量摘要。
func (s *CapacityService) GetAllGroupCapacity(ctx context.Context) ([]GroupCapacitySummary, error) {
	groupIDs, err := s.groupRepo.ListActiveIDs(ctx)
	if err != nil {
		return nil, err
	}

	if lister, ok := s.accountRepo.(CapacityBatchAccounts); ok {
		return s.getGroupCapacitiesBatch(ctx, groupIDs, lister)
	}

	return s.getGroupCapacitiesSequential(ctx, groupIDs), nil
}

func (s *CapacityService) getGroupCapacitiesSequential(ctx context.Context, groupIDs []int64) []GroupCapacitySummary {
	results := make([]GroupCapacitySummary, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		cap, err := s.GetGroupCapacity(ctx, groupID)
		if err != nil {
			// 单个分组失败时跳过，保留其它分组的部分结果。
			continue
		}
		cap.GroupID = groupID
		results = append(results, cap)
	}
	return results
}

type groupCapacityAccountRef struct {
	groupID   int64
	accountID int64
}

func (s *CapacityService) getGroupCapacitiesBatch(ctx context.Context, groupIDs []int64, lister CapacityBatchAccounts) ([]GroupCapacitySummary, error) {
	results := make([]GroupCapacitySummary, len(groupIDs))
	groupIndex := make(map[int64]int, len(groupIDs))
	for i, groupID := range groupIDs {
		results[i].GroupID = groupID
		groupIndex[groupID] = i
	}
	if len(groupIDs) == 0 {
		return results, nil
	}

	rows, err := lister.ListSchedulableCapacityByGroupIDs(ctx, groupIDs)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return results, nil
	}

	refs := make([]groupCapacityAccountRef, 0, len(rows))
	seenGroupAccount := make(map[groupCapacityAccountRef]struct{}, len(rows))
	accountIDSet := make(map[int64]struct{}, len(rows))
	accountIDs := make([]int64, 0, len(rows))
	sessionTimeouts := make(map[int64]time.Duration)

	for _, row := range rows {
		idx, ok := groupIndex[row.GroupID]
		if !ok || row.Account.ID <= 0 {
			continue
		}

		acc := row.Account
		if acc.QuotaAutoPaused {
			continue
		}

		ref := groupCapacityAccountRef{groupID: row.GroupID, accountID: row.Account.ID}
		if _, ok := seenGroupAccount[ref]; ok {
			continue
		}
		seenGroupAccount[ref] = struct{}{}
		refs = append(refs, ref)

		if _, ok := accountIDSet[row.Account.ID]; !ok {
			accountIDSet[row.Account.ID] = struct{}{}
			accountIDs = append(accountIDs, row.Account.ID)
		}

		results[idx].ConcurrencyMax += acc.Concurrency

		if maxSessions := acc.MaxSessions; maxSessions > 0 {
			results[idx].SessionsMax += maxSessions
			timeout := time.Duration(acc.SessionIdleTimeoutMinutes) * time.Minute
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			sessionTimeouts[acc.ID] = timeout
		}

		if rpm := acc.BaseRPM; rpm > 0 {
			results[idx].RPMMax += rpm
		}
	}

	if len(accountIDs) == 0 {
		return results, nil
	}

	concurrencyMap := map[int64]int{}
	if s.concurrencyService != nil {
		concurrencyMap, _ = s.concurrencyService.GetAccountConcurrencyBatch(ctx, accountIDs)
	}

	sessionAccountIDs := accountIDsForGroupsWithLimit(refs, groupIndex, results, func(summary GroupCapacitySummary) bool {
		return summary.SessionsMax > 0
	})
	var sessionsMap map[int64]int
	if len(sessionAccountIDs) > 0 && s.sessionLimitCache != nil {
		sessionsMap, _ = s.sessionLimitCache.GetActiveSessionCountBatch(ctx, sessionAccountIDs, sessionTimeouts)
	}

	rpmAccountIDs := accountIDsForGroupsWithLimit(refs, groupIndex, results, func(summary GroupCapacitySummary) bool {
		return summary.RPMMax > 0
	})
	var rpmMap map[int64]int
	if len(rpmAccountIDs) > 0 && s.rpmCache != nil {
		rpmMap, _ = s.rpmCache.GetRPMBatch(ctx, rpmAccountIDs)
	}

	for _, ref := range refs {
		idx := groupIndex[ref.groupID]
		results[idx].ConcurrencyUsed += concurrencyMap[ref.accountID]
		if sessionsMap != nil && results[idx].SessionsMax > 0 {
			results[idx].SessionsUsed += sessionsMap[ref.accountID]
		}
		if rpmMap != nil && results[idx].RPMMax > 0 {
			results[idx].RPMUsed += rpmMap[ref.accountID]
		}
	}
	return results, nil
}

// GetGroupCapacityByIDs 返回指定分组的容量摘要；仓储支持批量投影时只执行一次聚合查询。
func (s *CapacityService) GetGroupCapacityByIDs(ctx context.Context, groupIDs []int64) (map[int64]GroupCapacitySummary, error) {
	results := make(map[int64]GroupCapacitySummary, len(groupIDs))
	if s == nil || len(groupIDs) == 0 {
		return results, nil
	}

	normalized := make([]int64, 0, len(groupIDs))
	seen := make(map[int64]struct{}, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}

		if err := ctx.Err(); err != nil {
			return results, err
		}
		normalized = append(normalized, groupID)
	}

	if lister, ok := s.accountRepo.(CapacityBatchAccounts); ok {
		summaries, err := s.getGroupCapacitiesBatch(ctx, normalized, lister)
		if err != nil {
			return results, err
		}
		for _, summary := range summaries {
			results[summary.GroupID] = summary
		}
		return results, nil
	}

	for _, groupID := range normalized {
		capacity, err := s.GetGroupCapacity(ctx, groupID)
		if err != nil {
			continue
		}
		capacity.GroupID = groupID
		results[groupID] = capacity
	}

	return results, nil
}

func accountIDsForGroupsWithLimit(refs []groupCapacityAccountRef, groupIndex map[int64]int, summaries []GroupCapacitySummary, include func(GroupCapacitySummary) bool) []int64 {
	seen := make(map[int64]struct{})
	accountIDs := make([]int64, 0)
	for _, ref := range refs {
		idx, ok := groupIndex[ref.groupID]
		if !ok || !include(summaries[idx]) {
			continue
		}
		if _, ok := seen[ref.accountID]; ok {
			continue
		}
		seen[ref.accountID] = struct{}{}
		accountIDs = append(accountIDs, ref.accountID)
	}
	return accountIDs
}

func (s *CapacityService) GetGroupCapacity(ctx context.Context, groupID int64) (GroupCapacitySummary, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return GroupCapacitySummary{}, err
	}
	if len(accounts) == 0 {
		return GroupCapacitySummary{}, nil
	}
	accounts = slices.DeleteFunc(accounts, func(a account.CapacitySnapshot) bool { return a.QuotaAutoPaused })
	if len(accounts) == 0 {
		return GroupCapacitySummary{}, nil
	}

	// 收集账号 ID 和容量配置。
	accountIDs := make([]int64, 0, len(accounts))
	sessionTimeouts := make(map[int64]time.Duration)
	var concurrencyMax, sessionsMax, rpmMax int

	for i := range accounts {
		acc := &accounts[i]
		accountIDs = append(accountIDs, acc.ID)
		concurrencyMax += acc.Concurrency

		if ms := acc.MaxSessions; ms > 0 {
			sessionsMax += ms
			timeout := time.Duration(acc.SessionIdleTimeoutMinutes) * time.Minute
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			sessionTimeouts[acc.ID] = timeout
		}

		if rpm := acc.BaseRPM; rpm > 0 {
			rpmMax += rpm
		}
	}

	// 批量查询运行时容量数据；缓存异常只影响当前指标，不阻断容量展示。
	concurrencyMap := map[int64]int{}
	if s.concurrencyService != nil {
		concurrencyMap, _ = s.concurrencyService.GetAccountConcurrencyBatch(ctx, accountIDs)
	}

	var sessionsMap map[int64]int
	if sessionsMax > 0 && s.sessionLimitCache != nil {
		sessionsMap, _ = s.sessionLimitCache.GetActiveSessionCountBatch(ctx, accountIDs, sessionTimeouts)
	}

	var rpmMap map[int64]int
	if rpmMax > 0 && s.rpmCache != nil {
		rpmMap, _ = s.rpmCache.GetRPMBatch(ctx, accountIDs)
	}

	// 聚合账号级容量为分组级容量。
	var concurrencyUsed, sessionsUsed, rpmUsed int
	for _, id := range accountIDs {
		concurrencyUsed += concurrencyMap[id]
		if sessionsMap != nil {
			sessionsUsed += sessionsMap[id]
		}
		if rpmMap != nil {
			rpmUsed += rpmMap[id]
		}
	}

	return GroupCapacitySummary{
		ConcurrencyUsed: concurrencyUsed,
		ConcurrencyMax:  concurrencyMax,
		SessionsUsed:    sessionsUsed,
		SessionsMax:     sessionsMax,
		RPMUsed:         rpmUsed,
		RPMMax:          rpmMax,
	}, nil
}

type CapacityAccountRow struct {
	GroupID int64
	Account account.CapacitySnapshot
}
type CapacityAccounts interface {
	ListSchedulableByGroupID(context.Context, int64) ([]account.CapacitySnapshot, error)
}
type CapacityBatchAccounts interface {
	ListSchedulableCapacityByGroupIDs(context.Context, []int64) ([]CapacityAccountRow, error)
}
type CapacityGroups interface {
	ListActiveIDs(context.Context) ([]int64, error)
}
type CapacityConcurrency interface {
	GetAccountConcurrencyBatch(context.Context, []int64) (map[int64]int, error)
}
type CapacitySessions interface {
	GetActiveSessionCountBatch(context.Context, []int64, map[int64]time.Duration) (map[int64]int, error)
}
type CapacityRPM interface {
	GetRPMBatch(context.Context, []int64) (map[int64]int, error)
}

// CapacityService 只聚合已投影的账号配置及运行计数，不持有账号或计数缓存。
type CapacityService struct {
	accountRepo        CapacityAccounts
	groupRepo          CapacityGroups
	concurrencyService CapacityConcurrency
	sessionLimitCache  CapacitySessions
	rpmCache           CapacityRPM
}

func NewCapacityService(accounts CapacityAccounts, groups CapacityGroups, concurrency CapacityConcurrency, sessions CapacitySessions, rpm CapacityRPM) *CapacityService {
	return &CapacityService{accountRepo: accounts, groupRepo: groups, concurrencyService: concurrency, sessionLimitCache: sessions, rpmCache: rpm}
}
