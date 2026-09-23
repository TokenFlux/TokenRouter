package selection

import (
	"context"
	"log/slog"
	"time"

	account "github.com/TokenFlux/TokenRouter/internal/account"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// platformSelector 的关联表只存在于本次调用；缓存、Grok 资格观测和 EWMA 均复用原唯一实例。
func (s *compatiblePicker) platformSelector() (*schedulercore.PlatformSelector, *projectionScope) {
	scope := &projectionScope{accounts: map[uint64]*gatewayprovider.ExecutionAccount{}, groups: map[uint64]*routing.Group{}}
	available := s != nil && s.service != nil
	diagnostics := schedulercore.Diagnostics{Logf: logging.LegacyPrintf,
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
		BasicStickyTTL: openaiStickySessionTTL, CheckPricing: s.service.CheckChannelPricingRestriction,
		Hydrate: func(ctx context.Context, a *schedulercore.FlowAccount) (*schedulercore.FlowAccount, error) {
			v, err := s.service.hydrateSelectedAccount(ctx, scope.oldAccount(a))
			return scope.account(v), err
		},
		SetSticky: s.service.setStickySessionAccountID,
		PrivacyAllowed: func(ctx context.Context, id *int64, a *schedulercore.FlowAccount) bool {
			return s.service.openAIAccountPassesPrivacyRequirement(ctx, id, scope.oldAccount(a))
		},
		ShadowAllowed: func(ctx context.Context, a *schedulercore.FlowAccount) bool {
			return s.service.shadowProtocolsAllowed(ctx, scope.oldAccount(a))
		},
		ParentHealthy: func(a *schedulercore.FlowAccount, lookup func(int64) *schedulercore.FlowAccount) bool {
			return account.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(scope.oldAccount(a)), func(id int64) *account.Record {
				return gatewayprovider.ExecutionRecord(scope.oldAccount(lookup(id)))
			})
		},
		ParentLookup: func(ctx context.Context) func(int64) *schedulercore.FlowAccount {
			lookup := s.service.parentAccountLookup(ctx)
			return func(id int64) *schedulercore.FlowAccount { return scope.account(lookup(id)) }
		},
		NeedsChannelCheck: s.service.NeedsUpstreamChannelRestriction,
		ChannelRestricted: func(ctx context.Context, id int64, a *schedulercore.FlowAccount, model string, compact bool) bool {
			return s.service.UpstreamRoutingModelRestricted(ctx, id, scope.oldAccount(a), model, compact)
		},
		BasicEligible: func(ctx context.Context, a *schedulercore.FlowAccount, platform, model string, compact bool, capability account.OpenAIEndpointCapability) bool {
			return gatewayprovider.CompatibleAccountEligible(ctx, scope.oldAccount(a), platform, model, compact, capability)
		},
		BasicFailureReason: func(ctx context.Context, a *schedulercore.FlowAccount, platform, model string, compact bool, capability account.OpenAIEndpointCapability) string {
			return gatewayprovider.CompatibleEligibilityReason(ctx, scope.oldAccount(a), platform, model, compact, capability)
		},
		CompleteAcquired: func(ctx context.Context, a *schedulercore.FlowAccount, release func()) (*schedulercore.FlowSelection, error) {
			v, err := s.service.newAcquiredSelectionResult(ctx, scope.oldAccount(a), release)
			return scope.selection(v), err
		},
		Complete: func(ctx context.Context, a *schedulercore.FlowAccount, acquired bool, release func(), wait *schedulercore.AccountWaitPlan) (*schedulercore.FlowSelection, error) {
			v, err := s.service.newSelectionResult(ctx, scope.oldAccount(a), acquired, release, wait)
			return scope.selection(v), err
		},
		Available: available, CacheAvailable: available && s.service.cache != nil, SnapshotAvailable: available && s.service.schedulerSnapshot != nil, RecheckAvailable: available && s.service.schedulerSnapshot != nil && s.service.accountRepo != nil,
		Effective: func(ctx context.Context, id *int64) policy.EffectiveSettings {
			return s.service.advancedSchedulerEffectiveSettingsForRequest(ctx, id)
		},
		GroupRequiresPrivacy: s.service.openAIGroupRequiresPrivacySet,
		PreviousResponse: func(ctx context.Context, id *int64, previous, model string, excluded map[int64]struct{}, capability account.OpenAIEndpointCapability, compact bool) (*schedulercore.FlowSelection, error) {
			v, err := s.service.selectAccountByPreviousResponseIDForCapability(ctx, id, previous, model, excluded, capability, compact)
			return scope.selection(v), err
		},
		RequestCompatible: func(ctx context.Context, a *schedulercore.FlowAccount, input schedulercore.PlatformSelectionInput) (bool, string) {
			return s.isAccountRequestCompatibleReason(ctx, scope.oldAccount(a), input)
		},
		TransportCompatible: func(a *schedulercore.FlowAccount, transport string) bool {
			return s.isAccountTransportCompatible(scope.oldAccount(a), egress.OpenAIUpstreamTransport(transport))
		},
		HasGroupMetadata: func(a *schedulercore.FlowAccount) bool { return hasOpenAIAccountGroupMetadata(scope.oldAccount(a)) },
		MatchesGroup: func(a *schedulercore.FlowAccount, id *int64) bool {
			return s.service.openAIAccountMatchesSchedulingGroup(scope.oldAccount(a), id)
		},
		BindSticky: s.service.BindStickySession, DeleteSticky: s.service.deleteStickySessionAccountID, GetSticky: s.service.getStickySessionAccountID, RefreshSticky: s.service.refreshStickySessionTTL, StickyTTL: s.service.SessionStickyTTL,
		Options: func() schedulercore.FlowOptions {
			v := s.service.schedulingConfig()
			return schedulercore.FlowOptions{LoadBatchEnabled: v.LoadBatchEnabled, PreferSoonestReset: v.PreferSoonestReset, FallbackMaxWaiting: v.FallbackMaxWaiting, StickySessionMaxWaiting: v.StickySessionMaxWaiting, FallbackSelectionMode: v.FallbackSelectionMode, FallbackWaitTimeout: v.FallbackWaitTimeout, StickySessionWaitTimeout: v.StickySessionWaitTimeout}
		},
		GetSchedulable: func(ctx context.Context, id int64) (*schedulercore.FlowAccount, error) {
			v, err := s.service.getSchedulableAccount(ctx, id)
			return scope.account(v), err
		},
		ClearSticky: func(a *schedulercore.FlowAccount, model string) bool {
			return shouldClearStickySession(scope.oldAccount(a), model)
		},
		IsCompatible:  func(a *schedulercore.FlowAccount) bool { return scope.oldAccount(a).View().IsOpenAICompatible() },
		IsSchedulable: func(a *schedulercore.FlowAccount) bool { return scope.oldAccount(a).View().IsSchedulable() },
		Recheck: func(ctx context.Context, a *schedulercore.FlowAccount, id *int64, platform, model string, compact bool, capability account.OpenAIEndpointCapability) *schedulercore.FlowAccount {
			return scope.account(s.service.recheckSelectedOpenAIAccountFromDB(ctx, scope.oldAccount(a), id, platform, model, compact, capability))
		},
		Fresh: func(ctx context.Context, a *schedulercore.FlowAccount, platform, model string, compact bool, capability account.OpenAIEndpointCapability) *schedulercore.FlowAccount {
			return scope.account(s.service.resolveFreshSchedulableOpenAIAccount(ctx, scope.oldAccount(a), platform, model, compact, capability))
		},
		FreeQuota: func(ctx context.Context, values []schedulercore.FlowAccount) []schedulercore.FlowAccount {
			return scope.values(s.filterGrokFreeQuotaAccounts(ctx, scope.oldValues(values)))
		},
		CanonicalModel: func(a *schedulercore.FlowAccount, model string) string {
			return gatewayprovider.ExecutionModelPolicy(scope.oldAccount(a)).CanonicalSchedulingModel(model)
		},
		TeamLimited: func(a *schedulercore.FlowAccount, model string, now time.Time) bool {
			return isGrokTeamModelRateLimited(scope.oldAccount(a), model, now)
		},
		ModelQuotaBlocked: account.IsGrokModelQuotaBlocked, Acquire: s.service.tryAcquireAccountSlot,
		ListCandidates: func(ctx context.Context, id *int64, platform string) ([]schedulercore.FlowAccount, error) {
			v, err := s.service.listSchedulableAccounts(ctx, id, platform)
			return scope.values(v), err
		},
		RuntimeBlocked: func(a *schedulercore.FlowAccount, model string) bool {
			return s.service.isOpenAIAccountRequestRuntimeBlocked(scope.oldAccount(a), model)
		},
		FilterTeamLimited: func(values []schedulercore.FlowAccount, model string, now time.Time) []schedulercore.FlowAccount {
			return scope.values(filterGrokTeamModelRateLimitedAccounts(scope.oldValues(values), model, now))
		},
		FilterModelQuota: func(values []schedulercore.FlowAccount, model string, now time.Time) []schedulercore.FlowAccount {
			return scope.values(filterGrokModelQuotaBlockedAccounts(scope.oldValues(values), model, now))
		},
		CompactAllowed: func(a *schedulercore.FlowAccount) bool {
			return gatewayprovider.AllowsCompatibleCompact(scope.oldAccount(a))
		},
		IsSubscription: func(a *schedulercore.FlowAccount) bool {
			return scope.oldAccount(a).View().IsOpenAIChatGPTSubscription()
		},
		QuotaHeadroom: func(a *schedulercore.ScoreAccount, now time.Time) float64 {
			return openAIQuotaHeadroomFactor(scope.accounts[a.ProjectionID], now)
		},
		Unavailable: func(ctx context.Context, requested, model string, compact bool, details string, collections ...[]schedulercore.FlowAccount) error {
			converted := make([][]gatewayprovider.ExecutionAccount, len(collections))
			for i, v := range collections {
				converted[i] = scope.oldValues(v)
			}
			return noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, requested, model, compact, details, converted...)
		},
	}
	if available && s.service.accountRepo != nil {
		ports.ReadAccountDB = func(ctx context.Context, id int64) (*schedulercore.FlowAccount, error) {
			v, err := s.service.accountRepo.GetByID(ctx, id)
			return scope.account(v), err
		}
	}
	var concurrency *schedulercore.ConcurrencyService
	if available {
		concurrency = s.service.concurrencyService
	}
	return schedulercore.NewPlatformSelector(ports, concurrency, s.stats, &s.metrics.PlatformMetrics, diagnostics, time.Now), scope
}
