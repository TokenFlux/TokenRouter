package service

import (
	"context"
	"time"
)

// GroupCapacitySummary holds aggregated capacity for a single group.
type GroupCapacitySummary struct {
	GroupID         int64 `json:"group_id"`
	ConcurrencyUsed int   `json:"concurrency_used"`
	ConcurrencyMax  int   `json:"concurrency_max"`
	SessionsUsed    int   `json:"sessions_used"`
	SessionsMax     int   `json:"sessions_max"`
	RPMUsed         int   `json:"rpm_used"`
	RPMMax          int   `json:"rpm_max"`
}

// GroupCapacityAccount is the lightweight account shape needed to aggregate
// marketplace/group capacity without loading credentials, proxies or group
// edges for every schedulable account.
type GroupCapacityAccount struct {
	ID                        int64
	Concurrency               int
	MaxSessions               int
	SessionIdleTimeoutMinutes int
	BaseRPM                   int
}

type batchGroupCapacityAccountRepository interface {
	ListGroupCapacityAccounts(ctx context.Context, groupIDs []int64) (map[int64][]GroupCapacityAccount, error)
}

// GroupCapacityService aggregates per-group capacity from runtime data.
type GroupCapacityService struct {
	accountRepo        AccountRepository
	groupRepo          GroupRepository
	concurrencyService *ConcurrencyService
	sessionLimitCache  SessionLimitCache
	rpmCache           RPMCache
}

// NewGroupCapacityService creates a new GroupCapacityService.
func NewGroupCapacityService(
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrencyService *ConcurrencyService,
	sessionLimitCache SessionLimitCache,
	rpmCache RPMCache,
) *GroupCapacityService {
	return &GroupCapacityService{
		accountRepo:        accountRepo,
		groupRepo:          groupRepo,
		concurrencyService: concurrencyService,
		sessionLimitCache:  sessionLimitCache,
		rpmCache:           rpmCache,
	}
}

// GetAllGroupCapacity returns capacity summary for all active groups.
func (s *GroupCapacityService) GetAllGroupCapacity(ctx context.Context) ([]GroupCapacitySummary, error) {
	groups, err := s.groupRepo.ListActive(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]GroupCapacitySummary, 0, len(groups))
	for i := range groups {
		cap, err := s.getGroupCapacity(ctx, groups[i].ID)
		if err != nil {
			// Skip groups with errors, return partial results
			continue
		}
		cap.GroupID = groups[i].ID
		results = append(results, cap)
	}
	return results, nil
}

// GetGroupCapacityByIDs 返回指定分组的容量摘要；单个分组失败时跳过，避免影响其它分组展示。
func (s *GroupCapacityService) GetGroupCapacityByIDs(ctx context.Context, groupIDs []int64) (map[int64]GroupCapacitySummary, error) {
	results := make(map[int64]GroupCapacitySummary, len(groupIDs))
	if s == nil || len(groupIDs) == 0 {
		return results, nil
	}

	uniqueGroupIDs := dedupePositiveGroupIDs(groupIDs)
	if len(uniqueGroupIDs) == 0 {
		return results, nil
	}
	if err := ctx.Err(); err != nil {
		return results, err
	}

	if batchRepo, ok := s.accountRepo.(batchGroupCapacityAccountRepository); ok {
		accountsByGroup, err := batchRepo.ListGroupCapacityAccounts(ctx, uniqueGroupIDs)
		if err == nil {
			return s.aggregateBatchGroupCapacity(ctx, uniqueGroupIDs, accountsByGroup), nil
		}
	}

	for _, groupID := range uniqueGroupIDs {
		if err := ctx.Err(); err != nil {
			return results, err
		}

		capacity, err := s.getGroupCapacity(ctx, groupID)
		if err != nil {
			continue
		}
		capacity.GroupID = groupID
		results[groupID] = capacity
	}

	return results, nil
}

func dedupePositiveGroupIDs(groupIDs []int64) []int64 {
	seen := make(map[int64]struct{}, len(groupIDs))
	unique := make([]int64, 0, len(groupIDs))
	for _, groupID := range groupIDs {
		if groupID <= 0 {
			continue
		}
		if _, ok := seen[groupID]; ok {
			continue
		}
		seen[groupID] = struct{}{}
		unique = append(unique, groupID)
	}
	return unique
}

func (s *GroupCapacityService) aggregateBatchGroupCapacity(ctx context.Context, groupIDs []int64, accountsByGroup map[int64][]GroupCapacityAccount) map[int64]GroupCapacitySummary {
	results := make(map[int64]GroupCapacitySummary, len(groupIDs))
	if len(groupIDs) == 0 || len(accountsByGroup) == 0 {
		return results
	}

	accountIDs := make([]int64, 0)
	sessionTimeouts := make(map[int64]time.Duration)
	accountGroupIDs := make(map[int64][]int64)
	for _, groupID := range groupIDs {
		accounts := accountsByGroup[groupID]
		if len(accounts) == 0 {
			continue
		}
		summary := GroupCapacitySummary{GroupID: groupID}
		for _, acc := range accounts {
			if acc.ID <= 0 {
				continue
			}
			accountIDs = append(accountIDs, acc.ID)
			accountGroupIDs[acc.ID] = append(accountGroupIDs[acc.ID], groupID)
			summary.ConcurrencyMax += acc.Concurrency
			if acc.MaxSessions > 0 {
				summary.SessionsMax += acc.MaxSessions
				timeoutMinutes := acc.SessionIdleTimeoutMinutes
				if timeoutMinutes <= 0 {
					timeoutMinutes = 5
				}
				sessionTimeouts[acc.ID] = time.Duration(timeoutMinutes) * time.Minute
			}
			if acc.BaseRPM > 0 {
				summary.RPMMax += acc.BaseRPM
			}
		}
		results[groupID] = summary
	}
	if len(accountIDs) == 0 {
		return results
	}

	concurrencyMap := map[int64]int{}
	if s.concurrencyService != nil {
		concurrencyMap, _ = s.concurrencyService.GetAccountConcurrencyBatch(ctx, accountIDs)
	}

	var sessionsMap map[int64]int
	if len(sessionTimeouts) > 0 && s.sessionLimitCache != nil {
		sessionsMap, _ = s.sessionLimitCache.GetActiveSessionCountBatch(ctx, accountIDs, sessionTimeouts)
	}

	var rpmMap map[int64]int
	if s.rpmCache != nil {
		rpmMap, _ = s.rpmCache.GetRPMBatch(ctx, accountIDs)
	}

	for _, accountID := range accountIDs {
		for _, groupID := range accountGroupIDs[accountID] {
			summary := results[groupID]
			summary.ConcurrencyUsed += concurrencyMap[accountID]
			if sessionsMap != nil {
				summary.SessionsUsed += sessionsMap[accountID]
			}
			if rpmMap != nil {
				summary.RPMUsed += rpmMap[accountID]
			}
			results[groupID] = summary
		}
	}

	return results
}

func (s *GroupCapacityService) getGroupCapacity(ctx context.Context, groupID int64) (GroupCapacitySummary, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return GroupCapacitySummary{}, err
	}
	if len(accounts) == 0 {
		return GroupCapacitySummary{}, nil
	}

	// Collect account IDs and config values
	accountIDs := make([]int64, 0, len(accounts))
	sessionTimeouts := make(map[int64]time.Duration)
	var concurrencyMax, sessionsMax, rpmMax int

	for i := range accounts {
		acc := &accounts[i]
		accountIDs = append(accountIDs, acc.ID)
		concurrencyMax += acc.Concurrency

		if ms := acc.GetMaxSessions(); ms > 0 {
			sessionsMax += ms
			timeout := time.Duration(acc.GetSessionIdleTimeoutMinutes()) * time.Minute
			if timeout <= 0 {
				timeout = 5 * time.Minute
			}
			sessionTimeouts[acc.ID] = timeout
		}

		if rpm := acc.GetBaseRPM(); rpm > 0 {
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
