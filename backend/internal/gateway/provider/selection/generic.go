package selection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// generic.go selects gateway providers, validates hydrated records, and
// records scheduling outcomes and rejection evidence.
// A gateway request selects providers from its admitted group after client,
// protocol, model, quota, and runtime eligibility checks. Group mapping
// produces the model the provider layer resolves, while forced-platform input
// narrows that same group's pool. Generic scheduler callbacks choose either
// slot-free targets or a result carrying acquired resources and a bounded wait
// plan.
//
// Full provider hydration can change membership or model support after a
// lightweight snapshot was ranked. The selection request retains the group and
// client model for that later validation, and preparation failure releases a
// registered attempt's resources. Advanced results also capture feedback
// settings for the request, while failure statistics distinguish the first
// observed rejection category from a temporarily exhausted pool.

// selectionFailureStats counts the first diagnostic rejection for each provider
// and retains bounded examples.
type selectionFailureStats struct {
	// Total is partitioned by Eligible, Excluded, Unschedulable, PlatformFiltered,
	// ModelUnsupported, and ModelRateLimited: each provider contributes its first diagnostic outcome to exactly one count.
	Total            int // supplied candidate count
	Eligible         int // passing diagnostic count
	Excluded         int // excluded identifier count
	Unschedulable    int // unavailable record count
	PlatformFiltered int // forced-platform mismatch count
	ModelUnsupported int // rejected model count
	ModelRateLimited int // model cooldown count
	// SamplePlatformIDs, SampleMappingIDs, and SampleRateLimitIDs retain at most
	// five examples per category while every rejected provider still contributes
	// to its count. Cooldown examples also include the formatted remaining time.
	SamplePlatformIDs  []int64  // platform mismatch examples
	SampleMappingIDs   []int64  // unsupported model examples
	SampleRateLimitIDs []string // provider and cooldown examples
}

// selectionFailureDiagnosis identifies one provider's earliest diagnostic
// rejection and optional explanatory detail.
type selectionFailureDiagnosis struct {
	Category string // first diagnostic outcome
	Detail   string // optional outcome explanation
}

// SelectProviderForModel chooses a target for the requested model using the
// generic scheduler's slot-free mode. The group is validated against admission
// state, and session affinity can influence which eligible target is returned.
func (s *Generic) SelectProviderForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionProvider, error) {
	return s.SelectProviderForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectProviderForModelWithExclusions selects an eligible target outside the
// caller's excluded ID set. It captures the group and client model for
// hydration, while the scheduler's SelectOnly mode leaves generation slots
// unoccupied.
func (s *Generic) SelectProviderForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionProvider, error) {
	ctx = withSelectionRequest(ctx, groupID, requestedModel)
	ctx, capabilities := withCapabilitySelection(ctx)
	core, scope := s.genericSelector()
	selected, err := core.SelectOnly(ctx, schedulercore.SelectionInput{GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	if err != nil {
		if local := capabilities.failure(); local != nil {
			err = local
		}
	}
	return scope.oldProvider(selected), err
}

// SelectProviderWithLoadAwareness returns the selected target with acquired
// resources or a wait plan for the admitted group. Callers await capacity
// before forwarding and release acquired resources after the attempt.
// Excluded provider IDs apply to this selection without changing group policy.
// @project-doc docs/architecture/gateway_request_lifecycle.md#account_selection_and_failover
func (s *Generic) SelectProviderWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.SelectionResult, error) {
	ctx = withSelectionRequest(ctx, groupID, requestedModel)
	ctx, capabilities := withCapabilitySelection(ctx)
	core, scope := s.genericSelector()
	plan, _ := requeststate.RoutePlanFromContext(ctx)
	result, err := core.Select(ctx, schedulercore.SelectionInput{RoutePlan: plan, GroupID: groupID, SessionHash: sessionHash, RequestedModel: requestedModel, ExcludedIDs: excludedIDs})
	if err != nil {
		if local := capabilities.failure(); local != nil {
			err = local
		}
	}
	return scope.restore(result), err
}

// ReportAdvancedProviderScheduleResult records success and optional TTFT
// latency for an advanced result with a positive provider ID. Captured alpha
// settings take precedence so feedback follows the policy used during that
// request's selection.
func (s *Generic) ReportAdvancedProviderScheduleResult(selection *gatewayprovider.SelectionResult, providerID int64, success bool, result *forwardcore.MessagesResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler || providerID <= 0 {
		return
	}
	var ttftMs *int
	if result != nil {
		ttftMs = result.Timings.TTFTMs()
	}
	if selection.AdvancedSchedulerFeedback != nil {
		s.advancedSchedulerStats().Report(providerID, success, ttftMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.advancedSchedulerStats().Report(providerID, success, ttftMs)
}

// RecordAdvancedProviderSwitch increments switch feedback for an advanced
// selection result. Nil selectors, missing results, and basic selections are
// ignored so basic requests do not contribute to advanced scheduling
// observations.
func (s *Generic) RecordAdvancedProviderSwitch(selection *gatewayprovider.SelectionResult) {
	if s == nil || selection == nil || !selection.AdvancedScheduler {
		return
	}
	s.advancedSchedulerStats().ReportSwitch()
}

// advancedSchedulerStats returns the bound feedback store, creating a local
// store when absent. A nil selector returns nil; callers using this lazy path
// arrange initialization before concurrent access because publication here is
// unsynchronized.
func (s *Generic) advancedSchedulerStats() *schedulercore.RuntimeStats {
	if s == nil {
		return nil
	}
	if s.advancedProviderStats == nil {
		s.advancedProviderStats = schedulercore.NewRuntimeStats(time.Now)
	}
	return s.advancedProviderStats
}

// advancedSchedulerEffectiveSettingsForRequest resolves final-group overrides
// through the selector's Parameters instance. Nil context uses Background, and
// unavailable or basic groups provide empty overrides so effective settings
// come from process and runtime values.
func (s *Generic) advancedSchedulerEffectiveSettingsForRequest(ctx context.Context, id *int64) policy.EffectiveSettings {
	if ctx == nil {
		ctx = context.Background()
	}
	group := schedulerRequestGroup(ctx, id, s.schedulerSnapshot != nil, s.readSchedulingGroup)
	return s.schedulerParameters.Effective(ctx, schedulerGroupOverrides(group))
}

// schedulingConfig returns the generic selector's startup selection and wait
// policy value.
func (s *Generic) schedulingConfig() schedulercore.FlowOptions {
	return s.options.Scheduling
}

// withGroupContext stores a valid group in request state unless an already
// valid group with the same ID is present. Invalid incoming groups leave
// context intact, while replacement uses requeststate's group snapshot copying.
func (s *Generic) withGroupContext(ctx context.Context, group *routing.Group) context.Context {
	if !routing.IsGroupContextValid(group) {
		return ctx
	}
	if existing, ok := requeststate.GroupFromContext(ctx); ok && existing != nil && existing.ID == group.ID && routing.IsGroupContextValid(existing) {
		return ctx
	}
	return requeststate.WithGroup(ctx, group)
}

// groupFromContext returns a valid request group matching the supplied ID.
// Missing, incomplete, or differently identified groups return nil so
// resolveGroupByID can obtain the requested group's lightweight data.
func (s *Generic) groupFromContext(ctx context.Context, groupID int64) *routing.Group {
	if group, ok := requeststate.GroupFromContext(ctx); ok && routing.IsGroupContextValid(group) && group.ID == groupID {
		return group
	}
	return nil
}

// resolveGroupByID reuses a matching valid request group or reads its
// lightweight repository record. Callers supply a group reader when context
// cannot satisfy the lookup; read errors retain their cause through wrapping.
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

// routingProviderIDsForRequest resolves a client's single group mapping before
// looking up model-preferred providers. Missing inputs or unsuccessful group
// reads return nil so selection can use the normal pool; optional diagnostics
// record that read failure.
func (s *Generic) routingProviderIDsForRequest(ctx context.Context, groupID *int64, requestedModel string, platform string) []int64 {
	if groupID == nil || requestedModel == "" {
		return nil
	}
	// Model routing preferences are optional. An unavailable group read leaves
	// the request on normal pool selection and can be reported by routing diagnostics.
	group, err := s.resolveGroupByID(ctx, *groupID)
	if err != nil || group == nil {
		if s.debugModelRoutingEnabled() {
			logging.LegacyPrintf("service.gateway", "[ModelRoutingDebug] resolve group failed: group_id=%v model=%s platform=%s err=%v", derefGroupID(groupID), requestedModel, platform, err)
		}
		return nil
	}

	routingModel := s.groupMappedModelForGroup(ctx, groupID, requestedModel)
	ids := group.GetRoutingProviderIDs(routingModel)
	if s.debugModelRoutingEnabled() {
		logging.LegacyPrintf("service.gateway", "[ModelRoutingDebug] routing lookup: group_id=%d model=%s enabled=%v rules=%d matched_ids=%v",
			group.ID, requestedModel, group.ModelRoutingEnabled, len(group.ModelRouting), ids)
	}
	return ids
}

// resolveGatewayGroup validates the group's request-plan and client
// restrictions before candidate reads. It returns the admitted ID unchanged
// with the resolved group; entry admission prepares any authorized fallback
// before this call.
func (s *Generic) resolveGatewayGroup(ctx context.Context, groupID *int64) (*routing.Group, *int64, error) {
	group, err := currentSelectionGroup(ctx, groupID, s.resolveGroupByID)
	if err != nil {
		return nil, nil, err
	}
	return group, groupID, nil
}

// checkClaudeCodeRestriction delegates client validation for the admitted group
// and returns its unchanged ID. Nil group input passes through, while
// restriction or identity errors prevent selection from reading the group's
// pool.
func (s *Generic) checkClaudeCodeRestriction(ctx context.Context, groupID *int64) (*routing.Group, *int64, error) {
	if groupID == nil {
		return nil, groupID, nil
	}

	group, resolvedID, err := s.resolveGatewayGroup(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}

	return group, resolvedID, nil
}

// resolvePlatform returns a nonempty forced platform from authenticated request
// context when available. Otherwise it returns no platform filter; group and
// groupID are compatibility inputs and do not introduce another platform
// preference.
func (s *Generic) resolvePlatform(ctx context.Context, groupID *int64, group *routing.Group) (string, bool, error) {
	if platform, forced := apikey.ForcePlatformFromContext(ctx); forced && platform != "" {
		return platform, true, nil
	}
	return "", false, nil
}

// listSchedulableProviders reads candidates for a positive group through
// snapshots or the configured repository. Empty platform selects the known
// platform set, then threshold and Grok quota gates filter results; the mixed
// flag returned here is false.
func (s *Generic) listSchedulableProviders(ctx context.Context, groupID *int64, platform string, hasForcePlatform bool) ([]gatewayprovider.ExecutionProvider, bool, error) {
	if groupID == nil || *groupID <= 0 {
		return nil, false, nil
	}
	var providers []gatewayprovider.ExecutionProvider
	var err error
	if s.schedulerSnapshot != nil {
		providers, _, err = readSnapshotProviders(ctx, s.schedulerSnapshot, groupID, platform, hasForcePlatform)
	} else if platform == "" {
		providers, err = s.providerRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, capability.ProviderPlatforms())
	} else {
		providers, err = s.providerRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	if err != nil {
		return nil, false, err
	}
	providers = s.filterProvidersBySchedulingThreshold(ctx, providers)
	providers = s.filterGrokFreeQuotaProvidersForGateway(ctx, providers)
	return providers, false, nil
}

// IsSingleAntigravityProviderGroup reports whether the filtered Antigravity
// list contains one candidate. Read failures return false; handlers use a true
// result to enable single-provider retry before capacity-error cooldown
// handling.
func (s *Generic) IsSingleAntigravityProviderGroup(ctx context.Context, groupID *int64) bool {
	providers, _, err := s.listSchedulableProviders(ctx, groupID, capability.PlatformAntigravity, true)
	// An unavailable list cannot establish single-provider retry eligibility. Let
	// the handler use ordinary capacity-error handling when this query fails.
	if err != nil {
		return false
	}
	return len(providers) == 1
}

// isProviderAllowedForPlatform reports a nonnil target matching the optional
// forced platform. An empty platform admits every platform, and useMixed is
// retained as a compatibility input without changing this check.
func (s *Generic) isProviderAllowedForPlatform(provider *gatewayprovider.ExecutionProvider, platform string, useMixed bool) bool {
	return provider != nil && (platform == "" || provider.Record.Platform == platform)
}

// isProviderSchedulableForSelection delegates current status and scheduling
// eligibility to the target's record view. A nil target returns false, letting
// candidate filtering treat a missing record as unavailable.
func (s *Generic) isProviderSchedulableForSelection(provider *gatewayprovider.ExecutionProvider) bool {
	if provider == nil {
		return false
	}
	return provider.View().IsSchedulable()
}

// isProviderSchedulableForModelSelection checks the group-mapped model through
// the provider's scheduling policy. Nil providers return false, and the
// provider policy resolves applicable protocol and model cooldown rules from
// request state.
func (s *Generic) isProviderSchedulableForModelSelection(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	if provider == nil {
		return false
	}
	routingModel := s.groupMappedModelForProviderLayer(ctx, requestedModel)
	return gatewayprovider.ExecutionModelPolicy(provider).Schedulable(ctx, routingModel)
}

// shouldClearStickySessionForProviderLayer checks binding invalidation using
// the request's group-mapped model. The shared helper combines provider
// schedulability and active cooldown, while a missing target remains the
// caller's separate cleanup case.
func (s *Generic) shouldClearStickySessionForProviderLayer(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	routingModel := s.groupMappedModelForProviderLayer(ctx, requestedModel)
	return shouldClearStickySession(ctx, provider, routingModel)
}

// isProviderEligibleExceptModelSupport reports whether a candidate passes gates
// used before unsupported-model diagnosis. Protocol, exclusions, status,
// platform, privacy, quota, window-cost, RPM, and final upstream group
// restrictions all contribute.
//
// Window-cost and RPM use ordinary candidate allowances here, even when the
// request has a sticky binding. Model support is checked by the caller
// afterward, letting diagnosis distinguish an otherwise eligible pool that
// lacks the model from a pool already excluded for another eligibility reason.
func (s *Generic) isProviderEligibleExceptModelSupport(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) bool {
	if !gatewayprovider.ExecutionModelPolicy(provider).AllowsProtocol(ctx, "") {
		return false
	}
	if provider == nil {
		return false
	}
	if excludedIDs != nil {
		if _, excluded := excludedIDs[provider.Record.ID]; excluded {
			return false
		}
	}
	if !s.isProviderSchedulableForSelection(provider) || !s.isProviderAllowedForPlatform(provider, platform, useMixed) {
		return false
	}
	if schedGroup != nil && schedGroup.RequirePrivacySet && !provider.View().IsPrivacySet() {
		return false
	}
	if !s.isProviderSchedulableForQuota(provider) || !s.isProviderSchedulableForWindowCost(ctx, provider, false) || !s.isProviderSchedulableForRPM(ctx, provider, false) {
		return false
	}
	if groupID != nil && s.needsUpstreamGroupRestrictionCheck(ctx, groupID) &&
		s.isUpstreamModelRestrictedByGroup(ctx, *groupID, provider, requestedModel) {
		return false
	}
	return true
}

// shouldUseGroupModelUnsupportedError reports whether every otherwise eligible
// candidate rejects a nonblank requested model. Blank requests and empty pools
// return false, and candidates excluded by other admission gates do not
// establish model support.
//
// At least one relevant candidate must remain before the typed model rejection
// is appropriate. Finding any supported candidate returns false even if later
// scheduling cannot select it, allowing the caller to report temporary capacity
// or runtime unavailability instead of claiming that the group's configured
// model is absent.
func (s *Generic) shouldUseGroupModelUnsupportedError(ctx context.Context, providers []gatewayprovider.ExecutionProvider, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(providers) == 0 {
		return false
	}
	hasRelevantProvider := false
	for i := range providers {
		acc := &providers[i]
		if !s.isProviderEligibleExceptModelSupport(ctx, acc, requestedModel, platform, excludedIDs, useMixed, groupID, schedGroup) {
			continue
		}
		hasRelevantProvider = true
		if s.isModelSupportedByProviderWithContext(ctx, acc, requestedModel) {
			return false
		}
	}
	return hasRelevantProvider
}

// groupModelUnsupportedErrorIfApplicable returns routing's typed rejection when
// the relevant pool lacks the requested model. Otherwise it returns nil,
// leaving the caller to produce its ordinary no-provider or capacity error.
func (s *Generic) groupModelUnsupportedErrorIfApplicable(ctx context.Context, providers []gatewayprovider.ExecutionProvider, requestedModel string, platform string, excludedIDs map[int64]struct{}, useMixed bool, groupID *int64, schedGroup *routing.Group) error {
	if s.shouldUseGroupModelUnsupportedError(ctx, providers, requestedModel, platform, excludedIDs, useMixed, groupID, schedGroup) {
		if err := routing.NewGroupModelRejection(platform, requestedModel, modelRejectionSources(providers)); err != nil {
			return err
		}
	}
	return nil
}

// isProviderInGroup reports membership in the requested positive group using
// either stored membership representation. Nil providers and nil or nonpositive
// IDs return false, so ungrouped inputs cannot admit a provider.
func (s *Generic) isProviderInGroup(provider *gatewayprovider.ExecutionProvider, groupID *int64) bool {
	return openAIStickyProviderMatchesGroup(provider, groupID)
}

// tryAcquireProviderSlot acquires generation capacity through the bound
// concurrency service. Select-only context or an absent service returns
// successful acquisition with a no-op release; callers invoke the returned
// release after use.
func (s *Generic) tryAcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int) (*schedulercore.AcquireResult, error) {
	if schedulercore.IsSelectOnly(ctx) {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireProviderSlot(ctx, providerID, maxConcurrency)
}

// withWindowCostPrefetch delegates batch cost-window observations for the
// candidate pool into request context. Nil context, empty candidates, or a
// disabled batch capability passes context through; later gates can reuse the
// returned observations.
func (s *Generic) withWindowCostPrefetch(ctx context.Context, providers []gatewayprovider.ExecutionProvider) context.Context {
	if ctx == nil || len(providers) == 0 || !s.windowPrefetchAvailable {
		return ctx
	}
	values := make([]billing.CostWindowInput, len(providers))
	for i := range providers {
		values[i] = costWindowInput(&providers[i])
	}
	return s.windowCostGuard().Prefetch(ctx, values)
}

// isProviderSchedulableForQuota checks configured quota exhaustion for API Key
// and Bedrock records. Other provider types pass this gate, and callers supply
// a nonnil execution target before invoking its record view.
func (s *Generic) isProviderSchedulableForQuota(provider *gatewayprovider.ExecutionProvider) bool {
	if !provider.View().IsAPIKeyOrBedrock() {
		return true
	}
	return !provider.View().IsQuotaExceeded()
}

// isProviderSchedulableForWindowCost delegates Anthropic cost-window
// eligibility to the bound billing guard. The sticky flag permits the guard's
// reserved allowance, while the converted record supplies its current bounds
// and configured limit.
func (s *Generic) isProviderSchedulableForWindowCost(ctx context.Context, provider *gatewayprovider.ExecutionProvider, sticky bool) bool {
	return s.windowCostGuard().Allow(ctx, costWindowInput(provider), sticky)
}

// withRPMPrefetch delegates RPM batching for Anthropic OAuth and Setup Token
// candidates with positive base limits. An absent RPM cache keeps context
// unchanged; later per-provider checks can reuse the resulting request
// observations.
func (s *Generic) withRPMPrefetch(ctx context.Context, providers []gatewayprovider.ExecutionProvider) context.Context {
	if s.rpmCache == nil {
		return ctx
	}

	var ids []int64
	for i := range providers {
		if providers[i].View().IsAnthropicOAuthOrSetupToken() && gatewayprovider.ExecutionRuntimeConfig(&providers[i]).GetBaseRPM() > 0 {
			ids = append(ids, providers[i].Record.ID)
		}
	}
	return schedulercore.PrefetchRPM(ctx, s.rpmCache, ids)
}

// isProviderSchedulableForRPM delegates Anthropic OAuth and Setup Token minute
// limits with the provider's configured strategy. Other types pass, and sticky
// controls the scheduler's configured buffer allowance for an existing session.
func (s *Generic) isProviderSchedulableForRPM(ctx context.Context, provider *gatewayprovider.ExecutionProvider, sticky bool) bool {
	if !provider.View().IsAnthropicOAuthOrSetupToken() {
		return true
	}
	return schedulercore.AllowProviderRPM(ctx, s.rpmCache, schedulercore.ProviderRPMInput{ID: provider.Record.ID, Enabled: true, Base: gatewayprovider.ExecutionRuntimeConfig(provider).GetBaseRPM(), Buffer: gatewayprovider.ExecutionRuntimeConfig(provider).GetRPMStickyBuffer(), Strategy: gatewayprovider.ExecutionRuntimeConfig(provider).GetRPMStrategy()}, sticky)
}

// IncrementProviderRPM records the provider's dispatched request in the shared
// minute counter. Eligibility is read separately before this increment, so
// concurrent requests can exceed the configured soft limit before their
// increments become visible.
func (s *Generic) IncrementProviderRPM(ctx context.Context, id int64) error {
	return schedulercore.IncrementProviderRPM(ctx, s.rpmCache, id)
}

// checkAndRegisterSession delegates Anthropic OAuth or Setup Token session
// admission using the supplied sticky hash. Existing sessions and new sessions
// within capacity pass; select-only context skips registration so auxiliary
// choices consume no session capacity.
func (s *Generic) checkAndRegisterSession(ctx context.Context, provider *gatewayprovider.ExecutionProvider, session string) bool {
	if schedulercore.IsSelectOnly(ctx) {
		return true
	}
	return schedulercore.RegisterSession(ctx, s.sessionLimitCache, schedulerSessionBinding(provider, session))
}

// ReleaseProviderSession delegates unregistering a failed attempt's applicable
// Anthropic session before its idle expiry. A nil selector is ignored, and the
// cache removal tolerates repeated calls while failures are logged by
// scheduler.FinishSession.
func (s *Generic) ReleaseProviderSession(ctx context.Context, provider *gatewayprovider.ExecutionProvider, session string) {
	if s == nil {
		return
	}
	schedulercore.FinishSession(ctx, s.sessionLimitCache, schedulerSessionBinding(provider, session), schedulercore.AttemptOutcome{}, schedulercore.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
}

// schedulerSessionBinding translates a provider's Anthropic session
// configuration for scheduler registration and cleanup. Nil targets return
// empty bindings; idle timeout minutes become a duration and the supplied
// session remains the sticky hash.
func schedulerSessionBinding(provider *gatewayprovider.ExecutionProvider, session string) schedulercore.SessionBinding {
	if provider == nil {
		return schedulercore.SessionBinding{}
	}
	// Provider settings store idle lifetime in minutes; scheduler registration
	// consumes a duration for applying the same expiry to this session binding.
	return schedulercore.SessionBinding{ProviderID: provider.Record.ID, SessionID: session, Enabled: provider.View().IsAnthropicOAuthOrSetupToken(), Limit: gatewayprovider.ExecutionRuntimeConfig(provider).GetMaxSessions(), IdleTimeout: time.Duration(gatewayprovider.ExecutionRuntimeConfig(provider).GetSessionIdleTimeoutMinutes()) * time.Minute}
}

// getSchedulableProvider reads a full target from snapshots or the repository.
// Read errors pass through, while scheduling-threshold rejection and a blocked
// Grok Free OAuth gate return nil without error for candidate selection.
func (s *Generic) getSchedulableProvider(ctx context.Context, providerID int64) (*gatewayprovider.ExecutionProvider, error) {
	var (
		provider *gatewayprovider.ExecutionProvider
		err      error
	)
	if s.schedulerSnapshot != nil {
		provider, err = readSnapshotProvider(ctx, s.schedulerSnapshot, providerID)
	} else {
		provider, err = s.providerRepo.GetByID(ctx, providerID)
	}
	if err != nil || provider == nil {
		return provider, err
	}
	if s.isProviderBlockedBySchedulingThreshold(ctx, provider) {
		return nil, nil
	}

	if provider.View().IsGrok() {
		if gated := s.filterGrokFreeQuotaProvidersForGateway(ctx, []gatewayprovider.ExecutionProvider{*provider}); len(gated) == 0 {
			return nil, nil
		}
	}
	return provider, nil
}

// filterProvidersBySchedulingThreshold builds an ordered slice of candidates
// passing the current health threshold. Empty input is returned unchanged;
// nonempty results receive slice storage while provider record references
// retain their existing ownership.
func (s *Generic) filterProvidersBySchedulingThreshold(ctx context.Context, providers []gatewayprovider.ExecutionProvider) []gatewayprovider.ExecutionProvider {
	if len(providers) == 0 {
		return providers
	}

	filtered := make([]gatewayprovider.ExecutionProvider, 0, len(providers))
	for i := range providers {
		if s.isProviderBlockedBySchedulingThreshold(ctx, &providers[i]) {
			continue
		}
		filtered = append(filtered, providers[i])
	}
	return filtered
}

// isProviderBlockedBySchedulingThreshold delegates the target's
// scheduling-threshold observation to the health adapter. Missing selector,
// observer, or target returns false; the adapter may update the execution
// record with the observed pause.
func (s *Generic) isProviderBlockedBySchedulingThreshold(ctx context.Context, provider *gatewayprovider.ExecutionProvider) bool {
	if s == nil || s.healthObserver == nil || provider == nil {
		return false
	}
	return gatewayprovider.ApplyExecutionSchedulingThreshold(ctx, s.healthObserver, provider)
}

// hydrateSelectedProvider obtains the full target after selecting from a
// scheduling snapshot. Nil targets or an absent snapshot return the input;
// otherwise the repository is preferred and missing records return
// ErrNoAvailableProviders.
//
// When selectionRequest was captured, hydration checks the final request group
// and the client's group-mapped model against the later record. Membership,
// scheduling, and support are reevaluated together, so a provider moved or
// reconfigured since candidate ranking cannot become an execution target merely
// because the lightweight record passed.
func (s *Generic) hydrateSelectedProvider(ctx context.Context, provider *gatewayprovider.ExecutionProvider) (*gatewayprovider.ExecutionProvider, error) {
	if provider == nil || s.schedulerSnapshot == nil {
		return provider, nil
	}
	var hydrated *gatewayprovider.ExecutionProvider
	var err error
	if s.providerRepo != nil {
		hydrated, err = s.providerRepo.GetByID(ctx, provider.Record.ID)
	} else {
		hydrated, err = readSnapshotProvider(ctx, s.schedulerSnapshot, provider.Record.ID)
	}
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, schedulercore.ErrNoAvailableProviders
	}
	if input, ok := ctx.Value(selectionRequestKey{}).(selectionRequest); ok {
		groupID := input.groupID
		if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
			groupID = &group.ID
		}
		model := s.groupMappedModelForGroup(ctx, groupID, input.model)
		policy := gatewayprovider.ExecutionModelPolicy(hydrated)
		if !openAIStickyProviderMatchesGroup(hydrated, groupID) || !policy.Schedulable(ctx, model) || !policy.Supports(ctx, model) {
			return nil, schedulercore.ErrNoAvailableProviders
		}
	}
	return hydrated, nil
}

// newSelectionResult prepares a hydrated gateway result and registers the
// release handle with an attempt lease. Callers release acquired resources
// after their attempt or use the wait plan to await capacity before forwarding.
//
// Hydration failure invokes the wrapped release before returning the error,
// preventing a selected lightweight target from leaking capacity during
// preparation. Valid advanced-group context also captures the request's
// feedback alphas in the result. The lease owns release idempotence, while the
// result records the provider and waiting decision for the execution caller.
func (s *Generic) newSelectionResult(ctx context.Context, provider *gatewayprovider.ExecutionProvider, acquired bool, release func(), waitPlan *schedulercore.ProviderWaitPlan) (*gatewayprovider.SelectionResult, error) {
	attempt := schedulercore.NewAttemptLease(schedulercore.RequestLease(ctx), nil, release)
	if release != nil {
		release = attempt.Release
	}

	hydrated, err := s.hydrateSelectedProvider(ctx, provider)
	if err != nil {
		// The attempt already owns its release handle. Return that capacity before
		// exposing hydration failure so preparation cannot leave a provider slot occupied.
		if release != nil {
			release()
		}
		return nil, err
	}
	selection := &gatewayprovider.SelectionResult{
		Provider:    hydrated,
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

// logDetailedSelectionFailure collects first-rejection counts and bounded
// sample identifiers, then emits a gateway diagnostic. Session hashes are
// shortened for the log, and the returned statistics also feed the concise
// no-provider summary.
func (s *Generic) logDetailedSelectionFailure(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	platform string,
	providers []gatewayprovider.ExecutionProvider,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureStats {
	stats := s.collectSelectionFailureStats(ctx, providers, requestedModel, platform, excludedIDs, allowMixedScheduling)
	logging.LegacyPrintf(
		"service.gateway",
		"[SelectProviderDetailed] group_id=%v model=%s platform=%s session=%s total=%d eligible=%d excluded=%d unschedulable=%d platform_filtered=%d model_unsupported=%d model_rate_limited=%d sample_platform_filtered=%v sample_model_unsupported=%v sample_model_rate_limited=%v",
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

// collectSelectionFailureStats counts the earliest diagnostic category for
// every supplied provider. Each candidate contributes to exactly one outcome,
// and platform, unsupported-model, and cooldown categories retain at most five
// examples.
//
// Diagnosis order gives exclusions and provider state precedence over platform
// or model details. Cooldown examples resolve the group-mapped model and
// truncate remaining durations to seconds for logs; this observational summary
// does not reproduce every quota, privacy, or slot-acquisition gate that actual
// selection performs.
func (s *Generic) collectSelectionFailureStats(
	ctx context.Context,
	providers []gatewayprovider.ExecutionProvider,
	requestedModel string,
	platform string,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureStats {
	stats := selectionFailureStats{
		Total: len(providers),
	}

	for i := range providers {
		acc := &providers[i]
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
			routingModel := s.groupMappedModelForProviderLayer(ctx, requestedModel)
			// Show whole seconds in bounded diagnostic samples; the scheduling
			// policy uses the untruncated duration to decide model eligibility.
			remaining := gatewayprovider.ExecutionModelPolicy(acc).LimitRemaining(ctx, routingModel).Truncate(time.Second)
			stats.SampleRateLimitIDs = appendSelectionFailureRateSample(stats.SampleRateLimitIDs, acc.Record.ID, remaining)
		default:
			stats.Eligible++
		}
	}

	return stats
}

// diagnoseSelectionFailure returns a provider's first observed diagnostic
// rejection and optional detail. It checks missing records, exclusions,
// provider schedulability, forced platform, model support, and model scheduling
// in that order.
//
// This order makes each provider belong to one statistics bucket even when
// several conditions fail together. Model cooldown details use the group-mapped
// identity and a whole-second remaining duration. A provider reaching the end
// is marked eligible for these diagnostic checks, with selection's other quota
// and capacity gates evaluated elsewhere.
func (s *Generic) diagnoseSelectionFailure(
	ctx context.Context,
	acc *gatewayprovider.ExecutionProvider,
	requestedModel string,
	platform string,
	excludedIDs map[int64]struct{},
	allowMixedScheduling bool,
) selectionFailureDiagnosis {
	if acc == nil {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "provider_nil"}
	}
	if _, excluded := excludedIDs[acc.Record.ID]; excluded {
		return selectionFailureDiagnosis{Category: "excluded"}
	}
	if !s.isProviderSchedulableForSelection(acc) {
		return selectionFailureDiagnosis{Category: "unschedulable", Detail: "generic_unschedulable"}
	}
	if isPlatformFilteredForSelection(acc, platform, allowMixedScheduling) {
		return selectionFailureDiagnosis{
			Category: "platform_filtered",
			Detail:   fmt.Sprintf("provider_platform=%s requested_platform=%s", acc.Record.Platform, strings.TrimSpace(platform)),
		}
	}
	if requestedModel != "" && !s.isModelSupportedByProviderWithContext(ctx, acc, requestedModel) {
		return selectionFailureDiagnosis{
			Category: "model_unsupported",
			Detail:   fmt.Sprintf("model=%s", requestedModel),
		}
	}
	if !s.isProviderSchedulableForModelSelection(ctx, acc, requestedModel) {
		routingModel := s.groupMappedModelForProviderLayer(ctx, requestedModel)
		// Whole-second cooldown detail is for diagnostics; model eligibility uses
		// the untruncated duration in its separate scheduling-policy check.
		remaining := gatewayprovider.ExecutionModelPolicy(acc).LimitRemaining(ctx, routingModel).Truncate(time.Second)
		return selectionFailureDiagnosis{
			Category: "model_rate_limited",
			Detail:   fmt.Sprintf("remaining=%s", remaining),
		}
	}
	return selectionFailureDiagnosis{Category: "eligible"}
}

// isPlatformFilteredForSelection reports a missing target or a mismatch with
// the nonempty forced platform. An empty platform admits every target platform,
// and allowMixedScheduling is retained as an unused compatibility input.
func isPlatformFilteredForSelection(acc *gatewayprovider.ExecutionProvider, platform string, allowMixedScheduling bool) bool {
	return acc == nil || platform != "" && acc.Record.Platform != platform
}

// appendSelectionFailureSampleID appends a provider identifier while the
// diagnostic sample has fewer than five entries. Longer samples remain
// unchanged, bounding each category's logged examples while all candidate
// counts still accumulate.
func appendSelectionFailureSampleID(samples []int64, id int64) []int64 {
	// Keep logged examples bounded while category counts continue accumulating.
	const limit = 5
	if len(samples) >= limit {
		return samples
	}
	return append(samples, id)
}

// appendSelectionFailureRateSample appends a provider and formatted cooldown
// while the sample has fewer than five entries. Longer samples remain
// unchanged; callers supply the already truncated duration used in diagnostics.
func appendSelectionFailureRateSample(samples []string, providerID int64, remaining time.Duration) []string {
	// Keep logged examples bounded while category counts continue accumulating.
	const limit = 5
	if len(samples) >= limit {
		return samples
	}
	return append(samples, fmt.Sprintf("%d(%s)", providerID, remaining))
}

// summarizeSelectionFailureStats formats the category counts used in concise
// no-provider diagnostic messages.
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

// isModelSupportedByProviderWithContext applies one request group mapping
// before checking the target's model policy. Pass the client name; callers with
// an already group-mapped model use
// isRoutingModelSupportedByProviderWithContext to avoid another mapping.
func (s *Generic) isModelSupportedByProviderWithContext(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requestedModel string) bool {
	routingModel := s.groupMappedModelForProviderLayer(ctx, requestedModel)
	return s.isRoutingModelSupportedByProviderWithContext(ctx, provider, routingModel)
}

// isRoutingModelSupportedByProviderWithContext checks configured model support
// and final output capabilities for an already group-mapped model. Callers supply
// the intermediate identity and request requirements; protocol resolution and
// provider mapping precede attribute lookup for each candidate.
func (s *Generic) isRoutingModelSupportedByProviderWithContext(ctx context.Context, provider *gatewayprovider.ExecutionProvider, routingModel string) bool {
	return gatewayprovider.ExecutionModelPolicy(provider).Supports(ctx, routingModel) && candidateOutputEligibility(ctx, provider, routingModel) == ""
}

// groupMappedModelForProviderLayer maps the client name using a valid request
// group when policy is available. Missing selector, policy, group, or blank
// model retains requestedModel, allowing provider checks to use the available
// identity.
func (s *Generic) groupMappedModelForProviderLayer(ctx context.Context, requestedModel string) string {
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

// NewSessionAttempts constructs the request-owned set used to track and finish
// session attempts. Its cache and diagnostics come from this selector; callers
// retain the set until the request's attempt outcomes are known.
func (s *Generic) NewSessionAttempts() *schedulercore.SessionAttempts {
	return schedulercore.NewSessionAttempts(s.sessionLimitCache, schedulercore.Diagnostics{
		Logf: logging.LegacyPrintf,

		Event: logging.Event,
	},
	)
}

// TrackSessionAttempt adds the provider's applicable session binding to the
// request's attempt set. Callers pass the sticky-session hash and later finish
// the set so failed attempts can release registered session capacity.
func (s *Generic) TrackSessionAttempt(attempts *schedulercore.SessionAttempts, provider *gatewayprovider.ExecutionProvider, session string) {
	attempts.Track(schedulerSessionBinding(provider, session))
}

// costWindowInput converts the target's Anthropic cost-window settings to
// billing guard inputs. Nil input returns an empty disabled value; window time
// pointers remain attached to the record used by that gate.
func costWindowInput(a *gatewayprovider.ExecutionProvider) billing.CostWindowInput {
	if a == nil {
		return billing.CostWindowInput{}
	}
	return billing.CostWindowInput{ID: a.Record.ID, Enabled: a.View().IsAnthropicOAuthOrSetupToken(), Limit: gatewayprovider.ExecutionRuntimeConfig(a).GetWindowCostLimit(), Reserve: gatewayprovider.ExecutionRuntimeConfig(a).GetWindowCostStickyReserve(), Start: a.Record.SessionWindowStart, End: a.Record.SessionWindowEnd}
}

// readSchedulingGroup reads full settings through the configured group
// repository for advanced scheduling. An unavailable reader returns nil without
// error so callers resolving optional overrides can use their global settings.
func (s *Generic) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.groupRepo == nil {
		return nil, nil
	}
	return s.groupRepo.GetByID(ctx, id)
}
