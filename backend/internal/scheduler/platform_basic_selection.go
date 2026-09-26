package scheduler

import (
	"context"
	"fmt"
	"sort"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 基础平台选择保留自己的 LRU、粘性溢出和复核顺序，不合并高级调度策略。
func (s *PlatformSelector) selectBasicOnlyRoutes(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability account.OpenAIEndpointCapability) (*FlowAccount, error) {
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	if s.ports.CheckPricing(ctx, groupID, requestedModel) {
		s.diagnostics.event("warn", "group model restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, fmt.Errorf("%w supporting model: %s (group model restriction)", ErrNoAvailableAccounts, requestedModel)
	}

	if account := s.tryBasicSticky(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, stickyAccountID, requiredCapability); account != nil {
		return account, nil
	}

	accounts, err := s.ports.ListCandidates(ctx, groupID, platform)
	if err != nil {
		return nil, fmt.Errorf("query accounts failed: %w", err)
	}

	selected, compactBlocked := s.SelectBestBasic(ctx, groupID, platform, accounts, requestedModel, routingModel, excludedIDs, requireCompact, requiredCapability)

	if selected == nil {
		return nil, s.ports.Unavailable(ctx, requestedModel, routingModel, compactBlocked, "", accounts)
	}

	hydrated, err := s.ports.Hydrate(ctx, selected)
	if err != nil {
		return nil, err
	}

	if sessionHash != "" {
		_ = s.ports.SetSticky(ctx, groupID, sessionHash, selected.ID, s.ports.BasicStickyTTL)
	}

	return hydrated, nil
}

func (s *PlatformSelector) tryBasicSticky(ctx context.Context, groupID *int64, platform string, sessionHash, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyAccountID int64, requiredCapability account.OpenAIEndpointCapability) *FlowAccount {
	if sessionHash == "" {
		return nil
	}
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)

	accountID := stickyAccountID
	if accountID <= 0 {
		var err error
		accountID, err = s.ports.GetSticky(ctx, groupID, sessionHash)
		if err != nil || accountID <= 0 {
			return nil
		}
	}

	if _, excluded := excludedIDs[accountID]; excluded {
		return nil
	}

	account, err := s.ports.GetSchedulable(ctx, accountID)
	if err != nil {
		return nil
	}

	// 检查账号是否需要清理粘性会话
	// Check if sticky session should be cleared
	if s.ports.ClearSticky(account, routingModel) {
		_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
		return nil
	}

	// 验证账号是否可用于当前请求
	// Verify account is usable for current request
	if !s.ports.BasicEligible(ctx, account, platform, routingModel, false, requiredCapability) {
		return nil
	}
	if !s.ports.PrivacyAllowed(ctx, groupID, account) {
		return nil
	}
	if !s.ports.ShadowAllowed(ctx, account) || !s.ports.ParentHealthy(account, s.ports.ParentLookup(ctx)) {
		_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
		return nil
	}
	if s.ports.RuntimeBlocked(account, routingModel) {
		_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
		return nil
	}
	account = s.ports.Recheck(ctx, account, groupID, platform, routingModel, requireCompact, requiredCapability)
	if account == nil || !s.ports.MatchesGroup(account, groupID) {
		_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
		return nil
	}
	if groupID != nil && s.ports.NeedsGroupCheck(ctx, groupID) &&
		s.ports.GroupModelRestricted(ctx, *groupID, account, routingModel, requireCompact) {
		_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
		return nil
	}

	// 刷新会话 TTL 并返回账号
	// Refresh session TTL and return account
	_ = s.ports.RefreshSticky(ctx, groupID, sessionHash, s.ports.BasicStickyTTL)
	return account
}

func (s *PlatformSelector) SelectBestBasic(ctx context.Context, groupID *int64, platform string, accounts []FlowAccount, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability account.OpenAIEndpointCapability) (*FlowAccount, bool) {
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	compactBlocked := false
	needsUpstreamCheck := s.ports.NeedsGroupCheck(ctx, groupID)
	eligible := make([]*FlowAccount, 0, len(accounts))

	for i := range accounts {
		acc := &accounts[i]

		// 跳过被排除的账号
		// Skip excluded accounts
		if _, excluded := excludedIDs[acc.ID]; excluded {
			continue
		}

		fresh := s.ports.Fresh(ctx, acc, platform, routingModel, false, requiredCapability)
		if fresh == nil {
			continue
		}
		fresh = s.ports.Recheck(ctx, fresh, groupID, platform, routingModel, false, requiredCapability)
		if fresh == nil {
			continue
		}
		if !s.ports.PrivacyAllowed(ctx, groupID, fresh) {
			continue
		}
		if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, fresh, routingModel, requireCompact) {
			continue
		}
		if requireCompact && !s.ports.CompactAllowed(fresh) {
			compactBlocked = true
			continue
		}

		eligible = append(eligible, fresh)
	}

	if len(eligible) == 0 {
		return nil, compactBlocked
	}
	sort.SliceStable(eligible, func(i, j int) bool {
		a, b := eligible[i], eligible[j]
		return s.isBetterBasic(a, b)
	})
	return eligible[0], compactBlocked
}

func (s *PlatformSelector) isBetterBasic(candidate, current *FlowAccount) bool {
	// 优先级更高（数值更小）
	// Higher priority (lower value)
	if candidate.Priority < current.Priority {
		return true
	}
	if candidate.Priority > current.Priority {
		return false
	}

	// 同优先级，比较最后使用时间
	// Same priority, compare last used time
	switch {
	case candidate.LastUsedAt == nil && current.LastUsedAt != nil:
		// candidate 从未使用，优先
		return true
	case candidate.LastUsedAt != nil && current.LastUsedAt == nil:
		// current 从未使用，保持
		return false
	case candidate.LastUsedAt == nil && current.LastUsedAt == nil:
		// 都未使用，保持
		return false
	default:
		// 都使用过，选择最久未使用的
		return candidate.LastUsedAt.Before(*current.LastUsedAt)
	}
}

func (s *PlatformSelector) selectBasicRoutes(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability account.OpenAIEndpointCapability) (*FlowSelection, error) {
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	if s.ports.CheckPricing(ctx, groupID, requestedModel) {
		s.diagnostics.event("warn", "group model restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, fmt.Errorf("%w supporting model: %s (group model restriction)", ErrNoAvailableAccounts, requestedModel)
	}

	cfg := s.ports.Options()
	needsUpstreamCheck := s.ports.NeedsGroupCheck(ctx, groupID)
	var stickyAccountID int64
	if sessionHash != "" && s.ports.CacheAvailable {
		if accountID, err := s.ports.GetSticky(ctx, groupID, sessionHash); err == nil {
			stickyAccountID = accountID
		}
	}
	if s.concurrency == nil || !cfg.LoadBatchEnabled {
		account, err := s.selectBasicOnlyRoutes(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, stickyAccountID, requiredCapability)
		if err != nil {
			return nil, err
		}
		if !s.ports.PrivacyAllowed(ctx, groupID, account) {
			return nil, s.ports.Unavailable(ctx, requestedModel, routingModel, false, "", nil)
		}
		result, err := s.ports.Acquire(ctx, account.ID, account.Concurrency)
		if err == nil && result != nil && result.Acquired {
			return s.ports.CompleteAcquired(ctx, account, result.ReleaseFunc)
		}
		if stickyAccountID > 0 && stickyAccountID == account.ID && s.concurrency != nil {
			waitingCount, _ := s.concurrency.GetAccountWaitingCount(ctx, account.ID)
			if waitingCount < cfg.StickySessionMaxWaiting {
				return s.ports.Complete(ctx, account, false, nil, &AccountWaitPlan{
					AccountID:      account.ID,
					MaxConcurrency: account.Concurrency,
					Timeout:        cfg.StickySessionWaitTimeout,
					MaxWaiting:     cfg.StickySessionMaxWaiting,
				})
			}
		}
		return s.ports.Complete(ctx, account, false, nil, &AccountWaitPlan{
			AccountID:      account.ID,
			MaxConcurrency: account.Concurrency,
			Timeout:        cfg.FallbackWaitTimeout,
			MaxWaiting:     cfg.FallbackMaxWaiting,
		})
	}

	accounts, err := s.ports.ListCandidates(ctx, groupID, platform)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, s.ports.Unavailable(
			ctx,
			requestedModel,
			routingModel,
			false,
			PlatformFilterStats{}.Summary(""),
			accounts,
		)
	}

	isExcluded := func(accountID int64) bool {
		if excludedIDs == nil {
			return false
		}
		_, excluded := excludedIDs[accountID]
		return excluded
	}

	// 粘性账号的有界等待队列已满时，第二层可以为当前请求临时借用其它账号；
	// 该容量溢出只对单次请求有效，不能把整段会话的持久绑定迁移到冷缓存账号。
	stickySpillover := false
	if sessionHash != "" {
		accountID := stickyAccountID
		if accountID > 0 && !isExcluded(accountID) {
			account, err := s.ports.GetSchedulable(ctx, accountID)
			if err == nil {
				clearSticky := s.ports.ClearSticky(account, routingModel)
				if clearSticky {
					_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
				}
				if !clearSticky && s.ports.BasicEligible(ctx, account, platform, routingModel, false, requiredCapability) && s.ports.PrivacyAllowed(ctx, groupID, account) {
					account = s.ports.Recheck(ctx, account, groupID, platform, routingModel, requireCompact, requiredCapability)
					if account == nil {
						_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
					} else if !s.ports.MatchesGroup(account, groupID) {
						_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
					} else if s.ports.RuntimeBlocked(account, routingModel) {
						_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
					} else if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, account, routingModel, requireCompact) {
						_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
					} else if !s.ports.ShadowAllowed(ctx, account) || !s.ports.ParentHealthy(account, s.ports.ParentLookup(ctx)) {
						_ = s.ports.DeleteSticky(ctx, groupID, sessionHash)
					} else {
						result, err := s.ports.Acquire(ctx, accountID, account.Concurrency)
						if err == nil && result != nil && result.Acquired {
							selection, selectErr := s.ports.CompleteAcquired(ctx, account, result.ReleaseFunc)
							if selectErr != nil {
								return nil, selectErr
							}
							_ = s.ports.RefreshSticky(ctx, groupID, sessionHash, s.ports.BasicStickyTTL)
							return selection, nil
						}

						waitingCount, _ := s.concurrency.GetAccountWaitingCount(ctx, accountID)
						if waitingCount < cfg.StickySessionMaxWaiting {
							return s.ports.Complete(ctx, account, false, nil, &AccountWaitPlan{
								AccountID:      accountID,
								MaxConcurrency: account.Concurrency,
								Timeout:        cfg.StickySessionWaitTimeout,
								MaxWaiting:     cfg.StickySessionMaxWaiting,
							})
						}
						stickySpillover = true
					}
				}
			}
		}
	}

	parentCacheL2 := make(map[int64]*FlowAccount)
	parentLookupL2 := func(id int64) *FlowAccount {
		if a, ok := parentCacheL2[id]; ok {
			return a
		}
		if s.ports.ReadAccountDB == nil {
			return nil
		}
		a, _ := s.ports.ReadAccountDB(ctx, id)
		parentCacheL2[id] = a
		return a
	}
	baseCandidateCount := 0
	filterStats := PlatformFilterStats{Pool: len(accounts)}
	candidates := make([]*FlowAccount, 0, len(accounts))
	for i := range accounts {
		acc := &accounts[i]
		if isExcluded(acc.ID) {
			filterStats.Exclude("excluded")
			continue
		}
		// 调度快照可能暂时过期；在批处理选择前重新检查模型、配额和可调度状态。
		if reason := s.ports.BasicFailureReason(ctx, acc, platform, routingModel, false, requiredCapability); reason != "" {
			filterStats.Exclude(reason)
			continue
		}
		if !s.ports.PrivacyAllowed(ctx, groupID, acc) {
			filterStats.Exclude("privacy_not_set")
			continue
		}
		if !s.ports.ShadowAllowed(ctx, acc) || !s.ports.ParentHealthy(acc, parentLookupL2) {
			filterStats.Exclude("shadow_parent_unhealthy")
			continue
		}
		if s.ports.RuntimeBlocked(acc, routingModel) {
			filterStats.Exclude("runtime_blocked")
			continue
		}
		if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, acc, routingModel, requireCompact) {
			filterStats.Exclude("group_upstream_restricted")
			continue
		}
		baseCandidateCount++
		candidates = append(candidates, acc)
	}

	if len(candidates) == 0 {
		return nil, s.ports.Unavailable(
			ctx,
			requestedModel,
			routingModel,
			false,
			filterStats.Summary(""),
			accounts,
		)
	}
	accountLoads := make([]AccountWithConcurrency, 0, len(candidates))
	for _, acc := range candidates {
		accountLoads = append(accountLoads, AccountWithConcurrency{
			ID:             acc.ID,
			MaxConcurrency: acc.EffectiveLoadFactor(),
		})
	}

	tryAcquireFromLoadMap := func(loadMap map[int64]*AccountLoadInfo) (*FlowSelection, bool, error) {
		var available []FlowLoad
		for _, acc := range candidates {
			loadInfo := loadMap[acc.ID]
			if loadInfo == nil {
				loadInfo = &AccountLoadInfo{AccountID: acc.ID}
			}
			if loadInfo.LoadRate < 100 {
				available = append(available, FlowLoad{
					Account:  acc,
					LoadInfo: loadInfo,
				})
			}
		}

		if len(available) == 0 {
			return nil, false, nil
		}

		sort.SliceStable(available, func(i, j int) bool {
			a, b := available[i], available[j]
			if a.Account.Priority != b.Account.Priority {
				return a.Account.Priority < b.Account.Priority
			}
			if a.LoadInfo.LoadRate != b.LoadInfo.LoadRate {
				return a.LoadInfo.LoadRate < b.LoadInfo.LoadRate
			}
			switch {
			case a.Account.LastUsedAt == nil && b.Account.LastUsedAt != nil:
				return true
			case a.Account.LastUsedAt != nil && b.Account.LastUsedAt == nil:
				return false
			case a.Account.LastUsedAt == nil && b.Account.LastUsedAt == nil:
				return false
			default:
				return a.Account.LastUsedAt.Before(*b.Account.LastUsedAt)
			}
		})
		flowShuffleWithinSortGroups(available)
		selectionOrder := make([]FlowLoad, 0, len(available))
		if requireCompact {
			// 先尝试快照已启用的账号，再复核可能已由管理员重新启用的旧快照。
			appendEnabled := func(out []FlowLoad, enabled bool) []FlowLoad {
				for _, item := range available {
					if s.ports.CompactAllowed(item.Account) == enabled {
						out = append(out, item)
					}
				}
				return out
			}
			selectionOrder = appendEnabled(selectionOrder, true)
			selectionOrder = appendEnabled(selectionOrder, false)
		} else {
			selectionOrder = append(selectionOrder, available...)
		}

		for _, item := range selectionOrder {
			fresh := s.ports.Fresh(ctx, item.Account, platform, routingModel, false, requiredCapability)
			if fresh == nil {
				continue
			}
			fresh = s.ports.Recheck(ctx, fresh, groupID, platform, routingModel, requireCompact, requiredCapability)
			if fresh == nil {
				continue
			}
			if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, fresh, routingModel, requireCompact) {
				continue
			}
			result, err := s.ports.Acquire(ctx, fresh.ID, fresh.Concurrency)
			if err == nil && result != nil && result.Acquired {
				selection, selectErr := s.ports.CompleteAcquired(ctx, fresh, result.ReleaseFunc)
				if selectErr != nil {
					return nil, true, selectErr
				}
				if sessionHash != "" && !stickySpillover {
					_ = s.ports.SetSticky(ctx, groupID, sessionHash, fresh.ID, s.ports.BasicStickyTTL)
				}
				return selection, true, nil
			}
		}
		return nil, true, nil
	}

	loadMap, err := s.concurrency.GetAccountsLoadBatch(ctx, accountLoads)
	if err != nil {
		ordered := append([]*FlowAccount(nil), candidates...)
		flowSortAccountsByPriorityAndLastUsed(ordered, false)
		if requireCompact {
			ordered = s.prioritizeBasicCompact(ordered)
		}
		for _, acc := range ordered {
			fresh := s.ports.Fresh(ctx, acc, platform, routingModel, false, requiredCapability)
			if fresh == nil {
				continue
			}
			fresh = s.ports.Recheck(ctx, fresh, groupID, platform, routingModel, requireCompact, requiredCapability)
			if fresh == nil {
				continue
			}
			if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, fresh, routingModel, requireCompact) {
				continue
			}
			result, err := s.ports.Acquire(ctx, fresh.ID, fresh.Concurrency)
			if err == nil && result != nil && result.Acquired {
				selection, selectErr := s.ports.CompleteAcquired(ctx, fresh, result.ReleaseFunc)
				if selectErr != nil {
					return nil, selectErr
				}
				if sessionHash != "" && !stickySpillover {
					_ = s.ports.SetSticky(ctx, groupID, sessionHash, fresh.ID, s.ports.BasicStickyTTL)
				}
				return selection, nil
			}
		}
	} else {
		if selection, attempted, selectErr := tryAcquireFromLoadMap(loadMap); selectErr != nil {
			return nil, selectErr
		} else if selection != nil {
			return selection, nil
		} else if attempted {
			if freshLoadMap, loadErr := s.concurrency.GetAccountsLoadBatchFresh(ctx, accountLoads); loadErr == nil {
				if selection, _, selectErr := tryAcquireFromLoadMap(freshLoadMap); selectErr != nil {
					return nil, selectErr
				} else if selection != nil {
					return selection, nil
				}
			}
		}
	}

	flowSortAccountsByPriorityAndLastUsed(candidates, false)
	if requireCompact {
		candidates = s.prioritizeBasicCompact(candidates)
	}
	for _, acc := range candidates {
		fresh := s.ports.Fresh(ctx, acc, platform, routingModel, false, requiredCapability)
		if fresh == nil {
			continue
		}
		fresh = s.ports.Recheck(ctx, fresh, groupID, platform, routingModel, requireCompact, requiredCapability)
		if fresh == nil {
			continue
		}
		if needsUpstreamCheck && s.ports.GroupModelRestricted(ctx, *groupID, fresh, routingModel, requireCompact) {
			continue
		}
		return s.ports.Complete(ctx, fresh, false, nil, &AccountWaitPlan{
			AccountID:      fresh.ID,
			MaxConcurrency: fresh.Concurrency,
			Timeout:        cfg.FallbackWaitTimeout,
			MaxWaiting:     cfg.FallbackMaxWaiting,
		})
	}

	if requireCompact && baseCandidateCount > 0 {
		return nil, ErrNoAvailableCompactAccounts
	}
	return nil, s.ports.Unavailable(ctx, requestedModel, routingModel, false, "", accounts)
}

func (s *PlatformSelector) prioritizeBasicCompact(accounts []*FlowAccount) []*FlowAccount {
	if len(accounts) == 0 {
		return nil
	}
	enabled := make([]*FlowAccount, 0, len(accounts))
	disabled := make([]*FlowAccount, 0, len(accounts))
	for _, account := range accounts {
		if s.ports.CompactAllowed(account) {
			enabled = append(enabled, account)
		} else {
			disabled = append(disabled, account)
		}
	}
	return append(enabled, disabled...)
}

// SelectBasicOnly 只选择并补全账号，不取得请求槽或注册会话。
func (s *PlatformSelector) SelectBasicOnly(ctx context.Context, input PlatformSelectionInput) (*FlowAccount, error) {
	return s.selectBasicOnlyRoutes(ctx, input.GroupID, input.Platform, input.SessionHash, input.RequestedModel, input.RoutingModel, input.ExcludedIDs, input.RequireCompact, input.StickyAccountID, input.RequiredCapability)
}

func (s *PlatformSelector) SelectBasic(ctx context.Context, input PlatformSelectionInput) (*FlowSelection, error) {
	return s.selectBasicRoutes(ctx, input.GroupID, input.Platform, input.SessionHash, input.RequestedModel, input.RoutingModel, input.ExcludedIDs, input.RequireCompact, input.RequiredCapability)
}
