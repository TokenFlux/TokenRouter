// 本文件维护 account 的所属能力；兼容入口复用唯一实现。
package account

import (
	context "context"
	sync "sync"
	time "time"

	errgroup "golang.org/x/sync/errgroup"
)

// ManagementListReader 保留原分页与服务端排序入口。
type ManagementListReader interface {
	ListAccounts(context.Context, int, int, string, string, string, string, int64, string, string, string) ([]Record, int64, error)
}
type ManagementListInput struct {
	Page, PageSize                        int
	Platform, AccountType, Status, Search string
	GroupID                               int64
	PrivacyMode, SortBy, SortOrder        string
	IncludeSchedulerScore                 bool
}
type ManagementListResult struct {
	Items []RuntimeStatus
	Total int64
}

// ManagementList 拥有账号列表的查询与运行展示编排，不生成 HTTP 响应。
type ManagementList struct {
	admin   ManagementListReader
	runtime *RuntimeStatusReader
	scores  *SchedulerScoreView
	ollama  *OllamaCloudUsageService
}

func NewManagementList(admin ManagementListReader, runtime *RuntimeStatusReader, scores *SchedulerScoreView, ollama *OllamaCloudUsageService) *ManagementList {
	return &ManagementList{admin, runtime, scores, ollama}
}
func runtimeConfig(v *Record) *RuntimeConfig {
	return &RuntimeConfig{Extra: v.Extra, Concurrency: v.Concurrency, SessionWindowStart: v.SessionWindowStart, SessionWindowEnd: v.SessionWindowEnd}
}
func (s *ManagementList) List(ctx context.Context, input ManagementListInput) (*ManagementListResult, error) {
	page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder, includeSchedulerScore := input.Page, input.PageSize, input.Platform, input.AccountType, input.Status, input.Search, input.GroupID, input.PrivacyMode, input.SortBy, input.SortOrder, input.IncludeSchedulerScore
	accounts, total, err := s.admin.ListAccounts(ctx, page, pageSize, platform, accountType, status, search, groupID, privacyMode, sortBy, sortOrder)
	if err != nil {
		return nil, err
	}
	if s.ollama != nil && len(accounts) > 0 {
		accountPointers := make([]*Record, len(accounts))
		for index := range accounts {
			accountPointers[index] = &accounts[index]
		}
		if err := s.ollama.ResolveAccounts(ctx, accountPointers); err != nil {
			return nil, err
		}
	}

	// Get current concurrency counts for all accounts
	accountIDs := make([]int64, len(accounts))
	for i, acc := range accounts {
		accountIDs[i] = acc.ID
	}

	concurrencyCounts := make(map[int64]int)
	var windowCosts map[int64]float64
	var activeSessions map[int64]int
	var rpmCounts map[int64]int
	// 用户显式请求该列时才进入昂贵的高级调度候选池打分路径。
	var schedulerScores map[int64]*AccountSchedulerScore
	var schedulerGroupScores map[int64][]AccountSchedulerGroupScore
	if includeSchedulerScore && len(accounts) > 0 {
		schedulerScores, schedulerGroupScores = s.scores.Build(ctx, accounts, platform, accountType, status, search, groupID, privacyMode)
	}

	// 始终获取并发数（Redis ZCARD，极低开销）
	if s.runtime.options.Concurrency != nil {
		if cc, ccErr := s.runtime.options.Concurrency(ctx, accountIDs); ccErr == nil && cc != nil {
			concurrencyCounts = cc
		}
	}

	// 识别需要查询窗口费用、会话数和 RPM 的账号（Anthropic OAuth/SetupToken 且启用了相应功能）
	windowCostAccountIDs := make([]int64, 0)
	sessionLimitAccountIDs := make([]int64, 0)
	rpmAccountIDs := make([]int64, 0)
	sessionIdleTimeouts := make(map[int64]time.Duration) // 各账号的会话空闲超时配置
	for i := range accounts {
		acc := &accounts[i]
		if acc.IsAnthropicOAuthOrSetupToken() {
			if runtimeConfig(acc).GetWindowCostLimit() > 0 {
				windowCostAccountIDs = append(windowCostAccountIDs, acc.ID)
			}
			if runtimeConfig(acc).GetMaxSessions() > 0 {
				sessionLimitAccountIDs = append(sessionLimitAccountIDs, acc.ID)
				sessionIdleTimeouts[acc.ID] = time.Duration(runtimeConfig(acc).GetSessionIdleTimeoutMinutes()) * time.Minute
			}
			if runtimeConfig(acc).GetBaseRPM() > 0 {
				rpmAccountIDs = append(rpmAccountIDs, acc.ID)
			}
		}
	}

	// 始终获取 RPM 计数（Redis GET，极低开销）
	if len(rpmAccountIDs) > 0 && s.runtime.options.RPMBatch != nil {
		rpmCounts, _ = s.runtime.options.RPMBatch(ctx, rpmAccountIDs)
		if rpmCounts == nil {
			rpmCounts = make(map[int64]int)
		}
	}

	// 始终获取活跃会话数（Redis ZCARD，低开销）
	if len(sessionLimitAccountIDs) > 0 && s.runtime.options.Sessions != nil {
		activeSessions, _ = s.runtime.options.Sessions(ctx, sessionLimitAccountIDs, sessionIdleTimeouts)
		if activeSessions == nil {
			activeSessions = make(map[int64]int)
		}
	}

	// 始终获取窗口费用（PostgreSQL 聚合查询）
	if len(windowCostAccountIDs) > 0 {
		windowCosts = make(map[int64]float64)
		var mu sync.Mutex
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(10) // 限制并发数

		for i := range accounts {
			acc := &accounts[i]
			if !acc.IsAnthropicOAuthOrSetupToken() || runtimeConfig(acc).GetWindowCostLimit() <= 0 {
				continue
			}
			accCopy := acc // 闭包捕获
			g.Go(func() error {
				// 使用统一的窗口开始时间计算逻辑（考虑窗口过期情况）
				startTime := runtimeConfig(accCopy).GetCurrentWindowStartTime(s.runtime.options.Now())
				stats, err := s.runtime.options.WindowCost(gctx, accCopy.ID, startTime)
				if err == nil && stats != nil {
					mu.Lock()
					windowCosts[accCopy.ID] = *stats // 使用标准费用
					mu.Unlock()
				}
				return nil // 不返回错误，允许部分失败
			})
		}
		_ = g.Wait()
	}

	// Build response with concurrency info
	result := make([]RuntimeStatus, len(accounts))
	for i := range accounts {
		acc := &accounts[i]
		s.runtime.applyQuotaAutoPauseState(ctx, acc)
		item := RuntimeStatus{
			Record:             acc,
			CurrentConcurrency: concurrencyCounts[acc.ID],
			SchedulerScore:     schedulerScores[acc.ID],
			SchedulerScores:    schedulerGroupScores[acc.ID],
		}

		// 添加窗口费用（仅当启用时）
		if windowCosts != nil {
			if cost, ok := windowCosts[acc.ID]; ok {
				item.CurrentWindowCost = &cost
			}
		}

		// 添加活跃会话数（仅当启用时）
		if activeSessions != nil {
			if count, ok := activeSessions[acc.ID]; ok {
				item.ActiveSessions = &count
			}
		}

		// 添加 RPM 计数（仅当启用时）
		if rpmCounts != nil {
			if rpm, ok := rpmCounts[acc.ID]; ok {
				item.CurrentRPM = &rpm
			}
		}

		result[i] = item
	}

	return &ManagementListResult{Items: result, Total: total}, nil
}
