package selection

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"

	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// genericSelectionScope 用临时关联号保留同 ID 的多份读取快照，选择结束后即释放。
type projectionScope struct {
	next     uint64
	accounts map[uint64]*gatewayprovider.ExecutionAccount
	groups   map[uint64]*routing.Group
}

func (g *projectionScope) account(value *gatewayprovider.ExecutionAccount) *schedulercore.FlowAccount {
	if value == nil {
		return nil
	}
	g.next++
	id := g.next
	g.accounts[id] = value
	var plan *routing.CandidatePlan
	if captured, ok := value.Route.Candidate(); ok {
		plan = &captured
	}
	return &schedulercore.FlowAccount{Plan: plan, ProjectionID: id, ID: value.Record.ID, Name: value.Record.Name, Platform: value.Record.Platform, Type: value.Record.Type, Concurrency: value.Record.Concurrency, Priority: value.Record.Priority, LastUsedAt: cloneFlowTime(value.Record.LastUsedAt), SessionWindowEnd: cloneFlowTime(value.Record.SessionWindowEnd), LoadFactor: value.View().EffectiveLoadFactor(), BaseRPM: gatewayprovider.ExecutionRuntimeConfig(value).GetBaseRPM(), PrivacySet: value.View().IsPrivacySet(), MixedScheduling: value.View().IsMixedSchedulingEnabled()}
}

func (g *projectionScope) group(value *routing.Group) *schedulercore.FlowGroup {
	if value == nil {
		return nil
	}
	g.next++
	id := g.next
	g.groups[id] = value
	return &schedulercore.FlowGroup{ProjectionID: id, Group: *routing.CloneGroup(value)}
}

func (g *projectionScope) oldAccount(v *schedulercore.FlowAccount) *gatewayprovider.ExecutionAccount {
	if v == nil {
		return nil
	}
	return g.accounts[v.ProjectionID]
}

func (g *projectionScope) oldGroup(v *schedulercore.FlowGroup) *routing.Group {
	if v == nil {
		return nil
	}
	return g.groups[v.ProjectionID]
}

func (g *projectionScope) values(values []gatewayprovider.ExecutionAccount) []schedulercore.FlowAccount {
	if values == nil {
		return nil
	}
	out := make([]schedulercore.FlowAccount, len(values))
	for i := range values {
		out[i] = *g.account(&values[i])
	}
	return out
}

func (g *projectionScope) oldValues(values []schedulercore.FlowAccount) []gatewayprovider.ExecutionAccount {
	if values == nil {
		return nil
	}
	out := make([]gatewayprovider.ExecutionAccount, len(values))
	for i := range values {
		out[i] = *g.oldAccount(&values[i])
	}
	return out
}

func (g *projectionScope) pointers(values []*gatewayprovider.ExecutionAccount) []*schedulercore.FlowAccount {
	if values == nil {
		return nil
	}
	out := make([]*schedulercore.FlowAccount, len(values))
	for i, a := range values {
		out[i] = g.account(a)
	}
	return out
}

func (g *projectionScope) loads(values []accountWithLoad) []schedulercore.FlowLoad {
	if values == nil {
		return nil
	}
	out := make([]schedulercore.FlowLoad, len(values))
	for i, a := range values {
		out[i] = schedulercore.FlowLoad{Account: g.account(a.account), LoadInfo: a.loadInfo}
	}
	return out
}

func (g *projectionScope) selection(value *gatewayprovider.SelectionResult) *schedulercore.FlowSelection {
	if value == nil {
		return nil
	}
	out := &schedulercore.FlowSelection{Account: g.account(value.Account), Acquired: value.Acquired, ReleaseFunc: value.ReleaseFunc, WaitPlan: value.WaitPlan, AdvancedScheduler: value.AdvancedScheduler}
	if v := value.AdvancedSchedulerFeedback; v != nil {
		out.AdvancedSchedulerFeedback = &policy.FeedbackConfig{ErrorRateAlpha: v.ErrorRateAlpha, TtftAlpha: v.TtftAlpha}
	}
	return out
}

func (g *projectionScope) restore(value *schedulercore.FlowSelection) *gatewayprovider.SelectionResult {
	if value == nil {
		return nil
	}
	out := &gatewayprovider.SelectionResult{Account: g.oldAccount(value.Account), Acquired: value.Acquired, ReleaseFunc: value.ReleaseFunc, WaitPlan: value.WaitPlan, AdvancedScheduler: value.AdvancedScheduler}
	if v := value.AdvancedSchedulerFeedback; v != nil {
		out.AdvancedSchedulerFeedback = &policy.FeedbackConfig{ErrorRateAlpha: v.ErrorRateAlpha, TtftAlpha: v.TtftAlpha}
	}
	return out
}

func (s *Generic) genericSelector() (*schedulercore.GenericSelector, *projectionScope) {
	scope := &projectionScope{accounts: map[uint64]*gatewayprovider.ExecutionAccount{}, groups: map[uint64]*routing.Group{}}
	diagnostics := schedulercore.Diagnostics{Logf: logging.LegacyPrintf, Event: logging.Event}

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
	ports := schedulercore.GenericSelectionPorts{
		ForcePlatform: func(ctx context.Context) (string, bool) {
			value, ok := apikey.ForcePlatformFromContext(ctx)
			return value, ok
		},
		ResolveGroupByID: func(ctx context.Context, id int64) (*schedulercore.FlowGroup, error) {
			v, err := s.resolveGroupByID(ctx, id)
			return scope.group(v), err
		},
		ResolveGatewayGroup: func(ctx context.Context, id *int64) (*schedulercore.FlowGroup, *int64, error) {
			v, finalID, err := s.resolveGatewayGroup(ctx, id)
			return scope.group(v), finalID, err
		},
		HydrateSelectedAccount: func(ctx context.Context, a *schedulercore.FlowAccount) (*schedulercore.FlowAccount, error) {
			v, err := s.hydrateSelectedAccount(ctx, scope.oldAccount(a))
			return scope.account(v), err
		},
		RoutingAccountIDsForRequest: s.routingAccountIDsForRequest,
		GetSchedulableAccount: func(ctx context.Context, id int64) (*schedulercore.FlowAccount, error) {
			v, err := s.getSchedulableAccount(ctx, id)
			return scope.account(v), err
		},
		IsAccountInGroup: func(a *schedulercore.FlowAccount, id *int64) bool { return s.isAccountInGroup(scope.oldAccount(a), id) },
		LogDetailedSelectionFailure: func(ctx context.Context, id *int64, hash, model, platform string, values []schedulercore.FlowAccount, excluded map[int64]struct{}, mixed bool) string {
			return summarizeSelectionFailureStats(s.logDetailedSelectionFailure(ctx, id, hash, model, platform, scope.oldValues(values), excluded, mixed))
		},
		AdvancedSchedulerStats: func() *schedulercore.RuntimeStats { return s.advancedSchedulerStats() },
		AdvancedSchedulerEffectiveSettingsForRequest: func(ctx context.Context, id *int64) policy.EffectiveSettings {
			return s.advancedSchedulerEffectiveSettingsForRequest(ctx, id)
		},
		CheckGroupModelRestriction:         s.checkGroupModelRestriction,
		GroupMappedModelForAccountLayer:    s.groupMappedModelForAccountLayer,
		DebugModelRoutingEnabled:           s.debugModelRoutingEnabled,
		NeedsUpstreamGroupRestrictionCheck: s.needsUpstreamGroupRestrictionCheck,
		TryAcquireAccountSlot:              s.tryAcquireAccountSlot,
		PrefetchedSticky:                   prefetchedStickyAccountIDFromContext,
		SetAccountError: func(ctx context.Context, id int64, message string) error {
			return s.setAccountError(ctx, id, message)
		},
		SchedulingConfig: func() schedulercore.FlowOptions {
			v := s.schedulingConfig()
			return schedulercore.FlowOptions{LoadBatchEnabled: v.LoadBatchEnabled, PreferSoonestReset: v.PreferSoonestReset, FallbackMaxWaiting: v.FallbackMaxWaiting, StickySessionMaxWaiting: v.StickySessionMaxWaiting, FallbackSelectionMode: v.FallbackSelectionMode, FallbackWaitTimeout: v.FallbackWaitTimeout, StickySessionWaitTimeout: v.StickySessionWaitTimeout}
		},

		CheckClaudeCodeRestriction: func(ctx context.Context, id *int64) (*schedulercore.FlowGroup, *int64, error) {
			g, finalID, err := s.checkClaudeCodeRestriction(ctx, id)
			return scope.group(g), finalID, err
		},
		WithGroupContext: func(ctx context.Context, g *schedulercore.FlowGroup) context.Context {
			return s.withGroupContext(ctx, scope.oldGroup(g))
		},
		ResolvePlatform: func(ctx context.Context, id *int64, g *schedulercore.FlowGroup) (string, bool, error) {
			return s.resolvePlatform(ctx, id, scope.oldGroup(g))
		},
		ListSchedulableAccounts: func(ctx context.Context, id *int64, platform string, forced bool) ([]schedulercore.FlowAccount, bool, error) {
			v, mixed, err := s.listSchedulableAccounts(ctx, id, platform, forced)
			return scope.values(v), mixed, err
		},
		WithRPMPrefetch: func(ctx context.Context, v []schedulercore.FlowAccount) context.Context {
			return s.withRPMPrefetch(ctx, scope.oldValues(v))
		},
		WithWindowCostPrefetch: func(ctx context.Context, v []schedulercore.FlowAccount) context.Context {
			return s.withWindowCostPrefetch(ctx, scope.oldValues(v))
		},
		IsAccountAllowedForPlatform: func(a *schedulercore.FlowAccount, platform string, mixed bool) bool {
			return s.isAccountAllowedForPlatform(scope.oldAccount(a), platform, mixed)
		},
		IsAccountSchedulableForSelection: func(a *schedulercore.FlowAccount) bool {
			return s.isAccountSchedulableForSelection(scope.oldAccount(a))
		},
		IsAccountSchedulableForQuota: func(a *schedulercore.FlowAccount) bool { return s.isAccountSchedulableForQuota(scope.oldAccount(a)) },
		IsAccountSchedulableForModelSelection: func(ctx context.Context, a *schedulercore.FlowAccount, model string) bool {
			return s.isAccountSchedulableForModelSelection(ctx, scope.oldAccount(a), model)
		},
		IsAccountSchedulableForRPM: func(ctx context.Context, a *schedulercore.FlowAccount, sticky bool) bool {
			return s.isAccountSchedulableForRPM(ctx, scope.oldAccount(a), sticky)
		},
		IsAccountSchedulableForWindowCost: func(ctx context.Context, a *schedulercore.FlowAccount, sticky bool) bool {
			return s.isAccountSchedulableForWindowCost(ctx, scope.oldAccount(a), sticky)
		},
		IsModelSupportedByAccountWithContext: func(ctx context.Context, a *schedulercore.FlowAccount, model string) bool {
			return s.isModelSupportedByAccountWithContext(ctx, scope.oldAccount(a), model)
		},
		IsUpstreamModelRestrictedByGroup: func(ctx context.Context, id int64, a *schedulercore.FlowAccount, model string) bool {
			return s.isUpstreamModelRestrictedByGroup(ctx, id, scope.oldAccount(a), model)
		},
		ShouldClearStickySessionForAccountLayer: func(ctx context.Context, a *schedulercore.FlowAccount, model string) bool {
			return s.shouldClearStickySessionForAccountLayer(ctx, scope.oldAccount(a), model)
		},
		CheckAndRegisterSession: func(ctx context.Context, a *schedulercore.FlowAccount, session string) bool {
			return s.checkAndRegisterSession(ctx, scope.oldAccount(a), session)
		},
		GroupModelUnsupportedErrorIfApplicable: func(ctx context.Context, accounts []schedulercore.FlowAccount, model, platform string, excluded map[int64]struct{}, mixed bool, id *int64, g *schedulercore.FlowGroup) error {
			return s.groupModelUnsupportedErrorIfApplicable(ctx, scope.oldValues(accounts), model, platform, excluded, mixed, id, scope.oldGroup(g))
		},
		NewSelectionResult: func(ctx context.Context, a *schedulercore.FlowAccount, acquired bool, release func(), wait *schedulercore.AccountWaitPlan) (*schedulercore.FlowSelection, error) {
			v, err := s.newSelectionResult(ctx, scope.oldAccount(a), acquired, release, wait)
			return scope.selection(v), err
		},
	}
	if s.groupRepo != nil {
		ports.ReadGroup = func(ctx context.Context, id int64) (*schedulercore.FlowGroup, error) {
			v, err := s.groupRepo.GetByID(ctx, id)
			return scope.group(v), err
		}
	}
	return schedulercore.NewGenericSelector(ports, s.cache, s.concurrencyService, diagnostics, time.Now), scope
}

func cloneFlowTime(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	copy := *v
	return &copy
}
