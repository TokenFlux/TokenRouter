// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	"context"
	"sort"

	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
)

// SchedulerScoreAccounts 仅查询管理评分所需的候选集合，保持筛选与分页独立。
type SchedulerScoreAccounts interface {
	ListAccountsForSchedulerScoreFilter(context.Context, string, string, string, string, int64, string) ([]Record, error)
	ListSchedulableAccountsForAdvancedSchedulerScore(context.Context, *int64, string) ([]Record, error)
}
type SchedulerLoadRequest struct {
	ID             int64
	MaxConcurrency int
}
type SchedulerLoad struct {
	AccountID                                  int64
	CurrentConcurrency, WaitingCount, LoadRate int
}

// SchedulerScoreOptions 不持有评分状态，执行端口复用 scheduler 的共享反馈实例。
type SchedulerScoreOptions struct {
	Load  func(context.Context, []SchedulerLoadRequest) (map[int64]*SchedulerLoad, error)
	Score func(context.Context, *accessview.GroupConfig, []*Record, map[int64]*SchedulerLoad) map[int64]AccountSchedulerScore
	Warn  func(string, ...any)
}
type SchedulerScoreView struct {
	adminService SchedulerScoreAccounts
	options      SchedulerScoreOptions
}

func NewSchedulerScoreView(reader SchedulerScoreAccounts, options SchedulerScoreOptions) *SchedulerScoreView {
	if options.Warn == nil {
		options.Warn = func(string, ...any) {}
	}
	return &SchedulerScoreView{reader, options}
}

// scoreAdvancedSchedulerPool 对池内账号计算通用高级调度分数快照。
// loadMap 为共享的账号负载数据（含池内全部账号即可，多余条目无害）；传 nil 时自行批查。
func (h *SchedulerScoreView) scoreAdvancedSchedulerPool(ctx context.Context, group *accessview.GroupConfig, accounts []Record, loadMap map[int64]*SchedulerLoad) map[int64]AccountSchedulerScore {
	if len(accounts) == 0 {
		return nil
	}

	schedulableAccounts := make([]*Record, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if !account.IsSchedulable() {
			continue
		}
		schedulableAccounts = append(schedulableAccounts, account)
	}
	if len(schedulableAccounts) == 0 {
		return nil
	}

	if loadMap == nil {
		loadMap = h.fetchAdvancedSchedulerLoadMap(ctx, schedulableAccounts)
	}

	scores := h.options.Score(ctx, group, schedulableAccounts, loadMap)
	result := make(map[int64]AccountSchedulerScore, len(scores))
	for accountID, score := range scores {
		result[accountID] = AccountSchedulerScore{
			BaseScore:             score.BaseScore,
			StickyScore:           score.StickyScore,
			StickyScoreInfinity:   score.StickyScoreInfinity,
			StickyWeightedEnabled: score.StickyWeightedEnabled,
		}
	}
	return result
}

// fetchAdvancedSchedulerLoadMap 一次性批查给定高级调度候选的负载数据；
// 失败时记录日志并返回空表，评分核心会将缺失负载视为中性信号。
func (h *SchedulerScoreView) fetchAdvancedSchedulerLoadMap(ctx context.Context, accounts []*Record) map[int64]*SchedulerLoad {
	loadMap := map[int64]*SchedulerLoad{}
	if h.options.Load == nil || len(accounts) == 0 {
		return loadMap
	}
	seen := make(map[int64]struct{}, len(accounts))
	loadReq := make([]SchedulerLoadRequest, 0, len(accounts))
	for _, account := range accounts {
		if account == nil {
			continue
		}
		if _, ok := seen[account.ID]; ok {
			continue
		}
		seen[account.ID] = struct{}{}
		loadReq = append(loadReq, SchedulerLoadRequest{
			ID:             account.ID,
			MaxConcurrency: account.EffectiveLoadFactor(),
		})
	}
	if batchLoad, err := h.options.Load(ctx, loadReq); err != nil {
		h.options.Warn("advanced_scheduler_score_load_batch_failed", "error", err)
	} else if batchLoad != nil {
		loadMap = batchLoad
	}
	return loadMap
}

func (h *SchedulerScoreView) buildAdvancedAccountSchedulerScores(
	ctx context.Context,
	accounts []Record,
	filterPool []Record,
) (map[int64]*AccountSchedulerScore, map[int64][]AccountSchedulerGroupScore) {
	if len(accounts) == 0 {
		return nil, nil
	}
	if len(filterPool) == 0 {
		filterPool = accounts
	}

	pageAccountIDs := make(map[int64]struct{})
	advancedGroups := make(map[int64]*accessview.GroupConfig)
	for i := range accounts {
		account := &accounts[i]
		pageAccountIDs[account.ID] = struct{}{}
		if len(account.AccountGroups) == 0 {
			continue
		}
		for _, accountGroup := range account.AccountGroups {
			if accountGroup.GroupID > 0 && accountGroup.Group != nil && accountGroup.Group.SchedulerType == "advanced" {
				advancedGroups[accountGroup.GroupID] = accountGroup.Group
			}
		}
	}
	if len(pageAccountIDs) == 0 {
		return nil, nil
	}

	// 先取各分组池，再对"过滤池 ∪ 分组池"的账号并集做一次负载批查，
	// 避免每个池各查一次 Redis 的 N+1。
	groupIDList := make([]int64, 0, len(advancedGroups))
	for groupID := range advancedGroups {
		groupIDList = append(groupIDList, groupID)
	}
	sort.Slice(groupIDList, func(i, j int) bool { return groupIDList[i] < groupIDList[j] })

	groupPools := make(map[int64][]Record, len(groupIDList))
	if h.adminService != nil {
		for _, groupID := range groupIDList {
			gid := groupID
			group := advancedGroups[gid]
			if group == nil {
				continue
			}
			pool, err := h.adminService.ListSchedulableAccountsForAdvancedSchedulerScore(ctx, &gid, group.Platform)
			if err != nil {
				h.options.Warn("advanced_scheduler_group_score_pool_failed", "group_id", gid, "error", err)
				continue
			}
			groupPools[gid] = pool
		}
	}

	loadUnion := make([]*Record, 0, len(filterPool))
	collectAdvancedAccounts := func(pool []Record) {
		for i := range pool {
			loadUnion = append(loadUnion, &pool[i])
		}
	}
	collectAdvancedAccounts(filterPool)
	for _, pool := range groupPools {
		collectAdvancedAccounts(pool)
	}
	loadMap := h.fetchAdvancedSchedulerLoadMap(ctx, loadUnion)

	baseScores := make(map[int64]*AccountSchedulerScore)
	for accountID, score := range h.scoreAdvancedSchedulerPool(ctx, nil, filterPool, loadMap) {
		copiedScore := score
		baseScores[accountID] = &copiedScore
	}

	groupScoresByAccount := make(map[int64][]AccountSchedulerGroupScore)
	scoreGroupPool := func(groupID *int64, group *accessview.GroupConfig, groupNameByID map[int64]string, pool []Record) {
		if len(pool) == 0 {
			return
		}
		scores := h.scoreAdvancedSchedulerPool(ctx, group, pool, loadMap)
		for accountID, schedulerScore := range scores {
			if _, ok := pageAccountIDs[accountID]; !ok {
				continue
			}
			groupScore := AccountSchedulerGroupScore{
				GroupID:               groupID,
				AccountSchedulerScore: schedulerScore,
			}
			if groupID != nil {
				groupScore.GroupName = groupNameByID[*groupID]
			}
			groupScoresByAccount[accountID] = append(groupScoresByAccount[accountID], groupScore)
		}
	}

	for _, groupID := range groupIDList {
		gid := groupID
		pool, ok := groupPools[gid]
		if !ok {
			continue
		}
		groupNameByID := make(map[int64]string)
		for i := range pool {
			account := &pool[i]
			for _, accountGroup := range account.AccountGroups {
				if accountGroup.GroupID != gid {
					continue
				}
				if accountGroup.Group != nil {
					groupNameByID[gid] = accountGroup.Group.Name
				}
			}
		}
		scoreGroupPool(&gid, advancedGroups[gid], groupNameByID, pool)
	}
	// 只返回至少属于一个高级调度分组的评分；基础分组和未分组账号不显示该管理配置。
	for accountID := range baseScores {
		if _, ok := groupScoresByAccount[accountID]; !ok {
			delete(baseScores, accountID)
		}
	}

	for accountID := range groupScoresByAccount {
		sort.SliceStable(groupScoresByAccount[accountID], func(i, j int) bool {
			left := groupScoresByAccount[accountID][i]
			right := groupScoresByAccount[accountID][j]
			return *left.GroupID < *right.GroupID
		})
	}
	return baseScores, groupScoresByAccount
}

func (h *SchedulerScoreView) listAccountSchedulerScoreFilterPool(
	ctx context.Context,
	platform, accountType, status, search string,
	groupID int64,
	privacyMode string,
) []Record {
	if h.adminService == nil {
		return nil
	}
	accounts, err := h.adminService.ListAccountsForSchedulerScoreFilter(ctx, platform, accountType, status, search, groupID, privacyMode)
	if err != nil {
		h.options.Warn("advanced_scheduler_filter_score_pool_failed", "error", err)
		return nil
	}
	return accounts
}

// Build 保留过滤池与分组池的并集负载批查，以及分组稳定排序。
func (h *SchedulerScoreView) Build(ctx context.Context, values []Record, platform, kind, status, search string, groupID int64, privacy string) (map[int64]*AccountSchedulerScore, map[int64][]AccountSchedulerGroupScore) {
	pool := h.listAccountSchedulerScoreFilterPool(ctx, platform, kind, status, search, groupID, privacy)
	return h.buildAdvancedAccountSchedulerScores(ctx, values, pool)
}
