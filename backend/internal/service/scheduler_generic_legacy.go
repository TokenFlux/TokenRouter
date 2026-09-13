// 旧网关只为 scheduler 提供当前调用的投影及平台端口，不保存第二份选择或缓存状态。
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// genericSelectionScope 用临时关联号保留同 ID 的多份读取快照，选择结束后即释放。
type genericSelectionScope struct {
	next     uint64
	accounts map[uint64]*Account
	groups   map[uint64]*Group
}

func (g *genericSelectionScope) account(value *Account) *scheduler.FlowAccount {
	if value == nil {
		return nil
	}
	g.next++
	id := g.next
	g.accounts[id] = value
	return &scheduler.FlowAccount{Plan: value.resolvedCandidate, ProjectionID: id, ID: value.ID, Name: value.Name, Platform: value.Platform, Type: value.Type, Concurrency: value.Concurrency, Priority: value.Priority, LastUsedAt: cloneFlowTime(value.LastUsedAt), SessionWindowEnd: cloneFlowTime(value.SessionWindowEnd), LoadFactor: value.EffectiveLoadFactor(), BaseRPM: value.GetBaseRPM(), PrivacySet: value.IsPrivacySet(), MixedScheduling: value.IsMixedSchedulingEnabled()}
}
func (g *genericSelectionScope) group(value *Group) *scheduler.FlowGroup {
	if value == nil {
		return nil
	}
	g.next++
	id := g.next
	g.groups[id] = value
	return &scheduler.FlowGroup{ProjectionID: id, Group: *RoutingGroupView(value)}
}
func (g *genericSelectionScope) oldAccount(v *scheduler.FlowAccount) *Account {
	if v == nil {
		return nil
	}
	return g.accounts[v.ProjectionID]
}
func (g *genericSelectionScope) oldGroup(v *scheduler.FlowGroup) *Group {
	if v == nil {
		return nil
	}
	return g.groups[v.ProjectionID]
}
func (g *genericSelectionScope) values(values []Account) []scheduler.FlowAccount {
	if values == nil {
		return nil
	}
	out := make([]scheduler.FlowAccount, len(values))
	for i := range values {
		out[i] = *g.account(&values[i])
	}
	return out
}
func (g *genericSelectionScope) oldValues(values []scheduler.FlowAccount) []Account {
	if values == nil {
		return nil
	}
	out := make([]Account, len(values))
	for i := range values {
		out[i] = *g.oldAccount(&values[i])
	}
	return out
}
func (g *genericSelectionScope) pointers(values []*Account) []*scheduler.FlowAccount {
	if values == nil {
		return nil
	}
	out := make([]*scheduler.FlowAccount, len(values))
	for i, a := range values {
		out[i] = g.account(a)
	}
	return out
}
func (g *genericSelectionScope) loads(values []accountWithLoad) []scheduler.FlowLoad {
	if values == nil {
		return nil
	}
	out := make([]scheduler.FlowLoad, len(values))
	for i, a := range values {
		out[i] = scheduler.FlowLoad{Account: g.account(a.account), LoadInfo: a.loadInfo}
	}
	return out
}
func (g *genericSelectionScope) selection(value *AccountSelectionResult) *scheduler.FlowSelection {
	if value == nil {
		return nil
	}
	out := &scheduler.FlowSelection{Account: g.account(value.Account), Acquired: value.Acquired, ReleaseFunc: value.ReleaseFunc, WaitPlan: value.WaitPlan, AdvancedScheduler: value.AdvancedScheduler}
	if v := value.AdvancedSchedulerFeedback; v != nil {
		out.AdvancedSchedulerFeedback = &policy.FeedbackConfig{ErrorRateAlpha: v.errorRateAlpha, TtftAlpha: v.ttftAlpha}
	}
	return out
}
func (g *genericSelectionScope) restore(value *scheduler.FlowSelection) *AccountSelectionResult {
	if value == nil {
		return nil
	}
	out := &AccountSelectionResult{Account: g.oldAccount(value.Account), Acquired: value.Acquired, ReleaseFunc: value.ReleaseFunc, WaitPlan: value.WaitPlan, AdvancedScheduler: value.AdvancedScheduler}
	if v := value.AdvancedSchedulerFeedback; v != nil {
		out.AdvancedSchedulerFeedback = &advancedSchedulerFeedbackConfig{errorRateAlpha: v.ErrorRateAlpha, ttftAlpha: v.TtftAlpha}
	}
	return out
}
func schedulerEffectiveProjection(v advancedSchedulerEffectiveSettings) policy.EffectiveSettings {
	return policy.EffectiveSettings{StickyWeightedEnabled: v.stickyWeightedEnabled, SubscriptionPriorityEnabled: v.subscriptionPriorityEnabled, TopK: v.topK, Weights: policy.ScoreWeights(v.weights), Feedback: policy.FeedbackConfig{ErrorRateAlpha: v.feedback.errorRateAlpha, TtftAlpha: v.feedback.ttftAlpha}, StickyEscape: policy.StickyEscapeConfig{Enabled: v.stickyEscape.enabled, TtftMs: v.stickyEscape.ttftMs, ErrorRate: v.stickyEscape.errorRate}}
}
func (s *GatewayService) genericSelector() (*scheduler.GenericSelector, *genericSelectionScope) {
	scope := &genericSelectionScope{accounts: map[uint64]*Account{}, groups: map[uint64]*Group{}}
	diagnostics := LegacySchedulerDiagnostics()
	// 保留原结构化日志等级和字段，核心不安装日志后端。
	diagnostics.Event = func(level, event string, args ...any) {
		switch level {
		case "info":
			slog.Info(event, args...)
		case "warn":
			slog.Warn(event, args...)
		case "error":
			slog.Error(event, args...)
		default:
			slog.Debug(event, args...)
		}
	}
	ports := scheduler.GenericSelectionPorts{
		ForcePlatform: func(ctx context.Context) (string, bool) {
			value, ok := ctx.Value(ctxkey.ForcePlatform).(string)
			return value, ok
		},
		ResolveGroupByID: func(ctx context.Context, id int64) (*scheduler.FlowGroup, error) {
			v, err := s.resolveGroupByID(ctx, id)
			return scope.group(v), err
		},
		ResolveGatewayGroup: func(ctx context.Context, id *int64) (*scheduler.FlowGroup, *int64, error) {
			v, finalID, err := s.resolveGatewayGroup(ctx, id)
			return scope.group(v), finalID, err
		},
		HydrateSelectedAccount: func(ctx context.Context, a *scheduler.FlowAccount) (*scheduler.FlowAccount, error) {
			v, err := s.hydrateSelectedAccount(ctx, scope.oldAccount(a))
			return scope.account(v), err
		},
		RoutingAccountIDsForRequest: s.routingAccountIDsForRequest,
		GetSchedulableAccount: func(ctx context.Context, id int64) (*scheduler.FlowAccount, error) {
			v, err := s.getSchedulableAccount(ctx, id)
			return scope.account(v), err
		},
		IsAccountInGroup: func(a *scheduler.FlowAccount, id *int64) bool { return s.isAccountInGroup(scope.oldAccount(a), id) },
		LogDetailedSelectionFailure: func(ctx context.Context, id *int64, hash, model, platform string, values []scheduler.FlowAccount, excluded map[int64]struct{}, mixed bool) string {
			return summarizeSelectionFailureStats(s.logDetailedSelectionFailure(ctx, id, hash, model, platform, scope.oldValues(values), excluded, mixed))
		},
		AdvancedSchedulerStats: func() *scheduler.RuntimeStats { return schedulerStats(s.advancedSchedulerStats()) },
		AdvancedSchedulerEffectiveSettingsForRequest: func(ctx context.Context, id *int64) policy.EffectiveSettings {
			return schedulerEffectiveProjection(s.advancedSchedulerEffectiveSettingsForRequest(ctx, id))
		},
		CheckChannelPricingRestriction:       s.checkChannelPricingRestriction,
		ChannelMappedModelForAccountLayer:    s.channelMappedModelForAccountLayer,
		DebugModelRoutingEnabled:             s.debugModelRoutingEnabled,
		NeedsUpstreamChannelRestrictionCheck: s.needsUpstreamChannelRestrictionCheck,
		TryAcquireAccountSlot:                s.tryAcquireAccountSlot,
		PrefetchedSticky:                     prefetchedStickyAccountIDFromContext,
		SetAccountError: func(ctx context.Context, id int64, message string) error {
			return s.accountRepo.SetError(ctx, id, message)
		},
		SchedulingConfig: func() scheduler.FlowOptions {
			v := s.schedulingConfig()
			return scheduler.FlowOptions{LoadBatchEnabled: v.LoadBatchEnabled, PreferSoonestReset: v.PreferSoonestReset, FallbackMaxWaiting: v.FallbackMaxWaiting, StickySessionMaxWaiting: v.StickySessionMaxWaiting, FallbackSelectionMode: v.FallbackSelectionMode, FallbackWaitTimeout: v.FallbackWaitTimeout, StickySessionWaitTimeout: v.StickySessionWaitTimeout}
		},

		CheckClaudeCodeRestriction: func(ctx context.Context, id *int64) (*scheduler.FlowGroup, *int64, error) {
			g, finalID, err := s.checkClaudeCodeRestriction(ctx, id)
			return scope.group(g), finalID, err
		},
		WithGroupContext: func(ctx context.Context, g *scheduler.FlowGroup) context.Context {
			return s.withGroupContext(ctx, scope.oldGroup(g))
		},
		ResolvePlatform: func(ctx context.Context, id *int64, g *scheduler.FlowGroup) (string, bool, error) {
			return s.resolvePlatform(ctx, id, scope.oldGroup(g))
		},
		ListSchedulableAccounts: func(ctx context.Context, id *int64, platform string, forced bool) ([]scheduler.FlowAccount, bool, error) {
			v, mixed, err := s.listSchedulableAccounts(ctx, id, platform, forced)
			return scope.values(v), mixed, err
		},
		WithRPMPrefetch: func(ctx context.Context, v []scheduler.FlowAccount) context.Context {
			return s.withRPMPrefetch(ctx, scope.oldValues(v))
		},
		WithWindowCostPrefetch: func(ctx context.Context, v []scheduler.FlowAccount) context.Context {
			return s.withWindowCostPrefetch(ctx, scope.oldValues(v))
		},
		IsAccountAllowedForPlatform: func(a *scheduler.FlowAccount, platform string, mixed bool) bool {
			return s.isAccountAllowedForPlatform(scope.oldAccount(a), platform, mixed)
		},
		IsAccountSchedulableForSelection: func(a *scheduler.FlowAccount) bool { return s.isAccountSchedulableForSelection(scope.oldAccount(a)) },
		IsAccountSchedulableForQuota:     func(a *scheduler.FlowAccount) bool { return s.isAccountSchedulableForQuota(scope.oldAccount(a)) },
		IsAccountSchedulableForModelSelection: func(ctx context.Context, a *scheduler.FlowAccount, model string) bool {
			return s.isAccountSchedulableForModelSelection(ctx, scope.oldAccount(a), model)
		},
		IsAccountSchedulableForRPM: func(ctx context.Context, a *scheduler.FlowAccount, sticky bool) bool {
			return s.isAccountSchedulableForRPM(ctx, scope.oldAccount(a), sticky)
		},
		IsAccountSchedulableForWindowCost: func(ctx context.Context, a *scheduler.FlowAccount, sticky bool) bool {
			return s.isAccountSchedulableForWindowCost(ctx, scope.oldAccount(a), sticky)
		},
		IsModelSupportedByAccountWithContext: func(ctx context.Context, a *scheduler.FlowAccount, model string) bool {
			return s.isModelSupportedByAccountWithContext(ctx, scope.oldAccount(a), model)
		},
		IsUpstreamModelRestrictedByChannel: func(ctx context.Context, id int64, a *scheduler.FlowAccount, model string) bool {
			return s.isUpstreamModelRestrictedByChannel(ctx, id, scope.oldAccount(a), model)
		},
		ShouldClearStickySessionForAccountLayer: func(ctx context.Context, a *scheduler.FlowAccount, model string) bool {
			return s.shouldClearStickySessionForAccountLayer(ctx, scope.oldAccount(a), model)
		},
		CheckAndRegisterSession: func(ctx context.Context, a *scheduler.FlowAccount, session string) bool {
			return s.checkAndRegisterSession(ctx, scope.oldAccount(a), session)
		},
		GroupModelUnsupportedErrorIfApplicable: func(ctx context.Context, accounts []scheduler.FlowAccount, model, platform string, excluded map[int64]struct{}, mixed bool, id *int64, g *scheduler.FlowGroup) error {
			return s.groupModelUnsupportedErrorIfApplicable(ctx, scope.oldValues(accounts), model, platform, excluded, mixed, id, scope.oldGroup(g))
		},
		NewSelectionResult: func(ctx context.Context, a *scheduler.FlowAccount, acquired bool, release func(), wait *scheduler.AccountWaitPlan) (*scheduler.FlowSelection, error) {
			v, err := s.newSelectionResult(ctx, scope.oldAccount(a), acquired, release, wait)
			return scope.selection(v), err
		},
	}
	if s.groupRepo != nil {
		ports.ReadGroup = func(ctx context.Context, id int64) (*scheduler.FlowGroup, error) {
			v, err := s.groupRepo.GetByID(ctx, id)
			return scope.group(v), err
		}
	}
	return scheduler.NewGenericSelector(ports, s.cache, s.concurrencyService, diagnostics, time.Now), scope
}

func cloneFlowTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
