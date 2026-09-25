package selection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"

	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

const openAIAccountScheduleLayerLoadBalance = "load_balance"
const openAIAccountScheduleLayerGuardianParent = "guardian_parent"

type openAIAccountSchedulerMetrics struct {
	schedulercore.PlatformMetrics
}

func (m *openAIAccountSchedulerMetrics) recordSelect(v schedulercore.PlatformDecision) {
	if m != nil {
		m.RecordSelect(v)
	}
}
func (m *openAIAccountSchedulerMetrics) recordSwitch() {
	if m != nil {
		m.RecordSwitch()
	}
}

type compatiblePicker struct {
	service       *Compatible
	metrics       openAIAccountSchedulerMetrics
	stats         *schedulercore.RuntimeStats
	freeQuotaGate atomic.Pointer[accountcore.FreeQuotaGate]
}

func newDefaultOpenAIAccountScheduler(service *Compatible, stats *schedulercore.RuntimeStats) pickerEngine {
	if stats == nil {
		stats = schedulercore.NewRuntimeStats(time.Now)
	}
	return &compatiblePicker{
		service: service,
		stats:   stats,
	}
}

func (s *compatiblePicker) Select(ctx context.Context, req schedulercore.PlatformSelectionInput) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	core, scope := s.platformSelector()
	v, decision, err := core.Select(ctx, cloneSelectionInput(req))
	return scope.restore(v), decision, err
}

// hasOpenAIAccountGroupMetadata 判断账号是否声明了分组归属信息。
func hasOpenAIAccountGroupMetadata(account *gatewayprovider.ExecutionAccount) bool {
	return account != nil && (len(account.Record.GroupIDs) > 0 || len(account.Record.AccountGroups) > 0)
}

// openAIStickyAccountMatchesGroup 校验粘性会话账号是否仍属于当前请求分组。
func openAIStickyAccountMatchesGroup(account *gatewayprovider.ExecutionAccount, groupID *int64) bool {
	if account == nil {
		return false
	}
	if groupID == nil {
		return len(account.Record.AccountGroups) == 0 && len(account.Record.GroupIDs) == 0
	}
	for _, accountGroupID := range account.Record.GroupIDs {
		if accountGroupID == *groupID {
			return true
		}
	}
	for _, accountGroup := range account.Record.AccountGroups {
		if accountGroup.GroupID == *groupID {
			return true
		}
	}
	return false
}

func shouldEscapeAdvancedStickyAccount(stats *schedulercore.RuntimeStats, id int64, cfg policy.StickyEscapeConfig) (string, float64, float64, bool) {
	return schedulercore.ShouldEscapeSticky(stats, id, policy.StickyEscapeConfig{Enabled: cfg.Enabled, TtftMs: cfg.TtftMs, ErrorRate: cfg.ErrorRate})
}

func (s *compatiblePicker) shouldEscapeStickyAccount(accountID int64, cfg policy.StickyEscapeConfig) (reason string, errorRate float64, ttft float64, shouldEscape bool) {
	if s == nil {
		return "", 0, 0, false
	}
	return shouldEscapeAdvancedStickyAccount(s.stats, accountID, cfg)
}

func (s *compatiblePicker) isAccountTransportCompatible(account *gatewayprovider.ExecutionAccount, requiredTransport egress.OpenAIUpstreamTransport) bool {
	if requiredTransport == egress.OpenAIUpstreamTransportAny || requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || s.service == nil {
		return false
	}
	return s.service.isOpenAIAccountTransportCompatible(account, requiredTransport)
}

func (s *compatiblePicker) lookupShadowParentAccount(ctx context.Context, id int64) *gatewayprovider.ExecutionAccount {
	if s == nil || s.service == nil {
		return nil
	}
	if s.service.schedulerSnapshot != nil {
		if account, err := readSnapshotAccount(ctx, s.service.schedulerSnapshot, id); err == nil && account != nil {
			return account
		}
	}
	if s.service.accountRepo == nil {
		return nil
	}
	account, _ := s.service.accountRepo.GetByID(ctx, id)
	return account
}

func (s *compatiblePicker) isAccountRequestCompatible(ctx context.Context, account *gatewayprovider.ExecutionAccount, req schedulercore.PlatformSelectionInput) bool {
	compatible, _ := s.isAccountRequestCompatibleReason(ctx, account, req)
	return compatible
}

// isAccountRequestCompatibleReason 返回账号是否兼容，并在拒绝时标明具体门禁原因。
func (s *compatiblePicker) isAccountRequestCompatibleReason(ctx context.Context, account *gatewayprovider.ExecutionAccount, req schedulercore.PlatformSelectionInput) (bool, string) {
	if s != nil && s.service != nil && !s.service.shadowProtocolsAllowed(ctx, account) {
		return false, "parent_protocol_unavailable"
	}
	if !gatewayprovider.ExecutionModelPolicy(account).AllowsProtocol(ctx) {
		return false, "protocol_unavailable"
	}
	if account == nil {
		return false, "account_nil"
	}
	if req.RequirePrivacySet && !account.View().IsPrivacySet() {
		return false, "privacy_not_set"
	}
	if s != nil && s.service != nil && s.service.isOpenAIAccountRequestRuntimeBlocked(account, requestRoutingModel(req)) {
		return false, "runtime_blocked"
	}
	if s != nil && s.service != nil && s.service.isOpenAIProxyStreamQuarantined(ctx, account) {
		return false, "proxy_stream_quarantined"
	}

	if paused, decision := gatewayprovider.OpenAIQuotaPause(ctx, account); paused {
		reason := "quota_auto_pause"
		if decision.Window != "" {
			reason += "_" + decision.Window
		}
		return false, reason
	}

	if !accountcore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(account), func(id int64) *accountcore.Record {
		return gatewayprovider.ExecutionRecord(s.lookupShadowParentAccount(ctx, id))
	}) {
		return false, "shadow_parent_unhealthy"
	}
	if !gatewayprovider.ExecutionModelPolicy(account).SupportsCompatibleRouting(ctx, requestRoutingModel(req)) {
		return false, "model_not_supported"
	}
	if req.GroupID != nil && s != nil && s.service != nil &&
		s.service.NeedsUpstreamChannelRestriction(ctx, req.GroupID) &&
		s.service.UpstreamRoutingModelRestricted(ctx, *req.GroupID, account, requestRoutingModel(req), req.RequireCompact) {
		return false, "channel_upstream_restricted"
	}
	if !accountSupportsOpenAICapabilities(ctx, account, req.RequiredCapability, req.RequiredImageCapability) {
		return false, "capability_mismatch"
	}
	return true, ""
}

func (s *compatiblePicker) ReportResult(accountID int64, success bool, firstTokenMs *int, feedback ...policy.FeedbackConfig) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.Report(accountID, success, firstTokenMs, feedback...)
}

func (s *compatiblePicker) ReportSwitch() {
	if s == nil {
		return
	}
	s.metrics.recordSwitch()
}

func (s *compatiblePicker) SnapshotMetrics() schedulercore.PlatformMetricsSnapshot {
	if s == nil {
		return schedulercore.PlatformMetricsSnapshot{}
	}
	return s.metrics.Snapshot(s.stats.Size())
}

// groupUsesAdvancedScheduler 只依据最终解析后的分组决定调度模式。
func (s *Compatible) groupUsesAdvancedScheduler(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || *groupID <= 0 {
		return false
	}
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == *groupID {
		return group.UsesAdvancedScheduler()
	}
	if s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.readSchedulingGroup(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

func (s *Compatible) getOpenAIAccountScheduler(ctx context.Context, groupID *int64) pickerEngine {
	if s == nil {
		return nil
	}
	if !s.groupUsesAdvancedScheduler(ctx, groupID) {
		return nil
	}
	s.pickerOnce.Do(func() {
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = schedulercore.NewRuntimeStats(time.Now)
		}
		if s.picker == nil {
			s.picker = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.picker
}

// ensureOpenAIAccountScheduler 仅供结果统计和诊断初始化调度器实例。
// 实际账号选择仍必须经 getOpenAIAccountScheduler 按分组模式门控。
func (s *Compatible) ensureOpenAIAccountScheduler() pickerEngine {
	if s == nil {
		return nil
	}
	s.pickerOnce.Do(func() {
		if s.openaiAccountStats == nil {
			s.openaiAccountStats = schedulercore.NewRuntimeStats(time.Now)
		}
		if s.picker == nil {
			s.picker = newDefaultOpenAIAccountScheduler(s, s.openaiAccountStats)
		}
	})
	return s.picker
}

func (s *Compatible) SelectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requireCompact bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, "", "", requireCompact, capability.PlatformOpenAI, false)
}

// SelectAccountWithSchedulerForCapability 按能力要求调度账号。
// previousResponseCanMove 表示首包 input 可自行重建工具续链，previous_response_id 允许跨账号迁移
// （粘性加权模式下改为加权偏好而非硬粘连）。
func (s *Compatible) SelectAccountWithSchedulerForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability accountcore.OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	platform := capability.PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	return s.selectAccountWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

// SelectAccountWithSchedulerForCapabilityAndRoutingModel 同时保留客户端模型 R 与已解析的账号层模型。
// 该入口供 /v1/messages 使用，使渠道限制按 R/C 检查，账号能力与账号映射按 D 检查。
func (s *Compatible) SelectAccountWithSchedulerForCapabilityAndRoutingModel(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability accountcore.OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	platform := capability.PlatformOpenAI
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

func (s *Compatible) SelectAccountWithSchedulerForImages(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability accountcore.OpenAIImagesCapability,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	selection, decision, err := s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, egress.OpenAIUpstreamTransportHTTPSSE, "", requiredCapability, false, capability.PlatformOpenAI, false)
	if err == nil && selection != nil && selection.Account != nil {
		return selection, decision, nil
	}

	if requiredCapability == accountcore.OpenAIImagesCapabilityNative {
		return s.selectAccountWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, egress.OpenAIUpstreamTransportHTTPSSE, "", accountcore.OpenAIImagesCapabilityBasic, false, capability.PlatformOpenAI, false)
	}
	return selection, decision, err
}

func (s *Compatible) selectAccountWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability accountcore.OpenAIEndpointCapability,
	requiredImageCapability accountcore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	routingModel := s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	return s.selectAccountWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// selectAccountWithSchedulerForRouting 首次调度仍把隔离代理当作不可用；只有因容量耗尽
// 失败且熔断器确实在隔离代理时，才用同一账号层模型重跑一次并忽略隔离。这样健康代理
// 始终优先，同时避免共享代理场景被熔断器清空全部容量。
func (s *Compatible) selectAccountWithSchedulerForRouting(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability accountcore.OpenAIEndpointCapability,
	requiredImageCapability accountcore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	originalGroupID := derefGroupID(groupID)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, schedulercore.PlatformDecision{}, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if derefGroupID(groupID) != originalGroupID {

		routingModel = s.resolveChannelRoutingModel(ctx, groupID, requestedModel)
	}
	selection, decision, err := s.selectAccountWithSchedulerForRoutingOnce(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
	if err == nil || openAIProxyStreamQuarantineBypassed(ctx) {
		return selection, decision, err
	}
	if !errors.Is(err, schedulercore.ErrNoAvailableAccounts) && !errors.Is(err, schedulercore.ErrNoAvailableCompactAccounts) {
		return selection, decision, err
	}

	if routing.NormalizeOpenAICompatiblePlatform(platform) != capability.PlatformOpenAI {
		return selection, decision, err
	}
	blocked := s.getOpenAIProxyStreamCircuit().ActiveBlockCount(time.Now())
	if blocked == 0 {
		return selection, decision, err
	}
	s.logOpenAIProxyStreamQuarantineFailOpen(requestedModel, blocked)
	return s.selectAccountWithSchedulerForRoutingOnce(withOpenAIProxyStreamQuarantineBypass(ctx), groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// resolveOpenAISchedulerGroup 解析 OpenAI 路径最终实际使用的分组。
// 与通用网关保持一致：Claude Code 专属分组会在非 Claude Code 请求时沿回退链解析，
// 因此高级调度器模式、渠道映射和粘性缓存均绑定到最终目标分组。
func (s *Compatible) resolveOpenAISchedulerGroup(ctx context.Context, groupID *int64) (context.Context, *int64, error) {
	if groupID == nil || *groupID <= 0 {
		return ctx, groupID, nil
	}
	if forcePlatform, ok := apikey.ForcePlatformFromContext(ctx); ok && strings.TrimSpace(forcePlatform) != "" {
		return ctx, groupID, nil
	}

	read := func(ctx context.Context, id int64) (*routing.Group, error) {
		if contextual, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(contextual) && contextual.ID == id {
			return contextual, nil
		}
		if s != nil && s.schedulerSnapshot != nil {
			return s.readSchedulingGroup(ctx, id)
		}
		return nil, nil
	}
	group, resolved, err := routing.ResolveClientGroup(ctx, groupID, read, requeststate.IsClaudeCodeClient, routing.ClientGroupPolicy{KeepMissingSnapshot: true, RejectNonPositiveFallback: true})
	if err != nil {
		return ctx, nil, err
	}
	if group != nil {
		ctx = requeststate.WithGroup(ctx, group)
	}
	return ctx, resolved, nil
}

// withOpenAIGroupPrivacyRequirement 在一次调度请求内缓存分组隐私资格，避免重试重复查询。
func (s *Compatible) withOpenAIGroupPrivacyRequirement(ctx context.Context, groupID *int64) context.Context {
	// 保留原入口对有效 context 的要求。
	if ctx == nil {
		panic("cannot create context from nil parent")
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	hints.GroupPrivacyGroupID = derefGroupID(groupID)
	hints.GroupPrivacyRequirement = requeststate.Hint[bool]{Set: true, Value: s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID)}
	return requeststate.WithExecutionHints(ctx, hints)
}

func (s *Compatible) openAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	// 保留原入口对有效 context 的要求。
	if ctx == nil {
		panic("nil context")
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	if hints.GroupPrivacyRequirement.Set && hints.GroupPrivacyGroupID == derefGroupID(groupID) {
		return hints.GroupPrivacyRequirement.Value
	}
	return s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID)
}

func (s *Compatible) loadOpenAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.readSchedulingGroup(ctx, *groupID)
	if err != nil {

		return true
	}
	return group != nil && group.RequirePrivacySet
}

func (s *Compatible) selectAccountWithSchedulerForRoutingOnce(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability accountcore.OpenAIEndpointCapability,
	requiredImageCapability accountcore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	platform = routing.NormalizeOpenAICompatiblePlatform(platform)
	decision := schedulercore.PlatformDecision{}
	preserveGuardianParentBinding := requeststate.PreserveGuardianParentBinding(ctx, sessionHash)
	guardianParentAccountID := int64(0)
	if strings.TrimSpace(previousResponseID) == "" {
		guardianParentAccountID = s.resolveOpenAIGuardianParentAccountID(ctx, groupID)
	}
	scheduler := s.getOpenAIAccountScheduler(ctx, groupID)
	if scheduler == nil {
		decision.Layer = openAIAccountScheduleLayerLoadBalance
		if guardianParentAccountID > 0 {
			fallbackScheduler := &compatiblePicker{service: s, stats: schedulercore.NewRuntimeStats(time.Now)}
			selection, _, err := fallbackScheduler.selectBySessionHash(ctx, schedulercore.PlatformSelectionInput{
				GroupID:                 groupID,
				Platform:                platform,
				SessionHash:             sessionHash,
				StickyAccountID:         guardianParentAccountID,
				PreserveStickyBinding:   true,
				RequestedModel:          requestedModel,
				RoutingModel:            routingModel,
				RequiredTransport:       string(requiredTransport),
				RequiredCapability:      requiredCapability,
				RequiredImageCapability: requiredImageCapability,
				RequireCompact:          requireCompact,
				RequirePrivacySet:       s.openAIGroupRequiresPrivacySet(ctx, groupID),
				ExcludedIDs:             excludedIDs,
			})
			if err != nil {
				return nil, decision, err
			}
			if selection != nil && selection.Account != nil {
				decision.Layer = openAIAccountScheduleLayerGuardianParent
				decision.StickySessionHit = true
				decision.SelectedAccountID = selection.Account.Record.ID
				decision.SelectedAccountType = selection.Account.Record.Type
				return selection, decision, nil
			}
		}
		legacySessionHash := sessionHash
		if preserveGuardianParentBinding {
			legacySessionHash = ""
		}
		if requiredTransport == egress.OpenAIUpstreamTransportAny || requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE {
			effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
			for {
				selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, legacySessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
				if err != nil {
					return nil, decision, err
				}
				if selection == nil || selection.Account == nil {
					return selection, decision, nil
				}
				if accountSupportsOpenAICapabilities(ctx, selection.Account, requiredCapability, requiredImageCapability) {
					return selection, decision, nil
				}
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
				if effectiveExcludedIDs == nil {
					effectiveExcludedIDs = make(map[int64]struct{})
				}
				if _, exists := effectiveExcludedIDs[selection.Account.Record.ID]; exists {
					return nil, decision, schedulercore.ErrNoAvailableAccounts
				}
				effectiveExcludedIDs[selection.Account.Record.ID] = struct{}{}
			}
		}

		effectiveExcludedIDs := cloneExcludedAccountIDs(excludedIDs)
		for {
			selection, err := s.selectAccountWithLoadAwarenessForRouting(ctx, groupID, platform, legacySessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
			if err != nil {
				return nil, decision, err
			}
			if selection == nil || selection.Account == nil {
				return selection, decision, nil
			}
			if s.isOpenAIAccountTransportCompatible(selection.Account, requiredTransport) &&
				accountSupportsOpenAICapabilities(ctx, selection.Account, requiredCapability, requiredImageCapability) {
				return selection, decision, nil
			}
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if effectiveExcludedIDs == nil {
				effectiveExcludedIDs = make(map[int64]struct{})
			}
			if _, exists := effectiveExcludedIDs[selection.Account.Record.ID]; exists {
				return nil, decision, schedulercore.ErrNoAvailableAccounts
			}
			effectiveExcludedIDs[selection.Account.Record.ID] = struct{}{}
		}
	}

	if s.CheckChannelPricingRestriction(ctx, groupID, requestedModel) {
		slog.Warn("channel pricing restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, decision, fmt.Errorf("%w supporting model: %s (channel pricing restriction)", schedulercore.ErrNoAvailableAccounts, requestedModel)
	}

	var stickyAccountID int64
	if sessionHash != "" && s.cache != nil {
		if accountID, err := s.getStickySessionAccountID(ctx, groupID, sessionHash); err == nil && accountID > 0 {
			stickyAccountID = accountID
		}
	}
	effectiveSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
	stickyWeighted := effectiveSettings.StickyWeightedEnabled
	subscriptionPriority := effectiveSettings.SubscriptionPriorityEnabled
	stickyPreviousAccountID := int64(0)
	if stickyWeighted && previousResponseCanMove && strings.TrimSpace(previousResponseID) != "" && platform == capability.PlatformOpenAI {
		stickyPreviousAccountID = s.ResolveAccountIDByPreviousResponseIDForScheduler(ctx, groupID, previousResponseID, routingModel, excludedIDs, requiredCapability, requireCompact)
	}

	selection, decision, selectErr := scheduler.Select(ctx, schedulercore.PlatformSelectionInput{
		GroupID:                 groupID,
		Platform:                platform,
		SessionHash:             sessionHash,
		StickyAccountID:         stickyAccountID,
		GuardianParentAccountID: guardianParentAccountID,
		StickyPreviousAccountID: stickyPreviousAccountID,
		StickyWeighted:          stickyWeighted,
		SubscriptionPriority:    subscriptionPriority,
		PreserveStickyBinding:   preserveGuardianParentBinding,
		RequirePrivacySet:       s.openAIGroupRequiresPrivacySet(ctx, groupID),
		PreviousResponseID:      previousResponseID,
		PreviousResponseCanMove: previousResponseCanMove,
		RequestedModel:          requestedModel,
		RoutingModel:            routingModel,
		RequiredTransport:       string(requiredTransport),
		RequiredCapability:      requiredCapability,
		RequiredImageCapability: requiredImageCapability,
		RequireCompact:          requireCompact,
		ExcludedIDs:             excludedIDs,
	})
	if selection != nil {

		selection.AdvancedScheduler = true
		feedback := effectiveSettings.Feedback
		selection.AdvancedSchedulerFeedback = &feedback
	}
	return selection, decision, selectErr
}

func accountSupportsOpenAICapabilities(ctx context.Context, account *gatewayprovider.ExecutionAccount, requiredCapability accountcore.OpenAIEndpointCapability, requiredImageCapability accountcore.OpenAIImagesCapability) bool {
	if account == nil {
		return false
	}
	return gatewayprovider.SupportsRequestCapability(ctx, account, requiredCapability) &&
		account.View().SupportsOpenAIImageCapability(requiredImageCapability)
}

func cloneExcludedAccountIDs(excludedIDs map[int64]struct{}) map[int64]struct{} {
	if len(excludedIDs) == 0 {
		return nil
	}
	cloned := make(map[int64]struct{}, len(excludedIDs))
	for id := range excludedIDs {
		cloned[id] = struct{}{}
	}
	return cloned
}

func (s *Compatible) isOpenAIAccountTransportCompatible(account *gatewayprovider.ExecutionAccount, requiredTransport egress.OpenAIUpstreamTransport) bool {
	if requiredTransport == egress.OpenAIUpstreamTransportAny || requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || account == nil {
		return false
	}
	if requiredTransport == egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress {

		if account.View().IsGrok() {
			return true
		}
		if s.options.WS == nil || !s.options.WS.ModeRouterV2Enabled {
			return s.ResolveTransport(account).Transport == egress.OpenAIUpstreamTransportResponsesWebsocketV2
		}
		switch account.View().ResolveOpenAIResponsesWebSocketV2Mode(s.options.WSIngressMode) {
		case accountcore.OpenAIWSIngressModeCtxPool, accountcore.OpenAIWSIngressModePassthrough, accountcore.OpenAIWSIngressModeHTTPBridge, accountcore.OpenAIWSIngressModeShared, accountcore.OpenAIWSIngressModeDedicated:
			return true
		default:
			return false
		}
	}
	return s.ResolveTransport(account).Transport == requiredTransport
}

func (s *Compatible) ReportOpenAIAccountScheduleResult(accountOrID any, model string, success bool, firstTokenMs *int, observedErr ...error) bool {
	var account *gatewayprovider.ExecutionAccount
	var accountID int64
	switch value := accountOrID.(type) {
	case *gatewayprovider.ExecutionAccount:
		account = value
		if account != nil {
			accountID = account.Record.ID
		}
	case int64:
		accountID = value
	case int:
		accountID = int64(value)
	}
	if account == nil && accountID == 0 {
		return false
	}

	healthTripped := false
	if account != nil && s != nil && s.healthObserver != nil {

		if !success && len(observedErr) > 0 && observedErr[0] != nil {
			healthTripped = s.ObserveOpenAIAccountHealthFailure(context.Background(), account, observedErr[0])
		}
	}
	if success {
		s.runtimeBlockState().ResetRetry(accountID)
		s.clearOpenAIAccountModelTransientState(accountID, accountcore.NormalizeTransientModel(model))
	}
	if account == nil {

		s.reportOpenAIAccountScheduleResult(false, accountID, model, success, firstTokenMs)
		return healthTripped
	}
	if s == nil || s.healthObserver == nil {
		return healthTripped
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return healthTripped
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
	return healthTripped
}

// ObserveOpenAIAccountHealthFailure 记录已经写出响应后无法进入调度反馈的失败。
func (s *Compatible) ObserveOpenAIAccountHealthFailure(ctx context.Context, account *gatewayprovider.ExecutionAccount, observedErr error) bool {
	if s == nil || s.healthObserver == nil || account == nil || observedErr == nil {
		return false
	}
	status, body, eligible := gatewayprovider.ClassifyOpenAIAPIKeyHealthFailure(observedErr)
	record := gatewayprovider.ExecutionRecord(account)
	handled := s.healthObserver.Core.ApplyAPIKeyHealthFailure(ctx, record, status, body, eligible)
	account.Record.TempUnschedulableUntil = record.TempUnschedulableUntil
	account.Record.TempUnschedulableReason = record.TempUnschedulableReason
	return handled
}

// ReportOpenAIAccountScheduleResultForSelection 按本次实际选择模式写入反馈。
// 基础分组仍清理请求临时状态，但不会污染高级调度统计。
func (s *Compatible) ReportOpenAIAccountScheduleResultForSelection(selection *gatewayprovider.SelectionResult, accountID int64, model string, success bool, firstTokenMs *int) {
	if selection != nil && selection.AdvancedScheduler && selection.AdvancedSchedulerFeedback != nil {
		s.reportOpenAIAccountScheduleResultWithFeedback(accountID, model, success, firstTokenMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.reportOpenAIAccountScheduleResult(selection != nil && selection.AdvancedScheduler, accountID, model, success, firstTokenMs)
}

func (s *Compatible) reportOpenAIAccountScheduleResult(advanced bool, accountID int64, model string, success bool, firstTokenMs *int) {
	if success {
		s.clearOpenAIAccountModelTransientState(accountID, accountcore.NormalizeTransientModel(model))
	}
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs)
}

func (s *Compatible) reportOpenAIAccountScheduleResultWithFeedback(accountID int64, model string, success bool, firstTokenMs *int, feedback policy.FeedbackConfig) {
	if success {
		s.clearOpenAIAccountModelTransientState(accountID, accountcore.NormalizeTransientModel(model))
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(accountID, success, firstTokenMs, feedback)
}

func (s *Compatible) RecordOpenAIAccountSwitch() {

	if s == nil || s.healthObserver == nil {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler != nil {
		scheduler.ReportSwitch()
	}
}

// RecordOpenAIAccountSwitchForSelection 只记录高级调度请求的账号切换。
func (s *Compatible) RecordOpenAIAccountSwitchForSelection(selection *gatewayprovider.SelectionResult) {
	s.recordOpenAIAccountSwitch(selection != nil && selection.AdvancedScheduler)
}

func (s *Compatible) recordOpenAIAccountSwitch(advanced bool) {
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportSwitch()
}

func (s *Compatible) SnapshotOpenAIAccountSchedulerMetrics() schedulercore.PlatformMetricsSnapshot {
	scheduler := s.ensureOpenAIAccountScheduler()
	if scheduler == nil {
		return schedulercore.PlatformMetricsSnapshot{}
	}
	return scheduler.SnapshotMetrics()
}

func (s *Compatible) SessionStickyTTL() time.Duration {
	if s != nil &&
		s.options.StickyTTL >
			0 {
		return s.options.StickyTTL
	}
	return time.Hour
}

// 基础回退只委托同一粘性核心，不维护第二条绑定或等待策略。
func (s *compatiblePicker) selectBySessionHash(ctx context.Context, req schedulercore.PlatformSelectionInput) (*gatewayprovider.SelectionResult, bool, error) {
	core, scope := s.platformSelector()
	value, escaped, err := core.SelectBySessionHash(ctx, cloneSelectionInput(req))
	return scope.restore(value), escaped, err
}

// 旧候选只转换额度观测；评分信号使用账号模块唯一实现。
func openAIQuotaHeadroomFactor(value *gatewayprovider.ExecutionAccount, now time.Time) float64 {
	return accountcore.OpenAIQuotaHeadroomFactor(gatewayprovider.ExecutionRecord(value), now)
}
