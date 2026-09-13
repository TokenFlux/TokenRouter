package service

import (
	"context"
	"log/slog"
	"maps"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// 输入只投影本次选择字段，平台交换和完整执行账号仍归旧执行适配。
func platformSelectionInput(v OpenAIAccountScheduleRequest) scheduler.PlatformSelectionInput {
	return scheduler.PlatformSelectionInput{
		GroupID:                         v.GroupID,
		Platform:                        v.Platform,
		SessionHash:                     v.SessionHash,
		StickyAccountID:                 v.StickyAccountID,
		GuardianParentAccountID:         v.GuardianParentAccountID,
		StickyPreviousAccountID:         v.StickyPreviousAccountID,
		StickyWeighted:                  v.StickyWeighted,
		SubscriptionPriority:            v.SubscriptionPriority,
		PreserveStickyBinding:           v.PreserveStickyBinding,
		RequirePrivacySet:               v.RequirePrivacySet,
		PreviousResponseID:              v.PreviousResponseID,
		PreviousResponseCanMove:         v.PreviousResponseCanMove,
		RequestedModel:                  v.RequestedModel,
		RoutingModel:                    v.RoutingModel,
		RequiredTransport:               string(v.RequiredTransport),
		RequiredCapability:              v.RequiredCapability,
		RequiredImageCapability:         v.RequiredImageCapability,
		RequireCompact:                  v.RequireCompact,
		ExcludedIDs:                     maps.Clone(v.ExcludedIDs),
		AdvancedSchedulerFeedbackConfig: policy.FeedbackConfig{ErrorRateAlpha: v.AdvancedSchedulerFeedbackConfig.errorRateAlpha, TtftAlpha: v.AdvancedSchedulerFeedbackConfig.ttftAlpha},
		StickyEscapeConfig:              policy.StickyEscapeConfig{Enabled: v.StickyEscapeConfig.enabled, TtftMs: v.StickyEscapeConfig.ttftMs, ErrorRate: v.StickyEscapeConfig.errorRate},
	}
}
func legacyPlatformInput(v scheduler.PlatformSelectionInput) OpenAIAccountScheduleRequest {
	return OpenAIAccountScheduleRequest{
		GroupID:                         v.GroupID,
		Platform:                        v.Platform,
		SessionHash:                     v.SessionHash,
		StickyAccountID:                 v.StickyAccountID,
		GuardianParentAccountID:         v.GuardianParentAccountID,
		StickyPreviousAccountID:         v.StickyPreviousAccountID,
		StickyWeighted:                  v.StickyWeighted,
		SubscriptionPriority:            v.SubscriptionPriority,
		PreserveStickyBinding:           v.PreserveStickyBinding,
		RequirePrivacySet:               v.RequirePrivacySet,
		PreviousResponseID:              v.PreviousResponseID,
		PreviousResponseCanMove:         v.PreviousResponseCanMove,
		RequestedModel:                  v.RequestedModel,
		RoutingModel:                    v.RoutingModel,
		RequiredTransport:               OpenAIUpstreamTransport(v.RequiredTransport),
		RequiredCapability:              v.RequiredCapability,
		RequiredImageCapability:         v.RequiredImageCapability,
		RequireCompact:                  v.RequireCompact,
		ExcludedIDs:                     v.ExcludedIDs,
		AdvancedSchedulerFeedbackConfig: advancedSchedulerFeedbackConfig{errorRateAlpha: v.AdvancedSchedulerFeedbackConfig.ErrorRateAlpha, ttftAlpha: v.AdvancedSchedulerFeedbackConfig.TtftAlpha},
		StickyEscapeConfig:              advancedStickyEscapeConfig{enabled: v.StickyEscapeConfig.Enabled, ttftMs: v.StickyEscapeConfig.TtftMs, errorRate: v.StickyEscapeConfig.ErrorRate},
	}
}

// platformSelector 的关联表只存在于本次调用；缓存、Grok 资格观测和 EWMA 均复用原唯一实例。
func (s *defaultOpenAIAccountScheduler) platformSelector() (*scheduler.PlatformSelector, *genericSelectionScope) {
	scope := &genericSelectionScope{accounts: map[uint64]*Account{}, groups: map[uint64]*Group{}}
	available := s != nil && s.service != nil
	diagnostics := LegacySchedulerDiagnostics()
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
	ports := scheduler.PlatformSelectionPorts{
		BasicStickyTTL: openaiStickySessionTTL, CheckPricing: s.service.checkChannelPricingRestriction,
		Hydrate: func(ctx context.Context, a *scheduler.FlowAccount) (*scheduler.FlowAccount, error) {
			v, err := s.service.hydrateSelectedAccount(ctx, scope.oldAccount(a))
			return scope.account(v), err
		},
		SetSticky: s.service.setStickySessionAccountID,
		PrivacyAllowed: func(ctx context.Context, id *int64, a *scheduler.FlowAccount) bool {
			return s.service.openAIAccountPassesPrivacyRequirement(ctx, id, scope.oldAccount(a))
		},
		ShadowAllowed: func(ctx context.Context, a *scheduler.FlowAccount) bool {
			return s.service.shadowProtocolsAllowed(ctx, scope.oldAccount(a))
		},
		ParentHealthy: func(a *scheduler.FlowAccount, lookup func(int64) *scheduler.FlowAccount) bool {
			return parentHealthyForShadow(scope.oldAccount(a), func(id int64) *Account { return scope.oldAccount(lookup(id)) })
		},
		ParentLookup: func(ctx context.Context) func(int64) *scheduler.FlowAccount {
			lookup := s.service.parentAccountLookup(ctx)
			return func(id int64) *scheduler.FlowAccount { return scope.account(lookup(id)) }
		},
		NeedsChannelCheck: s.service.needsUpstreamChannelRestrictionCheck,
		ChannelRestricted: func(ctx context.Context, id int64, a *scheduler.FlowAccount, model string, compact bool) bool {
			return s.service.isUpstreamRoutingModelRestrictedByChannel(ctx, id, scope.oldAccount(a), model, compact)
		},
		BasicEligible: func(ctx context.Context, a *scheduler.FlowAccount, platform, model string, compact bool, capability OpenAIEndpointCapability) bool {
			return isOpenAICompatibleAccountEligibleForRequest(ctx, scope.oldAccount(a), platform, model, compact, capability)
		},
		BasicFailureReason: func(ctx context.Context, a *scheduler.FlowAccount, platform, model string, compact bool, capability OpenAIEndpointCapability) string {
			return openAICompatibleAccountEligibilityFailureReason(ctx, scope.oldAccount(a), platform, model, compact, capability)
		},
		CompleteAcquired: func(ctx context.Context, a *scheduler.FlowAccount, release func()) (*scheduler.FlowSelection, error) {
			v, err := s.service.newAcquiredSelectionResult(ctx, scope.oldAccount(a), release)
			return scope.selection(v), err
		},
		Complete: func(ctx context.Context, a *scheduler.FlowAccount, acquired bool, release func(), wait *scheduler.AccountWaitPlan) (*scheduler.FlowSelection, error) {
			v, err := s.service.newSelectionResult(ctx, scope.oldAccount(a), acquired, release, wait)
			return scope.selection(v), err
		},
		Available: available, CacheAvailable: available && s.service.cache != nil, SnapshotAvailable: available && s.service.schedulerSnapshot != nil, RecheckAvailable: available && s.service.schedulerSnapshot != nil && s.service.accountRepo != nil,
		Effective: func(ctx context.Context, id *int64) policy.EffectiveSettings {
			return schedulerEffectiveProjection(s.service.advancedSchedulerEffectiveSettingsForRequest(ctx, id))
		},
		GroupRequiresPrivacy: s.service.openAIGroupRequiresPrivacySet,
		PreviousResponse: func(ctx context.Context, id *int64, previous, model string, excluded map[int64]struct{}, capability OpenAIEndpointCapability, compact bool) (*scheduler.FlowSelection, error) {
			v, err := s.service.selectAccountByPreviousResponseIDForCapability(ctx, id, previous, model, excluded, capability, compact)
			return scope.selection(v), err
		},
		RequestCompatible: func(ctx context.Context, a *scheduler.FlowAccount, input scheduler.PlatformSelectionInput) (bool, string) {
			return s.isAccountRequestCompatibleReason(ctx, scope.oldAccount(a), legacyPlatformInput(input))
		},
		TransportCompatible: func(a *scheduler.FlowAccount, transport string) bool {
			return s.isAccountTransportCompatible(scope.oldAccount(a), OpenAIUpstreamTransport(transport))
		},
		HasGroupMetadata: func(a *scheduler.FlowAccount) bool { return hasOpenAIAccountGroupMetadata(scope.oldAccount(a)) },
		MatchesGroup: func(a *scheduler.FlowAccount, id *int64) bool {
			return s.service.openAIAccountMatchesSchedulingGroup(scope.oldAccount(a), id)
		},
		BindSticky: s.service.BindStickySession, DeleteSticky: s.service.deleteStickySessionAccountID, GetSticky: s.service.getStickySessionAccountID, RefreshSticky: s.service.refreshStickySessionTTL, StickyTTL: s.service.openAIWSSessionStickyTTL,
		Options: func() scheduler.FlowOptions {
			v := s.service.schedulingConfig()
			return scheduler.FlowOptions{LoadBatchEnabled: v.LoadBatchEnabled, PreferSoonestReset: v.PreferSoonestReset, FallbackMaxWaiting: v.FallbackMaxWaiting, StickySessionMaxWaiting: v.StickySessionMaxWaiting, FallbackSelectionMode: v.FallbackSelectionMode, FallbackWaitTimeout: v.FallbackWaitTimeout, StickySessionWaitTimeout: v.StickySessionWaitTimeout}
		},
		GetSchedulable: func(ctx context.Context, id int64) (*scheduler.FlowAccount, error) {
			v, err := s.service.getSchedulableAccount(ctx, id)
			return scope.account(v), err
		},
		ClearSticky: func(a *scheduler.FlowAccount, model string) bool {
			return shouldClearStickySession(scope.oldAccount(a), model)
		},
		IsCompatible:  func(a *scheduler.FlowAccount) bool { return scope.oldAccount(a).IsOpenAICompatible() },
		IsSchedulable: func(a *scheduler.FlowAccount) bool { return scope.oldAccount(a).IsSchedulable() },
		Recheck: func(ctx context.Context, a *scheduler.FlowAccount, id *int64, platform, model string, compact bool, capability OpenAIEndpointCapability) *scheduler.FlowAccount {
			return scope.account(s.service.recheckSelectedOpenAIAccountFromDB(ctx, scope.oldAccount(a), id, platform, model, compact, capability))
		},
		Fresh: func(ctx context.Context, a *scheduler.FlowAccount, platform, model string, compact bool, capability OpenAIEndpointCapability) *scheduler.FlowAccount {
			return scope.account(s.service.resolveFreshSchedulableOpenAIAccount(ctx, scope.oldAccount(a), platform, model, compact, capability))
		},
		FreeQuota: func(ctx context.Context, values []scheduler.FlowAccount) []scheduler.FlowAccount {
			return scope.values(s.filterGrokFreeQuotaAccounts(ctx, scope.oldValues(values)))
		},
		CanonicalModel: func(a *scheduler.FlowAccount, model string) string {
			return canonicalOpenAIAccountSchedulingModel(scope.oldAccount(a), model)
		},
		TeamLimited: func(a *scheduler.FlowAccount, model string, now time.Time) bool {
			return isGrokTeamModelRateLimited(scope.oldAccount(a), model, now)
		},
		ModelQuotaBlocked: isGrokModelQuotaBlocked, Acquire: s.service.tryAcquireAccountSlot,
		ListCandidates: func(ctx context.Context, id *int64, platform string) ([]scheduler.FlowAccount, error) {
			v, err := s.service.listSchedulableAccounts(ctx, id, platform)
			return scope.values(v), err
		},
		RuntimeBlocked: func(a *scheduler.FlowAccount, model string) bool {
			return s.service.isOpenAIAccountRequestRuntimeBlocked(scope.oldAccount(a), model)
		},
		FilterTeamLimited: func(values []scheduler.FlowAccount, model string, now time.Time) []scheduler.FlowAccount {
			return scope.values(filterGrokTeamModelRateLimitedAccounts(scope.oldValues(values), model, now))
		},
		FilterModelQuota: func(values []scheduler.FlowAccount, model string, now time.Time) []scheduler.FlowAccount {
			return scope.values(filterGrokModelQuotaBlockedAccounts(scope.oldValues(values), model, now))
		},
		CompactAllowed: func(a *scheduler.FlowAccount) bool { return allowsOpenAICompatibleCompact(scope.oldAccount(a)) },
		IsSubscription: func(a *scheduler.FlowAccount) bool { return scope.oldAccount(a).IsOpenAIChatGPTSubscription() },
		QuotaHeadroom: func(a *scheduler.ScoreAccount, now time.Time) float64 {
			return openAIQuotaHeadroomFactor(scope.accounts[a.ProjectionID], now)
		},
		Unavailable: func(ctx context.Context, requested, model string, compact bool, details string, collections ...[]scheduler.FlowAccount) error {
			converted := make([][]Account, len(collections))
			for i, v := range collections {
				converted[i] = scope.oldValues(v)
			}
			return noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx, requested, model, compact, details, converted...)
		},
	}
	if available && s.service.accountRepo != nil {
		ports.ReadAccountDB = func(ctx context.Context, id int64) (*scheduler.FlowAccount, error) {
			v, err := s.service.accountRepo.GetByID(ctx, id)
			return scope.account(v), err
		}
	}
	var concurrency *scheduler.ConcurrencyService
	if available {
		concurrency = s.service.concurrencyService
	}
	return scheduler.NewPlatformSelector(ports, concurrency, schedulerStats(s.stats), &s.metrics.PlatformMetrics, diagnostics, time.Now), scope
}
func (scope *genericSelectionScope) platformScores(values []openAIAccountCandidateScore) []scheduler.PlatformCandidateScore {
	if values == nil {
		return nil
	}
	out := make([]scheduler.PlatformCandidateScore, len(values))
	for i, v := range values {
		out[i] = scheduler.PlatformCandidateScore{Account: scope.account(v.account), LoadInfo: v.loadInfo, LoadKnown: v.loadKnown, Score: v.score, BaseScore: v.baseScore, StickyBonus: v.stickyBonus, PreviousBonus: v.previousBonus, SessionStickyBonus: v.sessionStickyBonus, Priority: v.priority, ErrorRate: v.errorRate, TTFT: v.ttft, HasTTFT: v.hasTTFT, HasFeedback: v.hasFeedback, Feedback: v.feedback, Factors: v.factors}
	}
	return out
}
func (scope *genericSelectionScope) legacyPlatformScores(values []scheduler.PlatformCandidateScore) []openAIAccountCandidateScore {
	if values == nil {
		return nil
	}
	out := make([]openAIAccountCandidateScore, len(values))
	for i, v := range values {
		out[i] = openAIAccountCandidateScore{account: scope.oldAccount(v.Account), loadInfo: v.LoadInfo, loadKnown: v.LoadKnown, score: v.Score, baseScore: v.BaseScore, stickyBonus: v.StickyBonus, previousBonus: v.PreviousBonus, sessionStickyBonus: v.SessionStickyBonus, priority: v.Priority, errorRate: v.ErrorRate, ttft: v.TTFT, hasTTFT: v.HasTTFT, hasFeedback: v.HasFeedback, feedback: v.Feedback, factors: v.Factors}
	}
	return out
}
