package selection

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// SelectAccount 选择账号（粘性会话+优先级）
func (s *Generic) SelectAccount(ctx context.Context, groupID *int64, sessionHash string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModel(ctx, groupID, sessionHash, "")
}

// SelectAccountForModel 选择支持指定模型的账号（粘性会话+优先级+模型映射）
func (s *Generic) SelectAccountForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionAccount, error) {
	return s.SelectAccountForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectAccountForModelWithExclusions selects an account supporting the requested model while excluding specified accounts.
func (s *Generic) SelectAccountForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionAccount, error) {
	core, scope := s.genericSelector()
	selected, err := core.SelectOnly(ctx, schedulercore.SelectionInput{GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	return scope.oldAccount(selected), err
}

// SelectAccountWithLoadAwareness selects account with load-awareness and wait plan.
// metadataUserID: 用于客户端亲和调度，从中提取客户端 ID
// sub2apiUserID: 系统用户 ID，用于二维亲和调度
// @project-doc docs/architecture/gateway_request_lifecycle.md#account_selection_and_failover
func (s *Generic) SelectAccountWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, metadataUserID string, sub2apiUserID int64) (*gatewayprovider.SelectionResult, error) {
	core, scope := s.genericSelector()
	plan, _ := requeststate.RoutePlanFromContext(ctx)
	result, err := core.Select(ctx, schedulercore.SelectionInput{RoutePlan: plan, GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	return scope.restore(result), err
}

// ReportAdvancedAccountScheduleResult 将通用网关转发结果写入高级调度运行时反馈。
// 只有实际由高级调度器选出的请求才会更新统计，基础调度器保持原有行为。
func (s *Generic) ReportAdvancedAccountScheduleResult(selection *gatewayprovider.SelectionResult, accountID int64, success bool, result *forwardcore.MessagesResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler || accountID <= 0 {
		return
	}
	var firstTokenMs *int
	if result != nil {
		firstTokenMs = result.FirstTokenMs
	}
	if selection.AdvancedSchedulerFeedback != nil {
		s.advancedSchedulerStats().Report(accountID, success, firstTokenMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.advancedSchedulerStats().Report(accountID, success, firstTokenMs)
}

// RecordAdvancedAccountSwitch 记录高级调度请求的一次账号切换事件。
func (s *Generic) RecordAdvancedAccountSwitch(selection *gatewayprovider.SelectionResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler {
		return
	}
	s.advancedSchedulerStats().ReportSwitch()
}

// tryAcquireByAdvancedScheduler 在既有硬过滤完成后按通用评分和 Top-K 加权顺序复核并发槽位。
// 所有账号仍逐个调用 tryAcquireAccountSlot，因此负载快照过期时不会越过真实并发上限。
func (s *Generic) tryAcquireByAdvancedScheduler(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	available []accountWithLoad,
) (*gatewayprovider.SelectionResult, bool, error) {
	core, scope := s.genericSelector()
	result, found, err := core.TryAdvanced(ctx, groupID, sessionHash, scope.loads(available))
	return scope.restore(result), found, err
}

// advancedSchedulerStats 返回网关级运行时反馈；测试或旧构造路径未初始化时惰性补齐。
func (s *Generic) advancedSchedulerStats() *schedulercore.RuntimeStats {
	if s == nil {
		return nil
	}
	if s.advancedAccountStats == nil {
		s.advancedAccountStats = schedulercore.NewRuntimeStats(time.Now)
	}
	return s.advancedAccountStats
}

// advancedSchedulerEffectiveSettingsForRequest 将最终分组覆盖应用到网关通用设置之上。
func (s *Generic) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, id *int64) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	group := schedulerRequestGroup(ctx, id, s.schedulerSnapshot != nil, s.readSchedulingGroup)
	return s.schedulerParameters.Effective(ctx, schedulerGroupOverrides(group))
}

func (s *Generic) schedulingConfig() schedulercore.FlowOptions {
	return s.options.Scheduling
}

func (s *Generic) withGroupContext(ctx context.Context, group *routing.Group) context.Context {
	if !routing.IsGroupContextValid(group) {
		return ctx
	}
	if existing, ok := requeststate.GroupFromContext(ctx); ok && existing != nil && existing.ID == group.ID && routing.IsGroupContextValid(existing) {
		return ctx
	}
	return requeststate.WithGroup(ctx, group)
}

func (s *Generic) groupFromContext(ctx context.Context, groupID int64) *routing.Group {
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == groupID {
		return group
	}
	return nil
}

func (s *Generic) resolveGroupByID(ctx context.Context, groupID int64) (*routing.Group, error) {
	if group := s.groupFromContext(ctx, groupID); group != nil {
		return group, nil
	}
	group, err := s.groupRepo.GetByIDLite(ctx, groupID)
	if err != nil {
		return nil, fmt.Errorf("get group failed: %w", err)
	}
	return group, nil
}

func (s *Generic) ResolveGroupByID(ctx context.Context, groupID int64) (*routing.Group, error) {
	return s.resolveGroupByID(ctx, groupID)
}

func (s *Generic) routingAccountIDsForRequest(ctx context.Context, groupID *int64, requestedModel string, platform string) []int64 {
	if groupID == nil || requestedModel == "" || platform != capability.PlatformAnthropic {
		return nil
	}
	group, err := s.resolveGroupByID(ctx, *groupID)
	if err != nil || group == nil {
		if s.debugModelRoutingEnabled() {
			logging.LegacyPrintf("service.gateway", "[ModelRoutingDebug] resolve group failed: group_id=%v model=%s platform=%s err=%v", derefGroupID(groupID), requestedModel, platform, err)
		}
		return nil
	}

	if group.Platform != capability.PlatformAnthropic {
		if s.debugModelRoutingEnabled() {
			logging.LegacyPrintf("service.gateway", "[ModelRoutingDebug] skip: non-anthropic group platform: group_id=%d group_platform=%s model=%s", group.ID, group.Platform, requestedModel)
		}
		return nil
	}
	routingModel := s.groupMappedModelForGroup(ctx, groupID, requestedModel)
	ids := group.GetRoutingAccountIDs(routingModel)
	if s.debugModelRoutingEnabled() {
		logging.LegacyPrintf("service.gateway", "[ModelRoutingDebug] routing lookup: group_id=%d model=%s enabled=%v rules=%d matched_ids=%v",
			group.ID, requestedModel, group.ModelRoutingEnabled, len(group.ModelRouting), ids)
	}
	return ids
}

func (s *Generic) resolveGatewayGroup(ctx context.Context, groupID *int64) (*routing.Group, *int64, error) {
	return routing.ResolveClientGroup(ctx, groupID, s.resolveGroupByID, requeststate.IsClaudeCodeClient, routing.ClientGroupPolicy{})
}

// checkClaudeCodeRestriction 检查分组的 Claude Code 客户端限制
// 如果分组启用了 claude_code_only 且请求不是来自 Claude Code 客户端：
//   - 有降级分组：返回降级分组的 ID
//   - 无降级分组：返回 ErrClaudeCodeOnly 错误
func (s *Generic) checkClaudeCodeRestriction(ctx context.Context, groupID *int64) (*routing.Group, *int64, error) {
	if groupID == nil {
		return nil, groupID, nil
	}

	if forcePlatform, hasForcePlatform := apikey.ForcePlatformFromContext(ctx); hasForcePlatform && forcePlatform != "" {
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

func (s *Generic) resolvePlatform(ctx context.Context, groupID *int64, group *routing.Group) (string, bool, error) {
	forcePlatform, hasForcePlatform := apikey.ForcePlatformFromContext(ctx)
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
	return capability.PlatformAnthropic, false, nil
}

func (s *Generic) listSchedulableAccounts(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]gatewayprovider.ExecutionAccount, bool, error) {
	if s.schedulerSnapshot != nil {
		accounts, useMixed, err := readSnapshotAccounts(ctx, s.schedulerSnapshot, groupID, platform, hasForcePlatform)
		if err == nil {
			accounts = s.filterAccountsBySchedulingThreshold(ctx, accounts)
			if platform == capability.PlatformGrok || strings.EqualFold(platform, capability.PlatformGrok) {
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
						"account_id", acc.Record.ID,
						"name", acc.Record.Name,
						"platform", acc.Record.Platform,
						"type", acc.Record.Type,
						"status", acc.Record.Status,
						"tls_fingerprint", acc.View().IsTLSFingerprintEnabled())
				}
			}
		}
		return accounts, useMixed, err
	}
	useMixed := (platform == capability.PlatformAnthropic || platform == capability.PlatformGemini) && !hasForcePlatform
	if useMixed {
		platforms := []string{platform, capability.PlatformAntigravity}
		var accounts []gatewayprovider.ExecutionAccount
		var err error
		if groupID != nil {
			accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, platforms)
		} else if s.options.Simple {
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
		filtered := make([]gatewayprovider.ExecutionAccount, 0, len(accounts))
		for _, acc := range accounts {
			if acc.Record.Platform == capability.PlatformAntigravity && !acc.View().IsMixedSchedulingEnabled() {
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
					"account_id", acc.Record.ID,
					"name", acc.Record.Name,
					"platform", acc.Record.Platform,
					"type", acc.Record.Type,
					"status", acc.Record.Status,
					"tls_fingerprint", acc.View().IsTLSFingerprintEnabled())
			}
		}
		return s.filterAccountsBySchedulingThreshold(ctx, filtered), useMixed, nil
	}

	var accounts []gatewayprovider.ExecutionAccount
	var err error
	if s.options.Simple {
		accounts, err = s.accountRepo.ListSchedulableByPlatform(ctx, platform)
	} else if groupID != nil {
		accounts, err = s.accountRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
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
				"account_id", acc.Record.ID,
				"name", acc.Record.Name,
				"platform", acc.Record.Platform,
				"type", acc.Record.Type,
				"status", acc.Record.Status,
				"tls_fingerprint", acc.View().IsTLSFingerprintEnabled())
		}
	}
	accounts = s.filterAccountsBySchedulingThreshold(ctx, accounts)
	if platform == capability.PlatformGrok || strings.EqualFold(platform, capability.PlatformGrok) {
		accounts = s.filterGrokFreeQuotaAccountsForGateway(ctx, accounts)
	}
	return accounts, useMixed, nil
}

// IsSingleAntigravityAccountGroup 检查指定分组是否只有一个 antigravity 平台的可调度账号。
// 用于 Handler 层在首次请求时提前设置 SingleAccountRetry context，
// 避免单账号分组收到 503 时错误地设置模型限流标记导致后续请求连续快速失败。
func (s *Generic) IsSingleAntigravityAccountGroup(ctx context.Context, groupID *int64) bool {
	accounts, _, err := s.listSchedulableAccounts(ctx, groupID, capability.PlatformAntigravity, true)
	if err != nil {
		return false
	}
	return len(accounts) == 1
}

func (s *Generic) isAccountAllowedForPlatform(account *gatewayprovider.ExecutionAccount, platform string, useMixed bool) bool {
	if account == nil {
		return false
	}
	if useMixed {
		if account.Record.Platform == platform {
			return true
		}
		return account.Record.Platform == capability.PlatformAntigravity && account.View().IsMixedSchedulingEnabled()
	}
	return account.Record.Platform == platform
}

func (s *Generic) isAccountSchedulableForSelection(account *gatewayprovider.ExecutionAccount) bool {
	if account == nil {
		return false
	}
	return account.View().IsSchedulable()
}

func (s *Generic) isAccountSchedulableForModelSelection(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	if account == nil {
		return false
	}
	routingModel := s.groupMappedModelForAccountLayer(ctx, requestedModel)
	return gatewayprovider.ExecutionModelPolicy(account).Schedulable(ctx, routingModel)
}

// shouldClearStickySessionForAccountLayer 使用分组映射后的模型检查粘性账号模型限流。
func (s *Generic) shouldClearStickySessionForAccountLayer(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	routingModel := s.groupMappedModelForAccountLayer(ctx, requestedModel)
	return shouldClearStickySession(account, routingModel)
}

// isAccountEligibleExceptModelSupport 判断账号除模型白名单/映射外是否具备本次请求资格。
func (s *Generic) isAccountEligibleExceptModelSupport(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) bool {
	if !gatewayprovider.ExecutionModelPolicy(account).AllowsProtocol(ctx) {
		return false
	}
	if account == nil {
		return false
	}
	if excludedIDs != nil {
		if _, excluded := excludedIDs[account.Record.ID]; excluded {
			return false
		}
	}
	if !s.isAccountSchedulableForSelection(account) || !s.isAccountAllowedForPlatform(account, platform, useMixed) {
		return false
	}
	if schedGroup != nil && schedGroup.RequirePrivacySet && !account.View().IsPrivacySet() {
		return false
	}
	if !s.isAccountSchedulableForQuota(account) || !s.isAccountSchedulableForWindowCost(ctx, account, false) || !s.isAccountSchedulableForRPM(ctx, account, false) {
		return false
	}
	if groupID != nil && s.needsUpstreamGroupRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByGroup(ctx, *groupID, account, requestedModel) {
		return false
	}
	return true
}

// shouldUseGroupModelUnsupportedError 判断账号选择失败是否明确由分组模型限制导致。
func (s *Generic) shouldUseGroupModelUnsupportedError(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) bool {
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
func (s *Generic) groupModelUnsupportedErrorIfApplicable(ctx context.Context, accounts []gatewayprovider.ExecutionAccount, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) error {
	if s.shouldUseGroupModelUnsupportedError(ctx, accounts, requestedModel, platform, excludedIDs, useMixed, groupID, schedGroup) {
		if err := routing.NewGroupModelRejection(platform, requestedModel, modelRejectionSources(accounts)); err != nil {
			return err
		}
	}
	return nil
}

// isAccountInGroup checks if the account belongs to the specified group.
// When groupID is nil, returns true only for ungrouped accounts (no group assignments).
func (s *Generic) isAccountInGroup(account *gatewayprovider.ExecutionAccount, groupID *int64) bool {
	if account == nil {
		return false
	}
	if groupID == nil {
		return len(account.Record.AccountGroups) == 0
	}
	for _, ag := range account.Record.AccountGroups {
		if ag.GroupID == *groupID {
			return true
		}
	}
	return false
}

func (s *Generic) tryAcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int) (*schedulercore.AcquireResult, error) {
	if schedulercore.IsSelectOnly(ctx) {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireAccountSlot(ctx, accountID, maxConcurrency)
}

func (s *Generic) withWindowCostPrefetch(ctx context.Context, accounts []gatewayprovider.ExecutionAccount) context.Context {
	if ctx == nil || len(accounts) == 0 || !s.windowPrefetchAvailable {
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
func (s *Generic) isAccountSchedulableForQuota(account *gatewayprovider.ExecutionAccount) bool {
	if !account.View().IsAPIKeyOrBedrock() {
		return true
	}
	return !account.View().IsQuotaExceeded()
}

// isAccountSchedulableForWindowCost 检查账号是否可根据窗口费用进行调度
// 仅适用于 Anthropic OAuth/SetupToken 账号
// 返回 true 表示可调度，false 表示不可调度
func (s *Generic) isAccountSchedulableForWindowCost(ctx context.Context, account *gatewayprovider.ExecutionAccount, sticky bool) bool {
	return s.windowCostGuard().Allow(ctx, costWindowInput(account), sticky)
}

// withRPMPrefetch 批量预取所有候选账号的 RPM 计数
func (s *Generic) withRPMPrefetch(ctx context.Context, accounts []gatewayprovider.ExecutionAccount) context.Context {
	if s.rpmCache == nil {
		return ctx
	}

	var ids []int64
	for i := range accounts {
		if accounts[i].View().IsAnthropicOAuthOrSetupToken() && gatewayprovider.ExecutionRuntimeConfig(&accounts[i]).GetBaseRPM() > 0 {
			ids = append(ids, accounts[i].Record.ID)
		}
	}
	return schedulercore.PrefetchRPM(ctx, s.rpmCache, ids)
}

// isAccountSchedulableForRPM 检查账号是否可根据 RPM 进行调度
// 仅适用于 Anthropic OAuth/SetupToken 账号
func (s *Generic) isAccountSchedulableForRPM(ctx context.Context, account *gatewayprovider.ExecutionAccount, sticky bool) bool {
	if !account.View().IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return schedulercore.AllowAccountRPM(ctx, s.rpmCache, schedulercore.AccountRPMInput{ID: account.Record.ID, Enabled: true, Base: gatewayprovider.ExecutionRuntimeConfig(account).GetBaseRPM(), Buffer: gatewayprovider.ExecutionRuntimeConfig(account).GetRPMStickyBuffer(), Strategy: gatewayprovider.ExecutionRuntimeConfig(account).GetRPMStrategy()}, sticky)
}

// IncrementAccountRPM increments the RPM counter for the given account.
// 已知 TOCTOU 竞态：调度时读取 RPM 计数与此处递增之间存在时间窗口，
// 高并发下可能短暂超出 RPM 限制。这是与 WindowCost 一致的 soft-limit
// 设计权衡——可接受的少量超额优于加锁带来的延迟和复杂度。
func (s *Generic) IncrementAccountRPM(ctx context.Context, id int64) error {
	return schedulercore.IncrementAccountRPM(ctx, s.rpmCache, id)
}

// checkAndRegisterSession 检查并注册会话，用于会话数量限制
// 仅适用于 Anthropic OAuth/SetupToken 账号
// sessionID: 会话标识符（使用粘性会话的 hash）
// 返回 true 表示允许（在限制内或会话已存在），false 表示拒绝（超出限制且是新会话）
func (s *Generic) checkAndRegisterSession(ctx context.Context, account *gatewayprovider.ExecutionAccount, session string) bool {
	if schedulercore.IsSelectOnly(ctx) {
		return true
	}
	return schedulercore.RegisterSession(ctx, s.sessionLimitCache, schedulerSessionBinding(account, session))
}

// ReleaseAccountSession 立即释放会话槽（不等待空闲超时）
// 供 handler 在请求最终失败（选号成功但转发失败/客户端中断）时调用：
// 上游从未真正服务该会话，若继续占槽，max_sessions 受限的账号会被失败请求的
// session hash 卡满整个空闲窗口，后续新会话全部被拒。
// 适用条件与 checkAndRegisterSession 对齐；不适用账号为 no-op，幂等可安全重复调用。
func (s *Generic) ReleaseAccountSession(ctx context.Context, account *gatewayprovider.ExecutionAccount, session string) {
	if s == nil {
		return
	}
	schedulercore.FinishSession(ctx, s.sessionLimitCache, schedulerSessionBinding(account, session), schedulercore.AttemptOutcome{}, schedulercore.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
}

func schedulerSessionBinding(account *gatewayprovider.ExecutionAccount, session string) schedulercore.SessionBinding {
	if account == nil {
		return schedulercore.SessionBinding{}
	}
	return schedulercore.SessionBinding{AccountID: account.Record.ID, SessionID: session, Enabled: account.View().IsAnthropicOAuthOrSetupToken(), Limit: gatewayprovider.ExecutionRuntimeConfig(account).GetMaxSessions(), IdleTimeout: time.Duration(gatewayprovider.ExecutionRuntimeConfig(account).GetSessionIdleTimeoutMinutes()) * time.Minute}
}

func (s *Generic) getSchedulableAccount(ctx context.Context, accountID int64) (*gatewayprovider.ExecutionAccount, error) {
	var (
		account *gatewayprovider.ExecutionAccount
		err     error
	)
	if s.schedulerSnapshot != nil {
		account, err = readSnapshotAccount(ctx, s.schedulerSnapshot, accountID)
	} else {
		account, err = s.accountRepo.GetByID(ctx, accountID)
	}
	if err != nil || account == nil {
		return account, err
	}
	if s.isAccountBlockedBySchedulingThreshold(ctx, account) {
		return nil, nil
	}

	if account.View().IsGrok() {
		if gated := s.filterGrokFreeQuotaAccountsForGateway(ctx, []gatewayprovider.ExecutionAccount{*account}); len(gated) == 0 {
			return nil, nil
		}
	}
	return account, nil
}

func (s *Generic) filterAccountsBySchedulingThreshold(ctx context.Context, accounts []gatewayprovider.ExecutionAccount) []gatewayprovider.ExecutionAccount {
	if len(accounts) == 0 {
		return accounts
	}

	filtered := make([]gatewayprovider.ExecutionAccount, 0, len(accounts))
	for i := range accounts {
		if s.isAccountBlockedBySchedulingThreshold(ctx, &accounts[i]) {
			continue
		}
		filtered = append(filtered, accounts[i])
	}
	return filtered
}

func (s *Generic) isAccountBlockedBySchedulingThreshold(ctx context.Context, account *gatewayprovider.ExecutionAccount) bool {
	if s == nil || s.healthObserver == nil || account == nil {
		return false
	}
	return gatewayprovider.ApplyExecutionSchedulingThreshold(ctx, s.healthObserver, account)
}

func (s *Generic) hydrateSelectedAccount(ctx context.Context, account *gatewayprovider.ExecutionAccount) (*gatewayprovider.ExecutionAccount, error) {
	if account == nil || s.schedulerSnapshot == nil {
		return account, nil
	}
	hydrated, err := readSnapshotAccount(ctx, s.schedulerSnapshot, account.Record.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected gateway account %d not found during hydration", account.Record.ID)
	}
	return hydrated, nil
}

func (s *Generic) newSelectionResult(ctx context.Context, account *gatewayprovider.ExecutionAccount, acquired bool, release func(), waitPlan *schedulercore.AccountWaitPlan) (*gatewayprovider.SelectionResult, error) {
	attempt := schedulercore.NewAttemptLease(schedulercore.RequestLease(ctx), nil, release)
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
	selection := &gatewayprovider.SelectionResult{
		Account:     hydrated,
		Acquired:    acquired,
		ReleaseFunc: release,
		WaitPlan:    waitPlan,
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.UsesAdvancedScheduler() {

		selection.AdvancedScheduler = true
		feedback := s.advancedSchedulerEffectiveSettingsForRequest(ctx, &group.ID).Feedback
		selection.AdvancedSchedulerFeedback = &feedback
	}
	return selection, nil
}

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

func (s *Generic) logDetailedSelectionFailure(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	platform string,
	accounts []gatewayprovider.ExecutionAccount,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureStats {
	stats := s.collectSelectionFailureStats(ctx, accounts, requestedModel, platform, excludedIDs, allowMixedScheduling)
	logging.LegacyPrintf(
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

func (s *Generic) collectSelectionFailureStats(
	ctx context.Context,
	accounts []gatewayprovider.ExecutionAccount,
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
			stats.SamplePlatformIDs = appendSelectionFailureSampleID(stats.SamplePlatformIDs, acc.Record.ID)
		case "model_unsupported":
			stats.ModelUnsupported++
			stats.SampleMappingIDs = appendSelectionFailureSampleID(stats.SampleMappingIDs, acc.Record.ID)
		case "model_rate_limited":
			stats.ModelRateLimited++
			routingModel := s.groupMappedModelForAccountLayer(ctx, requestedModel)
			remaining := gatewayprovider.ExecutionModelPolicy(acc).LimitRemaining(ctx, routingModel).Truncate(time.Second)
			stats.SampleRateLimitIDs = appendSelectionFailureRateSample(stats.SampleRateLimitIDs, acc.Record.ID, remaining)
		default:
			stats.Eligible++
		}
	}

	return stats
}

func (s *Generic) diagnoseSelectionFailure(
	ctx context.Context,
	acc *gatewayprovider.ExecutionAccount,
	requestedModel string,
	platform string,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureDiagnosis {
	if acc == nil {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "account_nil"}
	}
	if _, excluded := excludedIDs[acc.Record.ID]; excluded {
		return selectionFailureDiagnosis{Category: "excluded"}
	}
	if !s.isAccountSchedulableForSelection(acc) {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "generic_unschedulable"}
	}
	if isPlatformFilteredForSelection(acc, platform, allowMixedScheduling) {
		return selectionFailureDiagnosis{
			Category: "platform_filtered",
			Detail:   fmt.Sprintf("account_platform=%s requested_platform=%s", acc.Record.Platform, strings.TrimSpace(platform)),
		}
	}
	if requestedModel != "" && !s.isModelSupportedByAccountWithContext(ctx, acc, requestedModel) {
		return selectionFailureDiagnosis{
			Category: "model_unsupported",
			Detail:   fmt.Sprintf("model=%s", requestedModel),
		}
	}
	if !s.isAccountSchedulableForModelSelection(ctx, acc, requestedModel) {
		routingModel := s.groupMappedModelForAccountLayer(ctx, requestedModel)
		remaining := gatewayprovider.ExecutionModelPolicy(acc).LimitRemaining(ctx, routingModel).Truncate(time.Second)
		return selectionFailureDiagnosis{
			Category: "model_rate_limited",
			Detail:   fmt.Sprintf("remaining=%s", remaining),
		}
	}
	return selectionFailureDiagnosis{Category: "eligible"}
}

func isPlatformFilteredForSelection(acc *gatewayprovider.ExecutionAccount, platform string, allowMixedScheduling bool) bool {
	if acc == nil {
		return true
	}
	if allowMixedScheduling {
		if acc.Record.Platform == capability.PlatformAntigravity {
			return !acc.View().IsMixedSchedulingEnabled()
		}
		return acc.Record.Platform != platform
	}
	if strings.TrimSpace(platform) == "" {
		return false
	}
	return acc.Record.Platform != platform
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

// isModelSupportedByAccountWithContext 根据分组映射后的模型检查账号支持能力。
func (s *Generic) isModelSupportedByAccountWithContext(ctx context.Context, account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	routingModel := s.groupMappedModelForAccountLayer(ctx, requestedModel)
	return s.isRoutingModelSupportedByAccountWithContext(ctx, account, routingModel)
}

// isRoutingModelSupportedByAccountWithContext 检查已经过分组映射的模型，避免重复执行分组映射。
func (s *Generic) isRoutingModelSupportedByAccountWithContext(ctx context.Context, account *gatewayprovider.ExecutionAccount, routingModel string) bool {
	return gatewayprovider.ExecutionModelPolicy(account).Supports(ctx, routingModel)
}

func (s *Generic) groupMappedModelForAccountLayer(ctx context.Context, requestedModel string) string {
	if s == nil || s.groupPolicies == nil || strings.TrimSpace(requestedModel) == "" {
		return requestedModel
	}
	group, ok := requeststate.GroupFromContext(ctx)
	if !ok || !routing.IsGroupContextValid(group) {
		return requestedModel
	}
	groupID := group.ID
	return s.groupMappedModelForGroup(ctx, &groupID, requestedModel)
}

// isModelSupportedByAccount 根据账户平台检查模型支持（无 context，用于非 Antigravity 平台）
func (s *Generic) isModelSupportedByAccount(account *gatewayprovider.ExecutionAccount, requestedModel string) bool {
	return gatewayprovider.ExecutionModelPolicy(account).Supports(context.Background(), requestedModel)
}

// NewSessionAttempts 为一次请求提供唯一会话完成集合，不保存全局副本。
func (s *Generic) NewSessionAttempts() *schedulercore.SessionAttempts {
	return schedulercore.NewSessionAttempts(s.sessionLimitCache, schedulercore.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
}

// TrackSessionAttempt 只投影原账号会话参数，最终状态由执行入口传入。
func (s *Generic) TrackSessionAttempt(attempts *schedulercore.SessionAttempts, account *gatewayprovider.ExecutionAccount, session string) {
	attempts.Track(schedulerSessionBinding(account, session))
}
