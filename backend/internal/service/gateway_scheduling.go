package service

// 本文件由 gateway_service.go 纯移动拆分而来：账号选择与负载感知调度、窗口费用
// 与 RPM 预取、候选排序/过滤、混合平台调度与选择失败诊断。仅做代码搬迁，
// 无任何行为变更。

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// SelectAccount 选择账号（粘性会话+优先级）
func (s *GatewayService) SelectAccount(ctx context.Context, groupID *int64, sessionHash string) (*Account, error) {
	return s.SelectAccountForModel(ctx, groupID, sessionHash, "")
}

// SelectAccountForModel 选择支持指定模型的账号（粘性会话+优先级+模型映射）
func (s *GatewayService) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*Account, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectAccountForModelWithExclusions selects an account supporting the requested model while excluding specified accounts.
func (s *GatewayService) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*Account, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectOnly(ctx, scheduler.SelectionInput{GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	return scope.oldAccount(selected), err
}

// SelectAccountWithLoadAwareness selects account with load-awareness and wait plan.
// metadataUserID: 用于客户端亲和调度，从中提取客户端 ID
// sub2apiUserID: 系统用户 ID，用于二维亲和调度
// @project-doc docs/architecture/gateway_request_lifecycle.md#account_selection_and_failover
func (s *GatewayService) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, metadataUserID string, sub2apiUserID int64) (*AccountSelectionResult, error) {
	core, scope := s.genericSelector()
	plan, _ := routePlanFromContext(ctx)
	result, err := core.Select(ctx, scheduler.SelectionInput{RoutePlan: plan, GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	return scope.restore(result), err
}

// ReportAdvancedAccountScheduleResult 将通用网关转发结果写入高级调度运行时反馈。
// 只有实际由高级调度器选出的请求才会更新统计，基础调度器保持原有行为。
func (s *GatewayService) ReportAdvancedAccountScheduleResult(selection *AccountSelectionResult, accountID int64, success bool, result *ForwardResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler || accountID <= 0 {
		return
	}
	var firstTokenMs *int
	if result != nil {
		firstTokenMs = result.FirstTokenMs
	}
	if selection.AdvancedSchedulerFeedback != nil {
		s.advancedSchedulerStats().report(accountID, success, firstTokenMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.advancedSchedulerStats().report(accountID, success, firstTokenMs)
}

// RecordAdvancedAccountSwitch 记录高级调度请求的一次账号切换事件。
func (s *GatewayService) RecordAdvancedAccountSwitch(selection *AccountSelectionResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler {
		return
	}
	s.advancedSchedulerStats().reportSwitch()
}

// tryAcquireByAdvancedScheduler 在既有硬过滤完成后按通用评分和 Top-K 加权顺序复核并发槽位。
// 所有账号仍逐个调用 tryAcquireAccountSlot，因此负载快照过期时不会越过真实并发上限。
func (s *GatewayService) tryAcquireByAdvancedScheduler(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	available []accountWithLoad,
) (*AccountSelectionResult, bool, error) {
	core, scope := s.genericSelector()
	result, found, err := core.TryAdvanced(ctx, groupID, sessionHash, scope.loads(available))
	return scope.restore(result), found, err
}

// advancedSchedulerStats 返回网关级运行时反馈；测试或旧构造路径未初始化时惰性补齐。
func (s *GatewayService) advancedSchedulerStats() *advancedAccountRuntimeStats {
	if s == nil {
		return nil
	}
	if s.rateLimitService != nil {
		if stats := s.rateLimitService.AdvancedSchedulerRuntimeStats(); stats != nil {
			return stats
		}
	}
	if s.advancedAccountStats == nil {
		s.advancedAccountStats = newAdvancedAccountRuntimeStats()
	}
	return s.advancedAccountStats
}

// advancedSchedulerEffectiveSettingsForRequest 将最终分组覆盖应用到网关通用设置之上。
func (s *GatewayService) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, groupID *int64) advancedSchedulerEffectiveSettings {
	gateway := &OpenAIGatewayService{cfg: s.cfg, rateLimitService: s.rateLimitService, schedulerSnapshot: s.schedulerSnapshot}
	return gateway.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
}

func (s *GatewayService) schedulingConfig() config.GatewaySchedulingConfig {
	if s.cfg != nil {
		return s.cfg.Gateway.Scheduling
	}
	return config.GatewaySchedulingConfig{
		StickySessionMaxWaiting:  3,
		StickySessionWaitTimeout: 45 * time.Second,
		FallbackWaitTimeout:      30 * time.Second,
		FallbackMaxWaiting:       100,
		LoadBatchEnabled:         true,
		SlotCleanupInterval:      30 * time.Second,
	}
}

func (s *GatewayService) withGroupContext(ctx context.Context, group *Group) context.Context {
	if !IsGroupContextValid(group) {
		return ctx
	}
	if existing, ok := ctx.Value(ctxkey.Group).(*Group); ok && existing != nil && existing.ID == group.ID && IsGroupContextValid(existing) {
		return ctx
	}
	return context.WithValue(ctx, ctxkey.Group, group)
}

func (s *GatewayService) groupFromContext(ctx context.Context, groupID int64) *Group {
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.ID == groupID {
		return group
	}
	return nil
}

func (s *GatewayService) resolveGroupByID(ctx context.Context, groupID int64) (*Group, error) {
	if group := s.groupFromContext(ctx, groupID); group != nil {
		return group, nil
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group failed: %w", err)
	}
	return group, nil
}

func (s *GatewayService) ResolveGroupByID(ctx context.Context, groupID int64) (*Group, error) {
	return s.resolveGroupByID(ctx, groupID)
}

func (s *GatewayService) routingAccountIDsForRequest(ctx context.Context, groupID *int64, requestedModel string, platform string) []int64 {
	if groupID == nil || requestedModel == "" || platform != PlatformAnthropic {
		return nil
	}
	group, err := s.resolveGroupByID(ctx, *groupID)
	if err != nil || group == nil {
		if s.debugModelRoutingEnabled() {
			logger.LegacyPrintf("service.gateway", "[ModelRoutingDebug] resolve group failed: group_id=%v model=%s platform=%s err=%v", derefGroupID(groupID), requestedModel, platform, err)
		}
		return nil
	}
	// Preserve existing behavior: model routing only applies to anthropic groups.
	if group.Platform != PlatformAnthropic {
		if s.debugModelRoutingEnabled() {
			logger.LegacyPrintf("service.gateway", "[ModelRoutingDebug] skip: non-anthropic group platform: group_id=%d group_platform=%s model=%s", group.ID, group.Platform, requestedModel)
		}
		return nil
	}
	routingModel := s.channelMappedModelForGroup(ctx, groupID, requestedModel)
	ids := group.GetRoutingAccountIDs(routingModel)
	if s.debugModelRoutingEnabled() {
		logger.LegacyPrintf("service.gateway", "[ModelRoutingDebug] routing lookup: group_id=%d model=%s enabled=%v rules=%d matched_ids=%v",
			group.ID, requestedModel, group.ModelRoutingEnabled, len(group.ModelRouting), ids)
	}
	return ids
}

// @project-doc docs/domains/gateway_policy_controls.md#gateway_policy_layers
func (s *GatewayService) resolveGatewayGroup(ctx context.Context, groupID *int64) (*Group, *int64, error) {
	if groupID == nil {
		return nil, nil, nil
	}

	currentID := *groupID
	visited := map[int64]struct{}{}
	for {
		if _, seen := visited[currentID]; seen {
			return nil, nil, fmt.Errorf("fallback group cycle detected")
		}
		visited[currentID] = struct{}{}

		group, err := s.resolveGroupByID(ctx, currentID)
		if err != nil {
			return nil, nil, err
		}

		if !group.ClaudeCodeOnly || IsClaudeCodeClient(ctx) {
			return group, &currentID, nil
		}

		if group.FallbackGroupID == nil {
			return nil, nil, ErrClaudeCodeOnly
		}
		currentID = *group.FallbackGroupID
	}
}

// checkClaudeCodeRestriction 检查分组的 Claude Code 客户端限制
// 如果分组启用了 claude_code_only 且请求不是来自 Claude Code 客户端：
//   - 有降级分组：返回降级分组的 ID
//   - 无降级分组：返回 ErrClaudeCodeOnly 错误
func (s *GatewayService) checkClaudeCodeRestriction(ctx context.Context, groupID *int64) (*Group, *int64, error) {
	if groupID == nil {
		return nil, groupID, nil
	}

	// 强制平台模式不检查 Claude Code 限制
	if forcePlatform, hasForcePlatform := ctx.Value(ctxkey.ForcePlatform).(string); hasForcePlatform && forcePlatform != "" {
		group, err := s.resolveGroupByID(ctx, *groupID)
		if err != nil {
			return nil, nil, err
		}
		return group, groupID, nil
	}

	group, resolvedID, err := s.resolveGatewayGroup(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}

	return group, resolvedID, nil
}

func (s *GatewayService) resolvePlatform(ctx context.Context, groupID *int64, group *Group) (string, bool, error) {
	forcePlatform, hasForcePlatform := ctx.Value(ctxkey.ForcePlatform).(string)
	if hasForcePlatform && forcePlatform != "" {
		return forcePlatform, true, nil
	}
	if group != nil {
		return group.Platform, false, nil
	}
	if groupID != nil {
		group, err := s.resolveGroupByID(ctx, *groupID)
		if err != nil {
			return "", false, err
		}
		return group.Platform, false, nil
	}
	return PlatformAnthropic, false, nil
}

func (s *GatewayService) listSchedulableAccounts(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]Account, bool, error) {
	if s.schedulerSnapshot != nil {
		accounts, useMixed, err := s.schedulerSnapshot.ListSchedulableAccounts(ctx, groupID, platform, hasForcePlatform)
		if err == nil {
			accounts = s.filterAccountsBySchedulingThreshold(ctx, accounts)
			if platform == PlatformGrok || strings.EqualFold(platform, PlatformGrok) {
				accounts = s.filterGrokFreeQuotaAccountsForGateway(ctx, accounts)
			}
			slog.Debug("account_scheduling_list_snapshot",
				"group_id", derefGroupID(groupID),
				"platform", platform,
				"use_mixed", useMixed,
				"count", len(accounts))
			if slog.Default().Enabled(ctx, slog.LevelDebug) {
				for _, acc := range accounts {
					slog.Debug("account_scheduling_account_detail",
						"account_id", acc.ID,
						"name", acc.Name,
						"platform", acc.Platform,
						"type", acc.Type,
						"status", acc.Status,
						"tls_fingerprint", acc.IsTLSFingerprintEnabled())
				}
			}
		}
		return accounts, useMixed, err
	}
	useMixed := (platform == PlatformAnthropic || platform == PlatformGemini) && !hasForcePlatform
	if useMixed {
		platforms := []string{platform, PlatformAntigravity}
		var accounts []Account
		var err error
		if groupID != nil {
			accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, platforms)
		} else if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
			accounts, err = s.accountRepo.ListSchedulableByPlatforms(ctx, platforms)
		} else {
			accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatforms(ctx, platforms)
		}
		if err != nil {
			slog.Debug("account_scheduling_list_failed",
				"group_id", derefGroupID(groupID),
				"platform", platform,
				"error", err)
			return nil, useMixed, err
		}
		filtered := make([]Account, 0, len(accounts))
		for _, acc := range accounts {
			if acc.Platform == PlatformAntigravity && !acc.IsMixedSchedulingEnabled() {
				continue
			}
			filtered = append(filtered, acc)
		}
		slog.Debug("account_scheduling_list_mixed",
			"group_id", derefGroupID(groupID),
			"platform", platform,
			"raw_count", len(accounts),
			"filtered_count", len(filtered))
		if slog.Default().Enabled(ctx, slog.LevelDebug) {
			for _, acc := range filtered {
				slog.Debug("account_scheduling_account_detail",
					"account_id", acc.ID,
					"name", acc.Name,
					"platform", acc.Platform,
					"type", acc.Type,
					"status", acc.Status,
					"tls_fingerprint", acc.IsTLSFingerprintEnabled())
			}
		}
		return s.filterAccountsBySchedulingThreshold(ctx, filtered), useMixed, nil
	}

	var accounts []Account
	var err error
	if s.cfg != nil && s.cfg.RunMode == config.RunModeSimple {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, platform)
	} else if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
		// 分组内无账号则返回空列表，由上层处理错误，不再回退到全平台查询
	} else {
		accounts, err = s.accountRepo.ListSchedulableUngroupedByPlatform(ctx, platform)
	}
	if err != nil {
		slog.Debug("account_scheduling_list_failed",
			"group_id", derefGroupID(groupID),
			"platform", platform,
			"error", err)
		return nil, useMixed, err
	}
	slog.Debug("account_scheduling_list_single",
		"group_id", derefGroupID(groupID),
		"platform", platform,
		"count", len(accounts))
	if slog.Default().Enabled(ctx, slog.LevelDebug) {
		for _, acc := range accounts {
			slog.Debug("account_scheduling_account_detail",
				"account_id", acc.ID,
				"name", acc.Name,
				"platform", acc.Platform,
				"type", acc.Type,
				"status", acc.Status,
				"tls_fingerprint", acc.IsTLSFingerprintEnabled())
		}
	}
	accounts = s.filterAccountsBySchedulingThreshold(ctx, accounts)
	if platform == PlatformGrok || strings.EqualFold(platform, PlatformGrok) {
		accounts = s.filterGrokFreeQuotaAccountsForGateway(ctx, accounts)
	}
	return accounts, useMixed, nil
}

// IsSingleAntigravityAccountGroup 检查指定分组是否只有一个 antigravity 平台的可调度账号。
// 用于 Handler 层在首次请求时提前设置 SingleAccountRetry context，
// 避免单账号分组收到 503 时错误地设置模型限流标记导致后续请求连续快速失败。
func (s *GatewayService) IsSingleAntigravityAccountGroup(ctx context.Context, groupID *int64) bool {
	accounts, _, err := s.listSchedulableAccounts(ctx, groupID, PlatformAntigravity, true)
	if err != nil {
		return false
	}
	return len(accounts) == 1
}

func (s *GatewayService) isAccountAllowedForPlatform(account *Account, platform string, useMixed bool) bool {
	if account == nil {
		return false
	}
	if useMixed {
		if account.Platform == platform {
			return true
		}
		return account.Platform == PlatformAntigravity && account.IsMixedSchedulingEnabled()
	}
	return account.Platform == platform
}

func (s *GatewayService) isAccountSchedulableForSelection(account *Account) bool {
	if account == nil {
		return false
	}
	return account.IsSchedulable()
}

func (s *GatewayService) isAccountSchedulableForModelSelection(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil {
		return false
	}
	routingModel := s.channelMappedModelForAccountLayer(ctx, requestedModel)
	return account.IsSchedulableForModelWithContext(ctx, routingModel)
}

// shouldClearStickySessionForAccountLayer 使用渠道映射后的模型检查粘性账号模型限流。
func (s *GatewayService) shouldClearStickySessionForAccountLayer(ctx context.Context, account *Account, requestedModel string) bool {
	routingModel := s.channelMappedModelForAccountLayer(ctx, requestedModel)
	return shouldClearStickySession(account, routingModel)
}

// isAccountEligibleExceptModelSupport 判断账号除模型白名单/映射外是否具备本次请求资格。
func (s *GatewayService) isAccountEligibleExceptModelSupport(ctx context.Context, account *Account, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *Group) bool {
	if !account.allowsProtocolRequest(ctx) {
		return false
	}
	if account == nil {
		return false
	}
	if excludedIDs != nil {
		if _, excluded := excludedIDs[account.ID]; excluded {
			return false
		}
	}
	if !s.isAccountSchedulableForSelection(account) || !s.isAccountAllowedForPlatform(account, platform, useMixed) {
		return false
	}
	if schedGroup != nil && schedGroup.RequirePrivacySet && !account.IsPrivacySet() {
		return false
	}
	if !s.isAccountSchedulableForQuota(account) || !s.isAccountSchedulableForWindowCost(ctx, account, false) || !s.isAccountSchedulableForRPM(ctx, account, false) {
		return false
	}
	if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByChannel(ctx, *groupID, account, requestedModel) {
		return false
	}
	return true
}

// shouldUseGroupModelUnsupportedError 判断账号选择失败是否明确由分组模型限制导致。
func (s *GatewayService) shouldUseGroupModelUnsupportedError(ctx context.Context, accounts []Account, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *Group) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(accounts) == 0 {
		return false
	}
	hasRelevantAccount := false
	for i := range accounts {
		acc := &accounts[i]
		if !s.isAccountEligibleExceptModelSupport(ctx, acc, requestedModel, platform, excludedIDs, useMixed, groupID, schedGroup) {
			continue
		}
		hasRelevantAccount = true
		if s.isModelSupportedByAccountWithContext(ctx, acc, requestedModel) {
			return false
		}
	}
	return hasRelevantAccount
}

// groupModelUnsupportedErrorIfApplicable 在确认是分组模型限制时返回 typed error。
func (s *GatewayService) groupModelUnsupportedErrorIfApplicable(ctx context.Context, accounts []Account, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *Group) error {
	if s.shouldUseGroupModelUnsupportedError(ctx, accounts, requestedModel, platform, excludedIDs, useMixed, groupID, schedGroup) {
		if err := newGroupModelUnsupportedError(platform, requestedModel, accounts); err != nil {
			return err
		}
	}
	return nil
}

// isAccountInGroup checks if the account belongs to the specified group.
// When groupID is nil, returns true only for ungrouped accounts (no group assignments).
func (s *GatewayService) isAccountInGroup(account *Account, groupID *int64) bool {
	if account == nil {
		return false
	}
	if groupID == nil {
		// 无分组的 API Key 只能使用未分组的账号
		return len(account.AccountGroups) == 0
	}
	for _, ag := range account.AccountGroups {
		if ag.GroupID == *groupID {
			return true
		}
	}
	return false
}

func (s *GatewayService) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*AcquireResult, error) {
	if isAdvancedSchedulerNoSlotSelection(ctx) {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}

// windowCostFromPrefetchContext 保留旧观察入口，不持有第二份预取状态。
func windowCostFromPrefetchContext(ctx context.Context, id int64) (float64, bool) {
	return billing.PrefetchedWindowCost(ctx, id)
}
func (s *GatewayService) withWindowCostPrefetch(ctx context.Context, accounts []Account) context.Context {
	if ctx == nil || len(accounts) == 0 || s.sessionLimitCache == nil || s.usageLogRepo == nil {
		return ctx
	}
	values := make([]billing.CostWindowInput, len(accounts))
	for i := range accounts {
		values[i] = costWindowInput(&accounts[i])
	}
	return s.windowCostGuard().Prefetch(ctx, values)
}

// isAccountSchedulableForQuota 检查账号是否在配额限制内
// 适用于配置了 quota_limit 的 apikey 和 bedrock 类型账号
func (s *GatewayService) isAccountSchedulableForQuota(account *Account) bool {
	if !account.IsAPIKeyOrBedrock() {
		return true
	}
	return !account.IsQuotaExceeded()
}

// isAccountSchedulableForWindowCost 检查账号是否可根据窗口费用进行调度
// 仅适用于 Anthropic OAuth/SetupToken 账号
// 返回 true 表示可调度，false 表示不可调度
func (s *GatewayService) isAccountSchedulableForWindowCost(ctx context.Context, account *Account, sticky bool) bool {
	return s.windowCostGuard().Allow(ctx, costWindowInput(account), sticky)
}

// withRPMPrefetch 批量预取所有候选账号的 RPM 计数
func (s *GatewayService) withRPMPrefetch(ctx context.Context, accounts []Account) context.Context {
	if s.rpmCache == nil {
		return ctx
	}

	var ids []int64
	for i := range accounts {
		if accounts[i].IsAnthropicOAuthOrSetupToken() && accounts[i].GetBaseRPM() > 0 {
			ids = append(ids, accounts[i].ID)
		}
	}
	return scheduler.PrefetchRPM(ctx, s.rpmCache, ids)
}

// isAccountSchedulableForRPM 检查账号是否可根据 RPM 进行调度
// 仅适用于 Anthropic OAuth/SetupToken 账号
func (s *GatewayService) isAccountSchedulableForRPM(ctx context.Context, account *Account, sticky bool) bool {
	if !account.IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return scheduler.AllowAccountRPM(ctx, s.rpmCache, scheduler.AccountRPMInput{ID: account.ID, Enabled: true, Base: account.GetBaseRPM(), Buffer: account.GetRPMStickyBuffer(), Strategy: account.GetRPMStrategy()}, sticky)
}

// IncrementAccountRPM increments the RPM counter for the given account.
// 已知 TOCTOU 竞态：调度时读取 RPM 计数与此处递增之间存在时间窗口，
// 高并发下可能短暂超出 RPM 限制。这是与 WindowCost 一致的 soft-limit
// 设计权衡——可接受的少量超额优于加锁带来的延迟和复杂度。
func (s *GatewayService) IncrementAccountRPM(ctx context.Context, id int64) error {
	return scheduler.IncrementAccountRPM(ctx, s.rpmCache, id)
}

// checkAndRegisterSession 检查并注册会话，用于会话数量限制
// 仅适用于 Anthropic OAuth/SetupToken 账号
// sessionID: 会话标识符（使用粘性会话的 hash）
// 返回 true 表示允许（在限制内或会话已存在），false 表示拒绝（超出限制且是新会话）
func (s *GatewayService) checkAndRegisterSession(ctx context.Context, account *Account, session string) bool {
	if isAdvancedSchedulerNoSlotSelection(ctx) {
		return true
	}
	return scheduler.RegisterSession(ctx, s.sessionLimitCache, schedulerSessionBinding(account, session))
}

// ReleaseAccountSession 立即释放会话槽（不等待空闲超时）
// 供 handler 在请求最终失败（选号成功但转发失败/客户端中断）时调用：
// 上游从未真正服务该会话，若继续占槽，max_sessions 受限的账号会被失败请求的
// session hash 卡满整个空闲窗口，后续新会话全部被拒。
// 适用条件与 checkAndRegisterSession 对齐；不适用账号为 no-op，幂等可安全重复调用。
func (s *GatewayService) ReleaseAccountSession(ctx context.Context, account *Account, session string) {
	if s == nil {
		return
	}
	scheduler.FinishSession(ctx, s.sessionLimitCache, schedulerSessionBinding(account, session), scheduler.AttemptOutcome{}, LegacySchedulerDiagnostics())
}
func schedulerSessionBinding(account *Account, session string) scheduler.SessionBinding {
	if account == nil {
		return scheduler.SessionBinding{}
	}
	return scheduler.SessionBinding{AccountID: account.ID, SessionID: session, Enabled: account.IsAnthropicOAuthOrSetupToken(), Limit: account.GetMaxSessions(), IdleTimeout: time.Duration(account.GetSessionIdleTimeoutMinutes()) * time.Minute}
}

func (s *GatewayService) getSchedulableAccount(ctx context.Context, accountID int64) (*Account, error) {
	var (
		account *Account
		err     error
	)
	if s.schedulerSnapshot != nil {
		account, err = s.schedulerSnapshot.GetAccount(ctx, accountID)
	} else {
		account, err = s.accountRepo.GetByID(ctx, accountID)
	}
	if err != nil || account == nil {
		return account, err
	}
	if s.isAccountBlockedBySchedulingThreshold(ctx, account) {
		return nil, nil
	}
	// 粘性和非列表选择必须与 listSchedulableAccounts 一样遵守免费层软门禁。
	if account.IsGrok() {
		if gated := s.filterGrokFreeQuotaAccountsForGateway(ctx, []Account{*account}); len(gated) == 0 {
			return nil, nil
		}
	}
	return account, nil
}

func (s *GatewayService) filterAccountsBySchedulingThreshold(ctx context.Context, accounts []Account) []Account {
	if len(accounts) == 0 {
		return accounts
	}

	filtered := make([]Account, 0, len(accounts))
	for i := range accounts {
		if s.isAccountBlockedBySchedulingThreshold(ctx, &accounts[i]) {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}

func (s *GatewayService) isAccountBlockedBySchedulingThreshold(ctx context.Context, account *Account) bool {
	if s == nil || s.rateLimitService == nil || account == nil {
		return false
	}
	return s.rateLimitService.ApplyAccountSchedulingThreshold(ctx, account)
}

func (s *GatewayService) hydrateSelectedAccount(ctx context.Context, account *Account) (*Account, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := s.schedulerSnapshot.GetAccount(ctx, account.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected gateway account %d not found during hydration", account.ID)
	}
	return hydrated, nil
}

func (s *GatewayService) newSelectionResult(ctx context.Context, account *Account, acquired bool, release func(), waitPlan *AccountWaitPlan) (*AccountSelectionResult, error) {
	// B03：已取得的槽位立即转交幂等租约，补全失败也必须归还。
	attempt := scheduler.NewAttemptLease(scheduler.RequestLease(ctx), nil, release)
	if release != nil {
		release = attempt.Release
	}

	hydrated, err := s.hydrateSelectedAccount(ctx, account)
	if err != nil {
		if release != nil {
			release()
		}
		return nil, err
	}
	selection := &AccountSelectionResult{
		Account:     hydrated,
		Acquired:    acquired,
		ReleaseFunc: release,
		WaitPlan:    waitPlan,
	}
	if group, ok := ctx.Value(ctxkey.Group).(*Group); ok && IsGroupContextValid(group) && group.UsesAdvancedScheduler() {
		// 让转发层只依据选择结果写入高级运行时反馈，避免基础分组污染统计。
		selection.AdvancedScheduler = true
		feedback := s.advancedSchedulerEffectiveSettingsForRequest(ctx, &group.ID).feedback
		selection.AdvancedSchedulerFeedback = &feedback
	}
	return selection, nil
}

// sameAccountWithLoadGroup 判断两个 accountWithLoad 是否属于同一排序组

// shuffleWithinPriorityAndLastUsed 对排序后的 []*Account 切片，按 (Priority, LastUsedAt) 分组后组内随机打乱。
//
// 注意：当 preferOAuth=true 时，需要保证 OAuth 账号在同组内仍然优先，否则会把排序时的偏好打散掉。
// 因此这里采用"组内分区 + 分区内 shuffle"的方式：
// - 先把同组账号按 (OAuth / 非 OAuth) 拆成两段，保持 OAuth 段在前；
// - 再分别在各段内随机打散，避免热点。

// sameAccountGroup 判断两个 Account 是否属于同一排序组（Priority + LastUsedAt）

// sameLastUsedAt 判断两个 LastUsedAt 是否相同（精度到秒）

type selectionFailureStats struct {
	Total              int
	Eligible           int
	Excluded           int
	Unschedulable      int
	PlatformFiltered   int
	ModelUnsupported   int
	ModelRateLimited   int
	SamplePlatformIDs  []int64
	SampleMappingIDs   []int64
	SampleRateLimitIDs []string
}

type selectionFailureDiagnosis struct {
	Category string
	Detail   string
}

func (s *GatewayService) logDetailedSelectionFailure(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	platform string,
	accounts []Account,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureStats {
	stats := s.collectSelectionFailureStats(ctx, accounts, requestedModel, platform, excludedIDs, allowMixedScheduling)
	logger.LegacyPrintf(
		"service.gateway",
		"[SelectAccountDetailed] group_id=%v model=%s platform=%s session=%s total=%d eligible=%d excluded=%d unschedulable=%d platform_filtered=%d model_unsupported=%d model_rate_limited=%d sample_platform_filtered=%v sample_model_unsupported=%v sample_model_rate_limited=%v",
		derefGroupID(groupID),
		requestedModel,
		platform,
		shortSessionHash(sessionHash),
		stats.Total,
		stats.Eligible,
		stats.Excluded,
		stats.Unschedulable,
		stats.PlatformFiltered,
		stats.ModelUnsupported,
		stats.ModelRateLimited,
		stats.SamplePlatformIDs,
		stats.SampleMappingIDs,
		stats.SampleRateLimitIDs,
	)
	return stats
}

func (s *GatewayService) collectSelectionFailureStats(
	ctx context.Context,
	accounts []Account,
	requestedModel string,
	platform string,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureStats {
	stats := selectionFailureStats{
		Total: len(accounts),
	}

	for i := range accounts {
		acc := &accounts[i]
		diagnosis := s.diagnoseSelectionFailure(ctx, acc, requestedModel, platform, excludedIDs, allowMixedScheduling)
		switch diagnosis.Category {
		case "excluded":
			stats.Excluded++
		case "unschedulable":
			stats.Unschedulable++
		case "platform_filtered":
			stats.PlatformFiltered++
			stats.SamplePlatformIDs = appendSelectionFailureSampleID(stats.SamplePlatformIDs, acc.ID)
		case "model_unsupported":
			stats.ModelUnsupported++
			stats.SampleMappingIDs = appendSelectionFailureSampleID(stats.SampleMappingIDs, acc.ID)
		case "model_rate_limited":
			stats.ModelRateLimited++
			routingModel := s.channelMappedModelForAccountLayer(ctx, requestedModel)
			remaining := acc.GetRateLimitRemainingTimeWithContext(ctx, routingModel).Truncate(time.Second)
			stats.SampleRateLimitIDs = appendSelectionFailureRateSample(stats.SampleRateLimitIDs, acc.ID, remaining)
		default:
			stats.Eligible++
		}
	}

	return stats
}

func (s *GatewayService) diagnoseSelectionFailure(
	ctx context.Context,
	acc *Account,
	requestedModel string,
	platform string,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureDiagnosis {
	if acc == nil {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "account_nil"}
	}
	if _, excluded := excludedIDs[acc.ID]; excluded {
		return selectionFailureDiagnosis{Category: "excluded"}
	}
	if !s.isAccountSchedulableForSelection(acc) {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "generic_unschedulable"}
	}
	if isPlatformFilteredForSelection(acc, platform, allowMixedScheduling) {
		return selectionFailureDiagnosis{
			Category: "platform_filtered",
			Detail:   fmt.Sprintf("account_platform=%s requested_platform=%s", acc.Platform, strings.TrimSpace(platform)),
		}
	}
	if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, acc, requestedModel) {
		return selectionFailureDiagnosis{
			Category: "model_unsupported",
			Detail:   fmt.Sprintf("model=%s", requestedModel),
		}
	}
	if !s.isAccountSchedulableForModelSelection(ctx, acc, requestedModel) {
		routingModel := s.channelMappedModelForAccountLayer(ctx, requestedModel)
		remaining := acc.GetRateLimitRemainingTimeWithContext(ctx, routingModel).Truncate(time.Second)
		return selectionFailureDiagnosis{
			Category: "model_rate_limited",
			Detail:   fmt.Sprintf("remaining=%s", remaining),
		}
	}
	return selectionFailureDiagnosis{Category: "eligible"}
}

func isPlatformFilteredForSelection(acc *Account, platform string, allowMixedScheduling bool) bool {
	if acc == nil {
		return true
	}
	if allowMixedScheduling {
		if acc.Platform == PlatformAntigravity {
			return !acc.IsMixedSchedulingEnabled()
		}
		return acc.Platform != platform
	}
	if strings.TrimSpace(platform) == "" {
		return false
	}
	return acc.Platform != platform
}

func appendSelectionFailureSampleID(samples []int64, id int64) []int64 {
	const limit = 5
	if len(samples) >= limit {
		return samples
	}
	return append(samples, id)
}

func appendSelectionFailureRateSample(samples []string, accountID int64, remaining time.Duration) []string {
	const limit = 5
	if len(samples) >= limit {
		return samples
	}
	return append(samples, fmt.Sprintf("%d(%s)", accountID, remaining))
}

func summarizeSelectionFailureStats(stats selectionFailureStats) string {
	return fmt.Sprintf(
		"total=%d eligible=%d excluded=%d unschedulable=%d platform_filtered=%d model_unsupported=%d model_rate_limited=%d",
		stats.Total,
		stats.Eligible,
		stats.Excluded,
		stats.Unschedulable,
		stats.PlatformFiltered,
		stats.ModelUnsupported,
		stats.ModelRateLimited,
	)
}

// isModelSupportedByAccountWithContext 根据渠道映射后的模型检查账号支持能力。
func (s *GatewayService) isModelSupportedByAccountWithContext(ctx context.Context, account *Account, requestedModel string) bool {
	routingModel := s.channelMappedModelForAccountLayer(ctx, requestedModel)
	return s.isRoutingModelSupportedByAccountWithContext(ctx, account, routingModel)
}

// isRoutingModelSupportedByAccountWithContext 检查已经过渠道映射的模型，避免重复执行渠道映射。
func (s *GatewayService) isRoutingModelSupportedByAccountWithContext(ctx context.Context, account *Account, routingModel string) bool {
	if account == nil {
		return false
	}
	if account.Platform == PlatformAntigravity {
		if strings.TrimSpace(routingModel) == "" {
			return true
		}

		mapped := mapAntigravityModel(account, routingModel)
		if mapped == "" {
			return false
		}

		if enabled, ok := ThinkingEnabledFromContext(ctx); ok {
			finalModel := applyThinkingModelSuffix(mapped, enabled)
			if finalModel == mapped {
				return true
			}
			return account.IsModelSupported(finalModel)
		}
		return true
	}
	return s.isModelSupportedByAccount(account, routingModel)
}

func (s *GatewayService) channelMappedModelForAccountLayer(ctx context.Context, requestedModel string) string {
	if s == nil || s.channelService == nil || strings.TrimSpace(requestedModel) == "" {
		return requestedModel
	}
	group, ok := ctx.Value(ctxkey.Group).(*Group)
	if !ok || !IsGroupContextValid(group) {
		return requestedModel
	}
	groupID := group.ID
	return s.channelMappedModelForGroup(ctx, &groupID, requestedModel)
}

// isModelSupportedByAccount 根据账户平台检查模型支持（无 context，用于非 Antigravity 平台）
func (s *GatewayService) isModelSupportedByAccount(account *Account, requestedModel string) bool {
	if account.Platform == PlatformAntigravity {
		if strings.TrimSpace(requestedModel) == "" {
			return true
		}
		return mapAntigravityModel(account, requestedModel) != ""
	}
	if account.IsBedrock() {
		_, ok := ResolveBedrockModelID(account, requestedModel)
		return ok
	}
	// OpenAI 透传模式：仅替换认证，允许所有模型
	if account.Platform == PlatformOpenAI && account.IsOpenAIPassthroughEnabled() {
		return true
	}
	// Anthropic 非 APIKey 账号必须先执行账号映射，再按真正上游模型检查最终白名单。
	if account.Platform == PlatformAnthropic && account.Type != AccountTypeAPIKey {
		accountMappedModel := resolveAccountMappedModelForForward(account, requestedModel)
		upstreamModel := resolveAnthropicAccountUpstreamModel(account, accountMappedModel)
		return account.isFinalModelWhitelisted(upstreamModel)
	}
	// 其他平台使用账户的模型支持检查
	return account.IsModelSupported(requestedModel)
}

// NewSessionAttempts 为一次请求提供唯一会话完成集合，不保存全局副本。
func (s *GatewayService) NewSessionAttempts() *scheduler.SessionAttempts {
	return scheduler.NewSessionAttempts(s.sessionLimitCache, LegacySchedulerDiagnostics())
}

// TrackSessionAttempt 只投影原账号会话参数，最终状态由执行入口传入。
func (s *GatewayService) TrackSessionAttempt(attempts *scheduler.SessionAttempts, account *Account, session string) {
	attempts.Track(schedulerSessionBinding(account, session))
}
