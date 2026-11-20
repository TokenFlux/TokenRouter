package selection

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// compatible_ports.go binds compatible provider policy and execution targets
// to the platform scheduler.
// Compatible scheduling needs provider policy and credentials again after
// ranking candidate metadata. One invocation-scoped lookup associates every
// candidate, fresh read, and database read with its exact execution target, so
// same-ID records stay distinct through rechecks and completion.
//
// The adapter binds shared concurrency, performance statistics, sticky state,
// and the advanced picker's Grok quota cache to scheduler.PlatformSelector.
// Protocol, privacy, parent health, model, and transport callbacks read their
// associated target at the stage the core requests. Credential hydration and
// result conversion use the same lookup, carrying release handles and wait
// plans back to the gateway after selection.

// platformSelector builds a compatible scheduler adapter using a background
// context when no request context is supplied.
func (s *compatiblePicker) platformSelector() (*schedulercore.PlatformSelector, *projectionScope) {
	return s.platformSelectorForContext(context.Background())
}

// platformSelectorForContext builds a compatible scheduler adapter with a new
// per-call target lookup bound to reqCtx. Callers supply a picker with its
// constructed Compatible service; optional repository, cache, and snapshot
// flags describe which reads the core can perform.
//
// Callbacks convert target records through the returned scope before invoking
// platform eligibility and hydration, carrying reqCtx into sticky invalidation,
// canonical model resolution, and runtime cooldown lookups. Statistics, metrics,
// concurrency, and the picker's published free-quota gate remain shared across
// invocations. The scope distinguishes repeated persisted IDs so a selected
// database record resolves to that record when the gateway restores the result.
func (s *compatiblePicker) platformSelectorForContext(reqCtx context.Context) (*schedulercore.PlatformSelector, *projectionScope) {
	if reqCtx == nil {
		reqCtx = context.Background()
	}
	scope := &projectionScope{providers: map[uint64]*gatewayprovider.ExecutionProvider{}, groups: map[uint64]*routing.Group{}}
	available := s != nil && s.service != nil
	diagnostics := schedulercore.Diagnostics{
		Logf:  logging.LegacyPrintf,
		Event: logging.Event,
	}

	diagnostics.Event = func(level, event string, args ...any) {
		switch level {
		case "info":
			slog.Info(event, args...)
		case "warn":
			slog.Warn(event, args...)
		default:
			slog.Debug(event, args...)
		}
	}
	ports := schedulercore.PlatformSelectionPorts{
		BasicStickyTTL: openaiStickySessionTTL, CheckPricing: s.service.CheckGroupModelRestriction,
		Hydrate: func(ctx context.Context, a *schedulercore.FlowProvider) (*schedulercore.FlowProvider, error) {
			v, err := s.service.hydrateSelectedProvider(ctx, scope.oldProvider(a))
			return scope.provider(v), err
		},
		SetSticky: s.service.setStickySessionProviderID,
		PrivacyAllowed: func(ctx context.Context, id *int64, a *schedulercore.FlowProvider) bool {
			return s.service.openAIProviderPassesPrivacyRequirement(ctx, id, scope.oldProvider(a))
		},
		ShadowAllowed: func(ctx context.Context, a *schedulercore.FlowProvider) bool {
			return s.service.shadowProtocolsAllowed(ctx, scope.oldProvider(a))
		},
		ParentHealthy: func(a *schedulercore.FlowProvider, lookup func(int64) *schedulercore.FlowProvider) bool {
			return provider.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(scope.oldProvider(a)), func(id int64) *provider.Record {
				return gatewayprovider.ExecutionRecord(scope.oldProvider(lookup(id)))
			})
		},
		ParentLookup: func(ctx context.Context) func(int64) *schedulercore.FlowProvider {
			lookup := s.service.parentProviderLookup(ctx)
			return func(id int64) *schedulercore.FlowProvider { return scope.provider(lookup(id)) }
		},
		NeedsGroupCheck: s.service.NeedsUpstreamGroupRestriction,
		GroupModelRestricted: func(ctx context.Context, id int64, a *schedulercore.FlowProvider, model string, compact bool) bool {
			return s.service.UpstreamRoutingModelRestricted(ctx, id, scope.oldProvider(a), model, compact)
		},
		BasicEligible: func(ctx context.Context, a *schedulercore.FlowProvider, platform, model string, compact bool, capability provider.OpenAIEndpointCapability) bool {
			return s.service.basicCandidateEligibilityReason(ctx, scope.oldProvider(a), platform, model, compact, capability) == ""
		},
		BasicFailureReason: func(ctx context.Context, a *schedulercore.FlowProvider, platform, model string, compact bool, capability provider.OpenAIEndpointCapability) string {
			return s.service.basicCandidateEligibilityReason(ctx, scope.oldProvider(a), platform, model, compact, capability)
		},
		CompleteAcquired: func(ctx context.Context, a *schedulercore.FlowProvider, release func()) (*schedulercore.FlowSelection, error) {
			v, err := s.service.newAcquiredSelectionResult(ctx, scope.oldProvider(a), release)
			return scope.selection(v), err
		},
		Complete: func(ctx context.Context, a *schedulercore.FlowProvider, acquired bool, release func(), wait *schedulercore.ProviderWaitPlan) (*schedulercore.FlowSelection, error) {
			v, err := s.service.newSelectionResult(ctx, scope.oldProvider(a), acquired, release, wait)
			return scope.selection(v), err
		},
		Available: available, CacheAvailable: available && s.service.cache != nil, SnapshotAvailable: available && s.service.schedulerSnapshot != nil, RecheckAvailable: available && s.service.schedulerSnapshot != nil && s.service.providerRepo != nil,
		Effective: func(ctx context.Context, id *int64) policy.EffectiveSettings {
			return s.service.advancedSchedulerEffectiveSettingsForRequest(ctx, id)
		},
		GroupRequiresPrivacy: s.service.openAIGroupRequiresPrivacySet,
		PreviousResponse: func(ctx context.Context, id *int64, previous, model string, excluded map[int64]struct{}, capability provider.OpenAIEndpointCapability, compact bool) (*schedulercore.FlowSelection, error) {
			v, err := s.service.selectProviderByPreviousResponseIDForCapability(ctx, id, previous, model, excluded, capability, compact)
			return scope.selection(v), err
		},
		RequestCompatible: func(ctx context.Context, a *schedulercore.FlowProvider, input schedulercore.PlatformSelectionInput) (bool, string) {
			return s.isProviderRequestCompatibleReason(ctx, scope.oldProvider(a), input)
		},
		TransportCompatible: func(a *schedulercore.FlowProvider, transport string) bool {
			return s.isProviderTransportCompatible(scope.oldProvider(a), egress.OpenAIUpstreamTransport(transport))
		},
		HasGroupMetadata: func(a *schedulercore.FlowProvider) bool { return hasOpenAIProviderGroupMetadata(scope.oldProvider(a)) },
		MatchesGroup: func(a *schedulercore.FlowProvider, id *int64) bool {
			return s.service.openAIProviderMatchesSchedulingGroup(scope.oldProvider(a), id)
		},
		BindSticky: s.service.BindStickySession, DeleteSticky: s.service.deleteStickySessionProviderID, GetSticky: s.service.getStickySessionProviderID, RefreshSticky: s.service.refreshStickySessionTTL, StickyTTL: s.service.SessionStickyTTL,
		Options: func() schedulercore.FlowOptions {
			return s.service.schedulingConfig()
		},
		GetSchedulable: func(ctx context.Context, id int64) (*schedulercore.FlowProvider, error) {
			v, err := s.service.getSchedulableProvider(ctx, id)
			return scope.provider(v), err
		},
		ClearSticky: func(a *schedulercore.FlowProvider, model string) bool {
			return shouldClearStickySession(reqCtx, scope.oldProvider(a), model)
		},
		IsCompatible:  func(a *schedulercore.FlowProvider) bool { return scope.oldProvider(a) != nil },
		IsSchedulable: func(a *schedulercore.FlowProvider) bool { return scope.oldProvider(a).View().IsSchedulable() },
		Recheck: func(ctx context.Context, a *schedulercore.FlowProvider, id *int64, platform, model string, compact bool, capability provider.OpenAIEndpointCapability) *schedulercore.FlowProvider {
			return scope.provider(s.service.recheckSelectedOpenAIProviderFromDB(ctx, scope.oldProvider(a), id, platform, model, compact, capability))
		},
		Fresh: func(ctx context.Context, a *schedulercore.FlowProvider, platform, model string, compact bool, capability provider.OpenAIEndpointCapability) *schedulercore.FlowProvider {
			return scope.provider(s.service.resolveFreshSchedulableOpenAIProvider(ctx, scope.oldProvider(a), platform, model, compact, capability))
		},
		FreeQuota: func(ctx context.Context, values []schedulercore.FlowProvider) []schedulercore.FlowProvider {
			return scope.values(s.filterGrokFreeQuotaProviders(ctx, scope.oldValues(values)))
		},
		CanonicalModel: func(a *schedulercore.FlowProvider, model string) string {
			return gatewayprovider.ExecutionModelPolicy(scope.oldProvider(a)).WithContext(reqCtx).CanonicalSchedulingModel(model)
		},
		TeamLimited: func(a *schedulercore.FlowProvider, model string, now time.Time) bool {
			return isGrokTeamModelRateLimited(scope.oldProvider(a), model, now)
		},
		ModelQuotaBlocked: provider.IsGrokModelQuotaBlocked, Acquire: s.service.tryAcquireProviderSlot,
		ListCandidates: func(ctx context.Context, id *int64, platform string) ([]schedulercore.FlowProvider, error) {
			v, err := s.service.listSchedulableProviders(ctx, id, platform)
			return scope.values(v), err
		},
		RuntimeBlocked: func(a *schedulercore.FlowProvider, model string) bool {
			return s.service.isOpenAIProviderRequestRuntimeBlocked(reqCtx, scope.oldProvider(a), model)
		},
		FilterTeamLimited: func(values []schedulercore.FlowProvider, model string, now time.Time) []schedulercore.FlowProvider {
			return scope.values(filterGrokTeamModelRateLimitedProviders(reqCtx, scope.oldValues(values), model, now))
		},
		FilterModelQuota: func(values []schedulercore.FlowProvider, model string, now time.Time) []schedulercore.FlowProvider {
			return scope.values(filterGrokModelQuotaBlockedProviders(reqCtx, scope.oldValues(values), model, now))
		},
		CompactAllowed: func(a *schedulercore.FlowProvider) bool {
			return gatewayprovider.AllowsCompatibleCompact(scope.oldProvider(a))
		},
		IsSubscription: func(a *schedulercore.FlowProvider) bool {
			return scope.oldProvider(a).View().IsOpenAIChatGPTSubscription()
		},
		QuotaHeadroom: func(a *schedulercore.ScoreProvider, now time.Time) float64 {
			return openAIQuotaHeadroomFactor(scope.providers[a.ProjectionID], now)
		},
		Unavailable: func(ctx context.Context, requested, model string, compact bool, details string, collections ...[]schedulercore.FlowProvider) error {
			converted := make([][]gatewayprovider.ExecutionProvider, len(collections))
			for i, v := range collections {
				converted[i] = scope.oldValues(v)
			}
			return noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, requested, model, compact, details, converted...)
		},
	}
	if available && s.service.providerRepo != nil {
		ports.ReadProviderDB = func(ctx context.Context, id int64) (*schedulercore.FlowProvider, error) {
			v, err := s.service.providerRepo.GetByID(ctx, id)
			return scope.provider(v), err
		}
	}
	var concurrency *schedulercore.ConcurrencyService
	if available {
		concurrency = s.service.concurrencyService
	}
	return schedulercore.NewPlatformSelector(ports, concurrency, s.stats, &s.metrics.PlatformMetrics, diagnostics, time.Now), scope
}
