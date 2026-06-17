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

// OpenAIQuotaAutoPauseSettingsReader 提供 OpenAI 配额自动暂停的全局默认阈值。
type OpenAIQuotaAutoPauseSettingsReader interface {
	GetOpenAIQuotaAutoPauseSettings(ctx context.Context) OpsOpenAIAccountQuotaAutoPauseSettings
}

// GroupCapacityService aggregates per-group capacity from runtime data.
type GroupCapacityService struct {
	accountRepo        AccountRepository
	groupRepo          GroupRepository
	concurrencyService *ConcurrencyService
	sessionLimitCache  SessionLimitCache
	rpmCache           RPMCache
	quotaAutoPause     OpenAIQuotaAutoPauseSettingsReader
}

// NewGroupCapacityService creates a new GroupCapacityService.
func NewGroupCapacityService(
	accountRepo AccountRepository,
	groupRepo GroupRepository,
	concurrencyService *ConcurrencyService,
	sessionLimitCache SessionLimitCache,
	rpmCache RPMCache,
	quotaAutoPause OpenAIQuotaAutoPauseSettingsReader,
) *GroupCapacityService {
	return &GroupCapacityService{
		accountRepo:        accountRepo,
		groupRepo:          groupRepo,
		concurrencyService: concurrencyService,
		sessionLimitCache:  sessionLimitCache,
		rpmCache:           rpmCache,
		quotaAutoPause:     quotaAutoPause,
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

		capacity, err := s.getGroupCapacity(ctx, groupID)
		if err != nil {
			continue
		}
		capacity.GroupID = groupID
		results[groupID] = capacity
	}

	return results, nil
}

func (s *GroupCapacityService) getGroupCapacity(ctx context.Context, groupID int64) (GroupCapacitySummary, error) {
	accounts, err := s.accountRepo.ListSchedulableByGroupID(ctx, groupID)
	if err != nil {
		return GroupCapacitySummary{}, err
	}
	if len(accounts) == 0 {
		return GroupCapacitySummary{}, nil
	}
	accounts = s.filterQuotaAutoPausedAccounts(ctx, accounts)
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

func (s *GroupCapacityService) filterQuotaAutoPausedAccounts(ctx context.Context, accounts []Account) []Account {
	if len(accounts) == 0 {
		return accounts
	}
	if s != nil && s.quotaAutoPause != nil {
		ctx = WithOpenAIQuotaAutoPauseSettings(ctx, s.quotaAutoPause.GetOpenAIQuotaAutoPauseSettings(ctx))
	}
	filtered := accounts[:0]
	for i := range accounts {
		account := &accounts[i]
		// OpenAI 配额自动暂停不落库，容量统计必须复用同一套实时派生规则过滤。
		if EvaluateOpenAIQuotaAutoPause(ctx, account) {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}
