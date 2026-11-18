package selection

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// compatible.go selects compatible providers and rechecks their eligibility
// before returning execution targets or slot plans.
// Compatible selection chooses within the admitted group while retaining both
// the client model and the group-mapped routing model. Slot-free auxiliary
// requests, basic selection, and load-aware selection all reuse platform
// eligibility through scheduler adapters. A no-provider result may instead
// describe a configured model rejection or a Compact capability failure.
//
// Snapshot candidates receive fresh eligibility checks and, when a repository
// is paired with the snapshot, a database recheck before execution. The later
// record supplies membership, privacy, parent health, runtime, and model data.
// Hydration finishes credential preparation for snapshot-only selection;
// failure after an acquired slot invokes the caller's release handle. Sticky
// state and threshold observations stay bound to the application instances.

// openAINoAvailableSelectionError carries compatible diagnostics with the
// scheduler's no-provider error identity.
type openAINoAvailableSelectionError struct {
	message string // pool diagnostic message
}

// BindStickySession writes the compatible group-session binding using
// SessionStickyTTL. Empty sessions, nonpositive provider IDs, and
// guardian-parent preservation requests are ignored by
// BindStickySessionWithTTL, while cache write errors propagate to callers.
func (s *Compatible) BindStickySession(ctx context.Context, groupID *int64, sessionHash string, providerID int64) error {
	return s.BindStickySessionWithTTL(ctx, groupID, sessionHash, providerID, s.SessionStickyTTL())
}

// BindStickySessionWithTTL writes a compatible session binding with the
// supplied lifetime. Empty sessions, nonpositive provider IDs, and
// guardian-parent preservation hints return nil so child review requests leave
// the parent's affinity intact.
func (s *Compatible) BindStickySessionWithTTL(ctx context.Context, groupID *int64, sessionHash string, providerID int64, ttl time.Duration) error {
	if sessionHash == "" || providerID <= 0 || requeststate.PreserveGuardianParentBinding(ctx, sessionHash) {
		return nil
	}
	return s.setStickySessionProviderID(ctx, groupID, sessionHash, providerID, ttl)
}

// SelectProviderForModel delegates a slot-free model choice with no excluded
// providers. It validates the admitted group and applies the final group's
// scheduling mode before returning a target for the caller's auxiliary use.
func (s *Compatible) SelectProviderForModel(ctx context.Context, groupID *int64, sessionHash string, requestedModel string) (*gatewayprovider.ExecutionProvider, error) {
	return s.SelectProviderForModelWithExclusions(ctx, groupID, sessionHash, requestedModel, nil)
}

// SelectProviderForModelWithExclusions returns a model-compatible target
// outside excludedIDs after validating the admitted group. Advanced groups use
// select-only context, and any release handle returned by that path is invoked
// before exposing the provider.
func (s *Compatible) SelectProviderForModelWithExclusions(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.ExecutionProvider, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if s.groupUsesAdvancedScheduler(ctx, groupID) {
		selection, _, selectErr := s.SelectProviderWithScheduler(
			schedulercore.WithSelectOnly(ctx),
			groupID,
			"",
			sessionHash,
			requestedModel,
			excludedIDs, egress.OpenAIUpstreamTransportAny, false,
		)
		if selectErr != nil {
			return nil, selectErr
		}
		if selection == nil || selection.Provider == nil {
			return nil, schedulercore.ErrNoAvailableProviders
		}
		// Auxiliary selection hands back a target for counting or probing. Release
		// any supplied handle here before the caller receives the slot-free target.
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		return selection.Provider, nil
	}
	return s.selectProviderForModelWithExclusions(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, false, 0, "")
}

// shouldUseGroupModelUnsupportedError reports whether the nonblank routing
// model is unsupported by every record-schedulable candidate. Empty input pools
// and blank models return false, and inactive records do not establish a
// relevant pool.
//
// A single supporting candidate is enough to keep this result false. The caller
// can then report temporary unavailability instead of claiming the group's
// model configuration is missing. These checks examine record schedulability
// and compatible model support; other request-specific quota and capacity gates
// belong to the selection flow.
func shouldUseGroupModelUnsupportedError(ctx context.Context, providers []gatewayprovider.ExecutionProvider, requestedModel string) bool {
	requestedModel = strings.TrimSpace(requestedModel)
	if requestedModel == "" || len(providers) == 0 {
		return false
	}
	hasRelevantProvider := false
	for i := range providers {
		acc := &providers[i]
		if !acc.View().IsSchedulable() {
			continue
		}
		hasRelevantProvider = true
		if gatewayprovider.ExecutionModelPolicy(acc).SupportsCompatibleRouting(ctx, requestedModel) {
			return false
		}
	}
	return hasRelevantProvider
}

// SelectProviderForTokenCount selects an unbilled target after platform, model,
// capability, and runtime checks. It uses basic slot-free selection even for
// auxiliary counting, so the caller can invoke counting without occupying
// generation capacity.
func (s *Compatible) SelectProviderForTokenCount(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	requiredCapability providercore.OpenAIEndpointCapability,
	platform string,
) (*gatewayprovider.ExecutionProvider, error) {
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	return s.selectProviderForModelWithExclusions(
		ctx,
		groupID,
		platform,
		sessionHash,
		requestedModel,
		nil,
		false,
		0,
		requiredCapability,
	)
}

// noAvailableOpenAISelectionErrorForRoutingWithDetails classifies compatible
// selection exhaustion for the caller. Compact rejection takes precedence,
// followed by routing's typed model error when the first supplied provider
// collection supports that diagnosis.
//
// Other failures carry the client model and optional details in
// openAINoAvailableSelectionError. That wrapper keeps ErrNoAvailableProviders
// available through errors.Is while retaining the human-readable pool
// diagnostics. Model support uses routingModel, keeping the provider-layer name
// separate from the client name shown in the error.
func noAvailableOpenAISelectionErrorForRoutingWithDetails(ctx context.Context, requestedModel string, routingModel string, compactBlocked bool, details string, providers ...[]gatewayprovider.ExecutionProvider) error {
	if compactBlocked {
		return schedulercore.ErrNoAvailableCompactProviders
	}
	if len(providers) > 0 && shouldUseGroupModelUnsupportedError(ctx, providers[0], routingModel) {
		if err := routing.NewGroupModelRejection("", requestedModel, modelRejectionSources(providers[0])); err != nil {
			return err
		}
	}
	message := "no available OpenAI providers"
	if requestedModel != "" {
		message = fmt.Sprintf("no available OpenAI providers supporting model: %s", requestedModel)
	}
	if details != "" {
		message += " (" + details + ")"
	}
	return openAINoAvailableSelectionError{message: message}
}

// Error returns the compatible selection diagnostic message stored in this
// wrapper.
func (e openAINoAvailableSelectionError) Error() string {
	return e.message
}

// Unwrap exposes ErrNoAvailableProviders so callers can classify compatible
// pool exhaustion.
func (e openAINoAvailableSelectionError) Unwrap() error {
	return schedulercore.ErrNoAvailableProviders
}

// withOpenAIQuotaAutoPauseContext captures current quota auto-pause settings
// for provider checks on this request. Missing selector or settings cache
// passes context through, retaining any settings already supplied by the
// caller.
func (s *Compatible) withOpenAIQuotaAutoPauseContext(ctx context.Context) context.Context {
	if s == nil || s.quotaSettings == nil {
		return ctx
	}
	return gatewayprovider.WithQuotaAutoPauseSettings(ctx, s.quotaSettings.GetOpenAIQuotaAutoPauseSettings(ctx))
}

// selectProviderForModelWithExclusions resolves one group mapping before basic
// slot-free selection. Callers supply the client model, while the routing-aware
// helper receives its resolved provider-layer identity and the unchanged
// exclusion set.
func (s *Compatible) selectProviderForModelWithExclusions(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyProviderID int64, requiredCapability providercore.OpenAIEndpointCapability) (*gatewayprovider.ExecutionProvider, error) {
	routingModel := s.resolveGroupRoutingModel(ctx, groupID, requestedModel)
	return s.selectProviderForModelWithExclusionsForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, stickyProviderID, requiredCapability)
}

// selectProviderForModelWithExclusionsForRouting invokes basic slot-free
// selection with an already resolved routing model. It validates group
// admission, honors a nonblank forced platform from context, and captures
// sticky eligibility inputs for provider rechecks.
func (s *Compatible) selectProviderForModelWithExclusionsForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, stickyProviderID int64, requiredCapability providercore.OpenAIEndpointCapability) (*gatewayprovider.ExecutionProvider, error) {
	resolvedCtx, _, groupErr := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if groupErr != nil {
		return nil, groupErr
	}
	ctx = resolvedCtx
	if forced, ok := apikey.ForcePlatformFromContext(ctx); ok && strings.TrimSpace(forced) != "" {
		platform = forced
	}
	ctx = s.withCandidatePolicy(ctx, groupID, sessionHash)
	core, scope := (&compatiblePicker{service: s}).platformSelector()
	selected, err := core.SelectBasicOnly(ctx, schedulercore.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, StickyProviderID: stickyProviderID, RequiredCapability: requiredCapability})
	return scope.oldProvider(selected), err
}

// SelectProviderWithLoadAwareness delegates compatible load-aware selection
// after capturing quota auto-pause settings. A successful result carries
// acquired resources or a wait plan; callers finish resource acquisition and
// release according to that result.
func (s *Compatible) SelectProviderWithLoadAwareness(ctx context.Context, groupID *int64, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}) (*gatewayprovider.SelectionResult, error) {
	return s.selectProviderWithLoadAwareness(s.withOpenAIQuotaAutoPauseContext(ctx), groupID, "", sessionHash, requestedModel, excludedIDs, false, "")
}

// selectProviderWithLoadAwareness resolves the client's group model before
// invoking the routing-aware basic scheduler. It passes exclusions, Compact
// requirements, and endpoint capability through so provider eligibility uses
// the same dispatch identity.
func (s *Compatible) selectProviderWithLoadAwareness(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) (*gatewayprovider.SelectionResult, error) {
	routingModel := s.resolveGroupRoutingModel(ctx, groupID, requestedModel)
	return s.selectProviderWithLoadAwarenessForRouting(ctx, groupID, platform, sessionHash, requestedModel, routingModel, excludedIDs, requireCompact, requiredCapability)
}

// selectProviderWithLoadAwarenessForRouting invokes basic load-aware selection
// for an already mapped routing model. Group validation and forced-platform
// filtering precede candidate policy capture, and the returned scope restores
// the provider with its resource decision.
func (s *Compatible) selectProviderWithLoadAwarenessForRouting(ctx context.Context, groupID *int64, platform string, sessionHash string, requestedModel string, routingModel string, excludedIDs map[int64]struct{}, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) (*gatewayprovider.SelectionResult, error) {
	resolvedCtx, _, groupErr := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if groupErr != nil {
		return nil, groupErr
	}
	ctx = resolvedCtx
	if forced, ok := apikey.ForcePlatformFromContext(ctx); ok && strings.TrimSpace(forced) != "" {
		platform = forced
	}
	ctx = s.withCandidatePolicy(ctx, groupID, sessionHash)
	core, scope := (&compatiblePicker{service: s}).platformSelector()
	selected, err := core.SelectBasic(ctx, schedulercore.PlatformSelectionInput{GroupID: groupID, Platform: platform, SessionHash: sessionHash, RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs, RequireCompact: requireCompact, RequiredCapability: requiredCapability})
	return scope.restore(selected), err
}

// listSchedulableProviders reads candidates from a positive group's snapshot or
// repository. Empty platform queries the known platform set; read errors are
// wrapped, and threshold plus Grok soft-quota filtering apply before returning
// the pool.
func (s *Compatible) listSchedulableProviders(ctx context.Context, groupID *int64, platform string) ([]gatewayprovider.ExecutionProvider, error) {
	if groupID == nil || *groupID <= 0 {
		return nil, nil
	}
	platform = strings.TrimSpace(platform)
	var providers []gatewayprovider.ExecutionProvider
	var err error
	if s.schedulerSnapshot != nil {
		providers, _, err = readSnapshotProviders(ctx, s.schedulerSnapshot, groupID, platform, platform != "")
	} else if platform == "" {
		providers, err = s.providerRepo.ListSchedulableByGroupIDAndPlatforms(ctx, *groupID, capability.ProviderPlatforms())
	} else {
		providers, err = s.providerRepo.ListSchedulableByGroupIDAndPlatform(ctx, *groupID, platform)
	}
	if err != nil {
		return nil, fmt.Errorf("query providers failed: %w", err)
	}
	providers = s.filterOpenAIProvidersBySchedulingThreshold(ctx, providers)
	return s.filterGrokFreeQuotaProvidersForOpenAI(ctx, providers), nil
}

// tryAcquireProviderSlot delegates capacity acquisition to the compatible
// concurrency service. Select-only context or an absent service returns
// acquired with a no-op release; callers release successful acquired capacity
// after their attempt.
func (s *Compatible) tryAcquireProviderSlot(ctx context.Context, providerID int64, maxConcurrency int) (*schedulercore.AcquireResult, error) {
	if schedulercore.IsSelectOnly(ctx) {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	if s.concurrencyService == nil {
		return &schedulercore.AcquireResult{Acquired: true, ReleaseFunc: func() {}}, nil
	}
	return s.concurrencyService.AcquireProviderSlot(ctx, providerID, maxConcurrency)
}

// resolveFreshSchedulableOpenAIProvider returns a target passing the current
// candidate checks, or nil when unavailable. A configured snapshot causes a new
// full read by provider ID; otherwise the supplied target is checked directly.
//
// Eligibility combines protocol, model, request group, platform quotas, shadow
// parent policy and health, credential or model blocks, scheduling thresholds,
// and proxy quarantine. Read failures reject this candidate so the scheduler
// can continue with others. The returned pointer is the checked record, letting
// later stages retain its current configuration.
func (s *Compatible) resolveFreshSchedulableOpenAIProvider(ctx context.Context, provider *gatewayprovider.ExecutionProvider, platform string, requestedModel string, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) *gatewayprovider.ExecutionProvider {
	if provider == nil {
		return nil
	}
	resetChatToolObservation(ctx, provider.Record.ID, chatToolFresh)
	platform = strings.TrimSpace(platform)

	fresh := provider
	if s.schedulerSnapshot != nil {
		// Snapshot ranking can outlive eligibility changes. A failed full read rejects
		// this candidate rather than letting its older snapshot supply execution data.
		current, err := s.getSchedulableProvider(ctx, provider.Record.ID)
		if err != nil || current == nil {
			return nil
		}
		fresh = current
	}

	if s.candidateEligibilityReason(ctx, fresh, platform, requestedModel, requireCompact, requiredCapability) != "" {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, fresh) || !providercore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(fresh), func(id int64) *providercore.Record {
		return gatewayprovider.ExecutionRecord(s.parentProviderLookup(ctx)(id))
	}) {
		return nil
	}
	if s.isOpenAIProviderRequestRuntimeBlocked(fresh, requestedModel) {
		return nil
	}
	if s.isOpenAIProviderBlockedBySchedulingThreshold(ctx, fresh) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, fresh) {
		return nil
	}
	state, _ := ctx.Value(candidatePolicyKey{}).(candidatePolicy)
	if s.chatToolEligibilityReason(ctx, fresh, state.groupID, requestedModel, requireCompact, chatToolFresh) != "" {
		return nil
	}
	return fresh
}

// parentProviderLookup returns a repository callback for shadow parent health
// checks. Missing repositories return nil; each read returns its record value
// and discards the error, leaving parent health to evaluate that value or nil.
func (s *Compatible) parentProviderLookup(ctx context.Context) func(int64) *gatewayprovider.ExecutionProvider {
	return func(id int64) *gatewayprovider.ExecutionProvider {
		if s.providerRepo == nil {
			return nil
		}
		// Parent health consumes the reader's record result, including nil when
		// unavailable. The lookup error is discarded because the shared gate evaluates that result.
		a, _ := s.providerRepo.GetByID(ctx, id)
		return a
	}
}

// recheckSelectedOpenAIProviderFromDB returns the target that passes the
// selected-provider revalidation, or nil on rejection. With snapshots and a
// repository, it reads the latest database record before applying membership,
// privacy, platform eligibility, shadow policy, runtime blocks, thresholds, and
// quarantine.
//
// When those paired readers are absent, it checks the supplied record's
// membership, privacy, candidate eligibility, thresholds, shadow policy and
// health, and proxy state directly. Database read failure rejects the
// candidate. A successful database branch returns latest so cached credentials
// and memberships cannot replace the verified target.
func (s *Compatible) recheckSelectedOpenAIProviderFromDB(ctx context.Context, provider *gatewayprovider.ExecutionProvider, groupID *int64, platform string, requestedModel string, requireCompact bool, requiredCapability providercore.OpenAIEndpointCapability) *gatewayprovider.ExecutionProvider {
	if provider == nil {
		return nil
	}
	resetChatToolObservation(ctx, provider.Record.ID, chatToolRechecked)
	platform = strings.TrimSpace(platform)
	if s.schedulerSnapshot == nil || s.providerRepo == nil {
		if !s.openAIProviderMatchesSchedulingGroup(provider, groupID) {
			return nil
		}
		if !s.openAIProviderPassesPrivacyRequirement(ctx, groupID, provider) {
			return nil
		}
		if s.candidateEligibilityReason(ctx, provider, platform, requestedModel, requireCompact, requiredCapability) != "" {
			return nil
		}
		if s.isOpenAIProviderBlockedBySchedulingThreshold(ctx, provider) {
			return nil
		}
		if !s.shadowProtocolsAllowed(ctx, provider) || !providercore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(provider), func(id int64) *providercore.Record {
			return gatewayprovider.ExecutionRecord(s.parentProviderLookup(ctx)(id))
		}) {
			return nil
		}
		if s.isOpenAIProxyStreamQuarantined(ctx, provider) {
			return nil
		}
		if s.chatToolEligibilityReason(ctx, provider, groupID, requestedModel, requireCompact, chatToolRechecked) != "" {
			return nil
		}
		return provider
	}

	// The database can revoke group membership or change credentials after ranking.
	// Check its current record before returning the execution target chosen from snapshots.
	latest, err := s.providerRepo.GetByID(ctx, provider.Record.ID)
	if err != nil || latest == nil {
		return nil
	}
	if !s.openAIProviderMatchesSchedulingGroup(latest, groupID) {
		return nil
	}
	if !s.openAIProviderPassesPrivacyRequirement(ctx, groupID, latest) {
		return nil
	}
	if s.candidateEligibilityReason(ctx, latest, platform, requestedModel, requireCompact, requiredCapability) != "" {
		return nil
	}
	if !s.shadowProtocolsAllowed(ctx, latest) || !providercore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(latest), func(id int64) *providercore.Record {
		return gatewayprovider.ExecutionRecord(s.parentProviderLookup(ctx)(id))
	}) {
		return nil
	}
	if s.isOpenAIProviderRequestRuntimeBlocked(latest, requestedModel) {
		return nil
	}
	if s.isOpenAIProviderBlockedBySchedulingThreshold(ctx, latest) {
		return nil
	}
	if s.isOpenAIProxyStreamQuarantined(ctx, latest) {
		return nil
	}
	if s.chatToolEligibilityReason(ctx, latest, groupID, requestedModel, requireCompact, chatToolRechecked) != "" {
		return nil
	}
	return latest
}

// openAIProviderMatchesSchedulingGroup reports membership through the
// provider's stored group representations. Nil records and absent or
// nonpositive group IDs fail, requiring a positive matching admitted group for
// scheduling.
func (s *Compatible) openAIProviderMatchesSchedulingGroup(provider *gatewayprovider.ExecutionProvider, groupID *int64) bool {
	return openAIStickyProviderMatchesGroup(provider, groupID)
}

// openAIProviderPassesPrivacyRequirement checks this group's privacy
// requirement against the target's recorded state. Missing providers fail,
// while groups without a requirement admit either privacy state through the
// request's cached group policy.
func (s *Compatible) openAIProviderPassesPrivacyRequirement(ctx context.Context, groupID *int64, provider *gatewayprovider.ExecutionProvider) bool {
	return provider != nil && (!s.openAIGroupRequiresPrivacySet(ctx, groupID) || provider.View().IsPrivacySet())
}

// getSchedulableProvider reads the target through snapshots or the provider
// repository. Read errors pass through, while threshold rejection and a blocked
// Grok Free OAuth gate return nil without error for candidate selection.
func (s *Compatible) getSchedulableProvider(ctx context.Context, providerID int64) (*gatewayprovider.ExecutionProvider, error) {
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
	if s.isOpenAIProviderBlockedBySchedulingThreshold(ctx, provider) {
		return nil, nil
	}

	if provider.View().IsGrok() {
		if gated := s.filterGrokFreeQuotaProvidersForOpenAI(ctx, []gatewayprovider.ExecutionProvider{*provider}); len(gated) == 0 {
			return nil, nil
		}
	}
	return provider, nil
}

// filterGrokFreeQuotaProvidersForOpenAI applies the basic compatible selector's
// bound Free OAuth soft gate. Missing selector or gate retains the incoming
// candidates, while observed quota blocks remove only explicitly eligible Free
// OAuth records.
func (s *Compatible) filterGrokFreeQuotaProvidersForOpenAI(ctx context.Context, providers []gatewayprovider.ExecutionProvider) []gatewayprovider.ExecutionProvider {
	if s == nil {
		return providers
	}
	return filterFreeQuotaProjection(s.freeQuotaGate, providers)
}

// filterOpenAIProvidersBySchedulingThreshold returns candidates passing the
// health adapter's current scheduling threshold. Empty input stays unchanged;
// other results use new slice storage while the provider records retain their
// referenced data.
func (s *Compatible) filterOpenAIProvidersBySchedulingThreshold(ctx context.Context, providers []gatewayprovider.ExecutionProvider) []gatewayprovider.ExecutionProvider {
	if len(providers) == 0 {
		return providers
	}

	filtered := make([]gatewayprovider.ExecutionProvider, 0, len(providers))
	for i := range providers {
		if s.isOpenAIProviderBlockedBySchedulingThreshold(ctx, &providers[i]) {
			continue
		}
		filtered = append(filtered, providers[i])
	}
	return filtered
}

// isOpenAIProviderBlockedBySchedulingThreshold delegates scheduling pause
// decisions to the bound health adapter. Nil selector, observer, or target
// returns false; a handled observation can update the execution record's
// temporary scheduling state.
func (s *Compatible) isOpenAIProviderBlockedBySchedulingThreshold(ctx context.Context, provider *gatewayprovider.ExecutionProvider) bool {
	if s == nil || s.healthObserver == nil || provider == nil {
		return false
	}
	return gatewayprovider.ApplyExecutionSchedulingThreshold(ctx, s.healthObserver, provider)
}

// hydrateSelectedProvider supplies a full record for snapshot-only compatible
// selection. Nil targets, absent snapshots, and paired repositories pass the
// input through; snapshot-only reads propagate errors and report an error when
// the selected provider is missing.
func (s *Compatible) hydrateSelectedProvider(ctx context.Context, provider *gatewayprovider.ExecutionProvider) (*gatewayprovider.ExecutionProvider, error) {
	if provider == nil || s.schedulerSnapshot == nil || s.providerRepo != nil {
		return provider, nil
	}
	hydrated, err := readSnapshotProvider(ctx, s.schedulerSnapshot, provider.Record.ID)
	if err != nil {
		return nil, err
	}
	if hydrated == nil {
		return nil, fmt.Errorf("selected openai provider %d not found during hydration", provider.Record.ID)
	}
	return hydrated, nil
}

// newSelectionResult hydrates the provider and attaches the acquired flag,
// release callback, and wait plan. Preparation errors return no result; this
// helper leaves cleanup to its caller, while newAcquiredSelectionResult handles
// an acquired slot's failure.
func (s *Compatible) newSelectionResult(ctx context.Context, provider *gatewayprovider.ExecutionProvider, acquired bool, release func(), waitPlan *schedulercore.ProviderWaitPlan) (*gatewayprovider.SelectionResult, error) {
	hydrated, err := s.hydrateSelectedProvider(ctx, provider)
	if err != nil {
		return nil, err
	}
	return &gatewayprovider.SelectionResult{
		Provider:    hydrated,
		Acquired:    acquired,
		ReleaseFunc: release,
		WaitPlan:    waitPlan,
	}, nil
}

// newAcquiredSelectionResult prepares an acquired result and returns its
// release callback to the execution caller. Hydration failure invokes a nonnil
// release immediately before returning the error so preparation cannot retain
// the provider's capacity.
func (s *Compatible) newAcquiredSelectionResult(ctx context.Context, provider *gatewayprovider.ExecutionProvider, release func()) (*gatewayprovider.SelectionResult, error) {
	selection, err := s.newSelectionResult(ctx, provider, true, release, nil)
	// Slot acquisition precedes full snapshot hydration. Return that capacity when
	// preparation fails so another request can still use the selected provider.
	if err != nil && release != nil {
		release()
	}
	return selection, err
}

// schedulingConfig returns the compatible selector's startup selection and wait
// policy value.
func (s *Compatible) schedulingConfig() schedulercore.FlowOptions {
	return s.options.Scheduling
}

// StickyProviderID reads the positive group's session binding for cache billing
// attribution. Missing selector, cache, group, or session returns zero, and
// cache read failure also produces zero for an unavailable binding.
func (s *Compatible) StickyProviderID(ctx context.Context, groupID *int64, sessionHash string) int64 {
	if s == nil || s.cache == nil || groupID == nil || *groupID <= 0 || sessionHash == "" {
		return 0
	}
	// Cache billing attribution treats an unreadable binding as absent. A failed
	// observation returns zero so completion can apply its unbound-session rule.
	id, err := s.getStickySessionProviderID(ctx, groupID, sessionHash)
	if err != nil {
		return 0
	}
	return id
}

// readSchedulingGroup invokes the bound full group reader for compatible
// scheduling policy. An unavailable reader returns nil without error so callers
// resolving optional settings can handle a missing group.
func (s *Compatible) readSchedulingGroup(ctx context.Context, id int64) (*routing.Group, error) {
	if s.schedulingGroups == nil {
		return nil, nil
	}
	return s.schedulingGroups(ctx, id)
}
