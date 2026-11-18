package selection

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	providercore "github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// compatible_picker.go coordinates compatible provider scheduling, affinity
// preferences, admission retries, and outcome feedback.
// Compatible requests can prefer a prior response provider, a session provider,
// or group model-routing candidates while still meeting protocol and runtime
// eligibility. The admitted group's mode selects either the basic core or an
// advanced picker, and endpoint, image, transport, privacy, and Compact
// requirements remain attached to the request's routing model.
//
// Selection can retry after Anthropic session registration rejects an acquired
// target, releasing its capacity before adding it to a private exclusion set.
// Exhaustion with active OpenAI proxy quarantine permits one request-local
// bypass pass for an unbypassed branch; the circuit retains its observations.
// Advanced results capture their feedback alphas, while the picker shares
// performance statistics, counters, and a separately published Grok soft-quota
// cache across invocations.

// Compatible decision-layer values identify load-based choices and
// guardian-parent affinity in gateway diagnostics and selection metrics.
const (
	openAIProviderScheduleLayerLoadBalance    = "load_balance"    // ordinary ranked choice
	openAIProviderScheduleLayerGuardianParent = "guardian_parent" // guardian parent affinity
)

// openAIProviderSchedulerMetrics embeds the shared counter set used by
// compatible scheduler decisions.
type openAIProviderSchedulerMetrics struct {
	schedulercore.PlatformMetrics // shared atomic selection counters
}

// compatiblePicker binds compatible eligibility to shared advanced metrics,
// feedback, and lazy quota observations.
type compatiblePicker struct {
	service *Compatible                    // bound eligibility service
	metrics openAIProviderSchedulerMetrics // shared selection observations
	stats   *schedulercore.RuntimeStats    // shared performance feedback
	// freeQuotaGate receives one factory-created cache through atomic publication.
	// Later advanced calls reuse that winner for this picker's quota observations.
	freeQuotaGate atomic.Pointer[providercore.FreeQuotaGate]
}

// pickerEngine selects compatible targets and exposes their shared feedback and
// metric operations.
type pickerEngine interface {
	// Select returns a target and the decision describing its selection layer. An
	// acquired result transfers its release callback to the execution caller; a
	// wait plan requires capacity handling before forwarding.
	Select(context.Context, schedulercore.PlatformSelectionInput) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error)
	// ReportResult records provider success and optional TTFT latency in
	// milliseconds. Supplied feedback alphas apply to this observation; callers
	// pass the settings captured with the selection when available.
	ReportResult(int64, bool, *int, ...policy.FeedbackConfig)
	// ReportSwitch records one provider switch in the compatible scheduler's
	// metric counters.
	ReportSwitch()
	// SnapshotMetrics returns current compatible selection counters and retained
	// provider-feedback count as a value.
	SnapshotMetrics() schedulercore.PlatformMetricsSnapshot
}

// recordSwitch increments the embedded compatible provider-switch counter when
// the metric wrapper is present. A nil receiver is ignored, allowing optional
// metric callers to finish provider switching without a counter update.
func (m *openAIProviderSchedulerMetrics) recordSwitch() {
	if m != nil {
		m.RecordSwitch()
	}
}

// newDefaultOpenAIProviderScheduler constructs the compatible picker with the
// supplied service and feedback store. Missing feedback receives a new
// RuntimeStats instance, while service callers still supply the dependencies
// needed for selection.
func newDefaultOpenAIProviderScheduler(service *Compatible, stats *schedulercore.RuntimeStats) pickerEngine {
	if stats == nil {
		stats = schedulercore.NewRuntimeStats(time.Now)
	}
	return &compatiblePicker{
		service: service,
		stats:   stats,
	}
}

// Select delegates one compatible request to the platform core using a copied
// exclusion map. A fresh scope restores the checked execution target and its
// release or wait handles from the returned scheduler result.
func (s *compatiblePicker) Select(ctx context.Context, req schedulercore.PlatformSelectionInput) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	core, scope := s.platformSelector()
	v, decision, err := core.Select(ctx, cloneSelectionInput(req))
	return scope.restore(v), decision, err
}

// hasOpenAIProviderGroupMetadata reports whether a target carries GroupIDs or
// ProviderGroups membership data. Nil targets and two empty representations
// return false, letting continuation checks distinguish missing legacy metadata
// from recorded membership.
func hasOpenAIProviderGroupMetadata(provider *gatewayprovider.ExecutionProvider) bool {
	return provider != nil && (len(provider.Record.GroupIDs) > 0 || len(provider.Record.ProviderGroups) > 0)
}

// openAIStickyProviderMatchesGroup checks a positive group against GroupIDs and
// ProviderGroups. Nil targets and nil or nonpositive IDs fail, requiring actual
// membership even when the provider has no group metadata.
func openAIStickyProviderMatchesGroup(provider *gatewayprovider.ExecutionProvider, groupID *int64) bool {
	if provider == nil {
		return false
	}
	if groupID == nil || *groupID <= 0 {
		return false
	}
	if slices.Contains(provider.Record.GroupIDs, *groupID) {
		return true
	}
	for _, providerGroup := range provider.Record.ProviderGroups {
		if providerGroup.GroupID == *groupID {
			return true
		}
	}
	return false
}

// isProviderTransportCompatible permits Any and HTTP/SSE requests before
// consulting the service. Other transports require a bound compatible service
// and use its provider transport rules, returning false when that service is
// unavailable.
func (s *compatiblePicker) isProviderTransportCompatible(provider *gatewayprovider.ExecutionProvider, requiredTransport egress.OpenAIUpstreamTransport) bool {
	if requiredTransport == egress.OpenAIUpstreamTransportAny || requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || s.service == nil {
		return false
	}
	return s.service.isOpenAIProviderTransportCompatible(provider, requiredTransport)
}

// lookupShadowParentProvider returns a successfully read snapshot parent or
// uses the repository result. Missing services or repositories return nil;
// repository errors are discarded while their record value passes to
// parent-health checks.
func (s *compatiblePicker) lookupShadowParentProvider(ctx context.Context, id int64) *gatewayprovider.ExecutionProvider {
	if s == nil || s.service == nil {
		return nil
	}
	if s.service.schedulerSnapshot != nil {
		// Missing or failed parent snapshots need a repository check before shadow
		// health can be decided; a cache miss alone cannot establish parent unavailability.
		if provider, err := readSnapshotProvider(ctx, s.service.schedulerSnapshot, id); err == nil && provider != nil {
			return provider
		}
	}
	if s.service.providerRepo == nil {
		return nil
	}
	// Parent health evaluates the repository's record result even when that read
	// reports an error. A nil result rejects the shadow through the shared gate.
	provider, _ := s.service.providerRepo.GetByID(ctx, id)
	return provider
}

// isProviderRequestCompatibleReason returns the first compatible request
// rejection with its boolean result. A bound service supplies candidate policy
// and shadow-parent protocol checks; the target then faces request privacy,
// runtime, proxy, and quota eligibility.
//
// Parent health, compatible model support, applicable final upstream group
// restrictions, and endpoint/image capabilities complete the check. The
// provider-layer model comes from RoutingModel when present, keeping the client
// name available for error reporting. A successful result returns true and an
// empty reason, so ranking can operate on the candidates that passed these
// gates.
func (s *compatiblePicker) isProviderRequestCompatibleReason(ctx context.Context, provider *gatewayprovider.ExecutionProvider, req schedulercore.PlatformSelectionInput) (bool, string) {
	if s != nil && s.service != nil && !s.service.shadowProtocolsAllowed(ctx, provider) {
		return false, "parent_protocol_unavailable"
	}
	if !gatewayprovider.ExecutionModelPolicy(provider).AllowsProtocol(ctx, "") {
		return false, "protocol_unavailable"
	}
	if provider == nil {
		return false, "provider_nil"
	}
	if reason := s.service.candidateEligibilityReason(ctx, provider, req.Platform, requestRoutingModel(req), req.RequireCompact, req.RequiredCapability); reason != "" {
		return false, reason
	}
	if req.RequirePrivacySet && !provider.View().IsPrivacySet() {
		return false, "privacy_not_set"
	}
	if s != nil && s.service != nil && s.service.isOpenAIProviderRequestRuntimeBlocked(provider, requestRoutingModel(req)) {
		return false, "runtime_blocked"
	}
	if s != nil && s.service != nil && s.service.isOpenAIProxyStreamQuarantined(ctx, provider) {
		return false, "proxy_stream_quarantined"
	}

	if paused, decision := gatewayprovider.OpenAIQuotaPause(ctx, provider); paused {
		reason := "quota_auto_pause"
		if decision.Window != "" {
			reason += "_" + decision.Window
		}
		return false, reason
	}

	if !providercore.ParentHealthyForShadow(gatewayprovider.ExecutionRecord(provider), func(id int64) *providercore.Record {
		return gatewayprovider.ExecutionRecord(s.lookupShadowParentProvider(ctx, id))
	}) {
		return false, "shadow_parent_unhealthy"
	}
	if !gatewayprovider.ExecutionModelPolicy(provider).SupportsCompatibleRouting(ctx, requestRoutingModel(req)) {
		return false, "model_not_supported"
	}
	if req.GroupID != nil && s != nil && s.service != nil &&
		s.service.NeedsUpstreamGroupRestriction(ctx, req.GroupID) &&
		s.service.UpstreamRoutingModelRestricted(ctx, *req.GroupID, provider, requestRoutingModel(req), req.RequireCompact) {
		return false, "group_upstream_restricted"
	}
	if !providerSupportsOpenAICapabilities(ctx, provider, req.RequiredCapability, req.RequiredImageCapability) {
		return false, "capability_mismatch"
	}
	reason := s.service.chatToolEligibilityReason(ctx, provider, req.GroupID, requestRoutingModel(req), req.RequireCompact, chatToolRanked)
	return reason == "", reason
}

// ReportResult delegates provider feedback using optional captured alphas and
// TTFT milliseconds. A nil picker or absent feedback store is ignored,
// allowing metric callers to report an optional observation safely.
func (s *compatiblePicker) ReportResult(providerID int64, success bool, ttftMs *int, feedback ...policy.FeedbackConfig) {
	if s == nil || s.stats == nil {
		return
	}
	s.stats.Report(providerID, success, ttftMs, feedback...)
}

// ReportSwitch increments compatible provider-switch metrics for this picker
// when present. A nil picker is ignored, and the embedded metric implementation
// owns synchronization of its shared counter updates.
func (s *compatiblePicker) ReportSwitch() {
	if s == nil {
		return
	}
	s.metrics.recordSwitch()
}

// SnapshotMetrics returns the picker counters together with its retained
// provider-feedback count. A nil picker returns an empty value; populated
// pickers require the feedback store supplied by construction before reading
// its size.
func (s *compatiblePicker) SnapshotMetrics() schedulercore.PlatformMetricsSnapshot {
	if s == nil {
		return schedulercore.PlatformMetricsSnapshot{}
	}
	return s.metrics.Snapshot(s.stats.Size())
}

// groupUsesAdvancedScheduler reads the final group's advanced flag from valid
// context or a snapshot-bound full read. Missing selector, invalid group ID,
// unavailable snapshot binding, or failed group lookup returns false.
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
	// An unreadable full group cannot establish advanced scheduling eligibility.
	// Return false so this request uses the constructed selector's basic path.
	group, err := s.readSchedulingGroup(ctx, *groupID)
	return err == nil && group != nil && group.UsesAdvancedScheduler()
}

// getOpenAIProviderScheduler returns the shared picker when the resolved group
// uses advanced scheduling. Nil selectors and basic groups return nil;
// pickerOnce initializes omitted feedback and the picker before publishing them
// to concurrent callers.
func (s *Compatible) getOpenAIProviderScheduler(ctx context.Context, groupID *int64) pickerEngine {
	if s == nil {
		return nil
	}
	if !s.groupUsesAdvancedScheduler(ctx, groupID) {
		return nil
	}
	s.pickerOnce.Do(func() {
		if s.openaiProviderStats == nil {
			s.openaiProviderStats = schedulercore.NewRuntimeStats(time.Now)
		}
		if s.picker == nil {
			s.picker = newDefaultOpenAIProviderScheduler(s, s.openaiProviderStats)
		}
	})
	return s.picker
}

// ensureOpenAIProviderScheduler initializes or returns the shared picker for
// metric and diagnostic callers. Nil selectors return nil; selection uses
// getOpenAIProviderScheduler to check group mode before requesting this shared
// runtime.
func (s *Compatible) ensureOpenAIProviderScheduler() pickerEngine {
	if s == nil {
		return nil
	}
	s.pickerOnce.Do(func() {
		if s.openaiProviderStats == nil {
			s.openaiProviderStats = schedulercore.NewRuntimeStats(time.Now)
		}
		if s.picker == nil {
			s.picker = newDefaultOpenAIProviderScheduler(s, s.openaiProviderStats)
		}
	})
	return s.picker
}

// SelectProviderWithScheduler selects a target using final-group mode,
// previous-response affinity, and session affinity. It delegates general
// capability selection; callers await any wait plan before forwarding and
// release acquired capacity after the attempt.
func (s *Compatible) SelectProviderWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requireCompact bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	return s.selectProviderWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, "", "", requireCompact, "", false)
}

// SelectProviderWithSchedulerForCapability adds endpoint requirements to
// compatible provider choice. The first optional platform override narrows
// candidates, while previousResponseCanMove permits weighted previous-response
// affinity when the caller can rebuild continuation input on another provider.
func (s *Compatible) SelectProviderWithSchedulerForCapability(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability providercore.OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	platform := ""
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	return s.selectProviderWithScheduler(ctx, groupID, previousResponseID, sessionHash, requestedModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

// SelectProviderWithSchedulerForCapabilityAndRoutingModel retains the client
// name separately from an already resolved provider-layer routing model. Blank
// routing input uses one group mapping; nonblank input is trimmed and passed
// directly to provider policy.
//
// The client identity remains the input to early group restrictions and errors,
// while provider capabilities and mapping use the routing identity. This
// supports protocol dispatch where the routing name differs from the group
// restriction name. The first optional platform override narrows the pool, and
// acquired resources or waiting remain the execution caller's responsibility.
func (s *Compatible) SelectProviderWithSchedulerForCapabilityAndRoutingModel(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability providercore.OpenAIEndpointCapability,
	requireCompact bool,
	previousResponseCanMove bool,
	platformOverride ...string,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	platform := ""
	if len(platformOverride) > 0 {
		platform = platformOverride[0]
	}
	routingModel = strings.TrimSpace(routingModel)
	if routingModel == "" {
		routingModel = s.resolveGroupRoutingModel(ctx, groupID, requestedModel)
	}
	return s.selectProviderWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, "", requireCompact, platform, previousResponseCanMove)
}

// SelectProviderWithSchedulerForImages requires a mapped image-generation model
// and the requested image capability over HTTP/SSE. Failed native selection or
// a missing target retries basic capability; other capability requests return
// the initial result. When every inspected route lacks image capability, the
// returned local error distinguishes unknown attributes from declared rejection.
func (s *Compatible) SelectProviderWithSchedulerForImages(
	ctx context.Context,
	groupID *int64,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredCapability providercore.OpenAIImagesCapability,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	ctx, imageState := withCapabilitySelection(ctx)
	ctx = context.WithValue(ctx, imageModelRequiredKey{}, true)
	selection, decision, err := s.selectProviderWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, egress.OpenAIUpstreamTransportHTTPSSE, "", requiredCapability, false, "", false)
	if err == nil && selection != nil && selection.Provider != nil {
		return selection, decision, nil
	}

	// Native image support is preferred. When that selection has no usable target,
	// retry basic image support so eligible bridge providers can serve this request.
	if requiredCapability == providercore.OpenAIImagesCapabilityNative {
		selection, decision, err = s.selectProviderWithScheduler(ctx, groupID, "", sessionHash, requestedModel, excludedIDs, egress.OpenAIUpstreamTransportHTTPSSE, "", providercore.OpenAIImagesCapabilityBasic, false, "", false)
	}
	if err != nil || selection == nil || selection.Provider == nil {
		if capabilityErr := imageState.failure(); capabilityErr != nil {
			return nil, decision, capabilityErr
		}
	}
	return selection, decision, err
}

// selectProviderWithScheduler applies one group mapping to the client model
// before the routing-aware selection flow. Exclusions and continuation,
// transport, endpoint, image, Compact, and platform requirements pass through
// with the resulting identity.
func (s *Compatible) selectProviderWithScheduler(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability providercore.OpenAIEndpointCapability,
	requiredImageCapability providercore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	routingModel := s.resolveGroupRoutingModel(ctx, groupID, requestedModel)
	return s.selectProviderWithSchedulerForRouting(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// selectProviderWithSchedulerForRouting chooses an eligible target for an
// already resolved model while validating final-group admission.
// Model-preferred providers are tried within the group before the full pool,
// except when continuing a previous response.
//
// A private exclusion map lets session registration reject a chosen target
// after its slot was acquired. That capacity is released before retrying
// another provider. Exhaustion with active OpenAI proxy quarantine permits a
// derived context to bypass that gate while retaining the routing model. A
// branch carrying the bypass hint stops further quarantine retries; the shared
// circuit state remains available to later requests.
func (s *Compatible) selectProviderWithSchedulerForRouting(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability providercore.OpenAIEndpointCapability,
	requiredImageCapability providercore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (selection *gatewayprovider.SelectionResult, decision schedulercore.PlatformDecision, err error) {
	ctx, capabilities := withCapabilitySelection(ctx)
	// Retain typed admission failures until candidate selection has exhausted the
	// pool; a supported route leaves capacity errors under scheduler control.
	defer func() {
		if err == nil && selection != nil && selection.Provider != nil {
			return
		}
		// Only ordinary pool exhaustion can become this request error. Storage
		// failures remain authoritative even when an earlier candidate lacked tools.
		if requeststate.ChatFunctionToolsRequired(ctx) && (err == nil || errors.Is(err, schedulercore.ErrNoAvailableProviders)) {
			if failure := capabilities.chatToolFailure(); failure != nil {
				err = failure
				return
			}
		}
		if err != nil {
			if local := capabilities.failure(); local != nil {
				err = local
			}
		}
	}()
	if forced, ok := apikey.ForcePlatformFromContext(ctx); ok && strings.TrimSpace(forced) != "" {
		platform = forced
	}
	originalGroupID := derefGroupID(groupID)
	resolvedCtx, resolvedGroupID, err := s.resolveOpenAISchedulerGroup(ctx, groupID)
	if err != nil {
		return nil, schedulercore.PlatformDecision{}, err
	}
	ctx = resolvedCtx
	groupID = resolvedGroupID
	if derefGroupID(groupID) != originalGroupID {
		routingModel = s.resolveGroupRoutingModel(ctx, groupID, requestedModel)
	}
	// Group model routing supplies a preferred subset. Its unsuccessful pass
	// leaves the same admitted group available for selection from the full candidate pool.
	if _, configured := ctx.Value(preferredProvidersKey{}).(map[int64]struct{}); !configured && previousResponseID == "" {
		if group, ok := requeststate.GroupFromContext(ctx); ok && group != nil {
			ids := group.GetRoutingProviderIDs(routingModel)
			if len(ids) > 0 {
				preferred := make(map[int64]struct{}, len(ids))
				for _, id := range ids {
					preferred[id] = struct{}{}
				}
				result, decision, preferredErr := s.selectProviderWithSchedulerForRouting(context.WithValue(ctx, preferredProvidersKey{}, preferred), groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
				if preferredErr == nil {
					return result, decision, preferredErr
				}
			}
		}
	}
	excludedIDs = cloneExcludedProviderIDs(excludedIDs)
	for {
		selection, decision, err = s.selectProviderWithSchedulerForRoutingOnce(ctx, groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
		if err != nil || selection == nil || selection.Provider == nil || s.generic == nil || s.generic.checkAndRegisterSession(ctx, selection.Provider, sessionHash) {
			break
		}
		// Session registration rejected an already selected target. Return its slot
		// before recording a retry exclusion so another eligible request can use that capacity.
		if selection.ReleaseFunc != nil {
			selection.ReleaseFunc()
		}
		if excludedIDs == nil {
			excludedIDs = map[int64]struct{}{}
		}
		excludedIDs[selection.Provider.Record.ID] = struct{}{}
	}

	if err == nil || openAIProxyStreamQuarantineBypassed(ctx) {
		return selection, decision, err
	}
	if !errors.Is(err, schedulercore.ErrNoAvailableProviders) && !errors.Is(err, schedulercore.ErrNoAvailableCompactProviders) {
		return selection, decision, err
	}

	if platform != "" && strings.TrimSpace(platform) != capability.PlatformOpenAI {
		return selection, decision, err
	}
	blocked := s.getOpenAIProxyStreamCircuit().ActiveBlockCount(time.Now())
	if blocked == 0 {
		return selection, decision, err
	}
	s.logOpenAIProxyStreamQuarantineFailOpen(requestedModel, blocked)
	// Capacity-style exhaustion with active proxy blocks permits one bypass pass.
	// The copied request hint stops further recursion and leaves shared quarantine state intact.
	return s.selectProviderWithSchedulerForRouting(withOpenAIProxyStreamQuarantineBypass(ctx), groupID, previousResponseID, sessionHash, requestedModel, routingModel, excludedIDs, requiredTransport, requiredCapability, requiredImageCapability, requireCompact, platform, previousResponseCanMove)
}

// resolveOpenAISchedulerGroup validates the admitted ID against request plan,
// group snapshot, and client restrictions. A resolved group is installed in
// copied request state, while the ID remains unchanged and validation errors
// prevent candidate reads.
func (s *Compatible) resolveOpenAISchedulerGroup(ctx context.Context, groupID *int64) (context.Context, *int64, error) {
	var read func(context.Context, int64) (*routing.Group, error)
	if s != nil && s.schedulingGroups != nil {
		read = s.readSchedulingGroup
	}
	group, err := currentSelectionGroup(ctx, groupID, read)
	if err != nil {
		return ctx, nil, err
	}
	if group != nil {
		ctx = requeststate.WithGroup(ctx, group)
	}
	return ctx, groupID, nil
}

// withOpenAIGroupPrivacyRequirement captures the group's privacy decision in
// copied request execution hints. A nil parent panics, and the group identifier
// accompanies the value so another group's cached requirement cannot be reused.
func (s *Compatible) withOpenAIGroupPrivacyRequirement(ctx context.Context, groupID *int64) context.Context {
	if ctx == nil {
		panic("cannot create context from nil parent")
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	hints.GroupPrivacyGroupID = derefGroupID(groupID)
	hints.GroupPrivacyRequirement = requeststate.Hint[bool]{Set: true, Value: s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID)}
	return requeststate.WithExecutionHints(ctx, hints)
}

// openAIGroupRequiresPrivacySet returns a captured requirement belonging to the
// same group or loads the rule again. Nil context panics; group matching
// prevents a prior request hint from supplying another group's policy.
func (s *Compatible) openAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	if ctx == nil {
		panic("nil context")
	}
	hints := requeststate.ExecutionHintsFromContext(ctx)
	if hints.GroupPrivacyRequirement.Set && hints.GroupPrivacyGroupID == derefGroupID(groupID) {
		return hints.GroupPrivacyRequirement.Value
	}
	return s.loadOpenAIGroupRequiresPrivacySet(ctx, groupID)
}

// loadOpenAIGroupRequiresPrivacySet reads the group's privacy rule when
// snapshots are configured. Missing selector, group, or snapshot binding
// returns false, while a full group read error returns true to require privacy
// on that call.
func (s *Compatible) loadOpenAIGroupRequiresPrivacySet(ctx context.Context, groupID *int64) bool {
	if s == nil || groupID == nil || s.schedulerSnapshot == nil {
		return false
	}
	group, err := s.readSchedulingGroup(ctx, *groupID)
	if err != nil {
		// An unreadable group policy cannot establish that privacy is optional.
		// Require it for this pass until a later request can read the group rule.
		return true
	}
	return group != nil && group.RequirePrivacySet
}

// selectProviderWithSchedulerForRoutingOnce chooses the current group's basic
// or advanced path using captured candidate, quota, and privacy inputs.
// Immutable HTTP continuation resolves its response-bound provider before
// either scheduler mode. Other previous-response requests suppress guardian
// lookup, and preserved parent affinity prevents basic fallback from rewriting
// the parent's session binding.
//
// Basic selection can retain a guardian target, then retries with a private
// exclusion set when transport or image capability fails after choice. Advanced
// selection combines group-effective settings with session, guardian, and
// movable response affinity before delegating to the shared picker. Returned
// advanced results capture feedback alphas so later observations use the same
// request policy.
func (s *Compatible) selectProviderWithSchedulerForRoutingOnce(
	ctx context.Context,
	groupID *int64,
	previousResponseID string,
	sessionHash string,
	requestedModel string,
	routingModel string,
	excludedIDs map[int64]struct{},
	requiredTransport egress.OpenAIUpstreamTransport,
	requiredCapability providercore.OpenAIEndpointCapability,
	requiredImageCapability providercore.OpenAIImagesCapability,
	requireCompact bool,
	platform string,
	previousResponseCanMove bool,
) (*gatewayprovider.SelectionResult, schedulercore.PlatformDecision, error) {
	ctx = s.withCandidatePolicy(ctx, groupID, sessionHash)
	ctx = s.withOpenAIQuotaAutoPauseContext(ctx)
	ctx = s.withOpenAIGroupPrivacyRequirement(ctx, groupID)
	platform = strings.TrimSpace(platform)
	decision := schedulercore.PlatformDecision{}
	preserveGuardianParentBinding := requeststate.PreserveGuardianParentBinding(ctx, sessionHash)
	guardianParentProviderID := int64(0)
	if strings.TrimSpace(previousResponseID) == "" {
		guardianParentProviderID = s.resolveOpenAIGuardianParentProviderID(ctx, groupID)
	}
	scheduler := s.getOpenAIProviderScheduler(ctx, groupID)
	// HTTP continuation carries a provider-owned response reference. Resolve that
	// binding before either scheduler mode so unavailable history cannot reach another credential.
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	if strings.TrimSpace(previousResponseID) != "" && !previousResponseCanMove && source == protocol.ProtocolOpenAIResponses {
		selection, err := s.selectHTTPResponseContinuation(ctx, schedulercore.PlatformSelectionInput{
			GroupID: groupID, Platform: platform, PreviousResponseID: previousResponseID,
			RequestedModel: requestedModel, RoutingModel: routingModel, ExcludedIDs: excludedIDs,
			RequiredTransport: string(requiredTransport), RequiredCapability: requiredCapability,
			RequiredImageCapability: requiredImageCapability, RequireCompact: requireCompact,
		})
		if err != nil {
			return nil, decision, err
		}
		decision.Layer = "previous_response_id"
		decision.StickyPreviousHit = true
		decision.SelectedProviderID = selection.Provider.Record.ID
		decision.SelectedProviderType = selection.Provider.Record.Type
		if scheduler != nil {
			selection.AdvancedScheduler = true
			feedback := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID).Feedback
			selection.AdvancedSchedulerFeedback = &feedback
		}
		return selection, decision, nil
	}
	if scheduler == nil {
		decision.Layer = openAIProviderScheduleLayerLoadBalance
		// Basic mode can still reuse an eligible guardian parent provider. Preserve
		// its binding during this optional affinity pass before ordinary pool selection.
		if guardianParentProviderID > 0 {
			fallbackScheduler := &compatiblePicker{service: s, stats: schedulercore.NewRuntimeStats(time.Now)}
			selection, _, err := fallbackScheduler.selectBySessionHash(ctx, schedulercore.PlatformSelectionInput{
				GroupID:                 groupID,
				Platform:                platform,
				SessionHash:             sessionHash,
				StickyProviderID:        guardianParentProviderID,
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
			if selection != nil && selection.Provider != nil {
				decision.Layer = openAIProviderScheduleLayerGuardianParent
				decision.StickySessionHit = true
				decision.SelectedProviderID = selection.Provider.Record.ID
				decision.SelectedProviderType = selection.Provider.Record.Type
				return selection, decision, nil
			}
		}
		legacySessionHash := sessionHash
		if preserveGuardianParentBinding {
			legacySessionHash = ""
		}
		effectiveExcludedIDs := cloneExcludedProviderIDs(excludedIDs)
		for {
			selection, err := s.selectProviderWithLoadAwarenessForRouting(ctx, groupID, platform, legacySessionHash, requestedModel, routingModel, effectiveExcludedIDs, requireCompact, requiredCapability)
			if err != nil {
				return nil, decision, err
			}
			if selection == nil || selection.Provider == nil {
				return selection, decision, nil
			}
			if (requiredTransport == egress.OpenAIUpstreamTransportAny ||
				requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE ||
				s.isOpenAIProviderTransportCompatible(selection.Provider, requiredTransport)) &&
				providerSupportsOpenAICapabilities(ctx, selection.Provider, requiredCapability, requiredImageCapability) {
				return selection, decision, nil
			}
			// Transport or image capability rejected a target after basic acquisition.
			// Release its slot before excluding it and trying another group member.
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
			if effectiveExcludedIDs == nil {
				effectiveExcludedIDs = make(map[int64]struct{})
			}
			if _, exists := effectiveExcludedIDs[selection.Provider.Record.ID]; exists {
				return nil, decision, schedulercore.ErrNoAvailableProviders
			}
			effectiveExcludedIDs[selection.Provider.Record.ID] = struct{}{}
		}
	}

	if s.CheckGroupModelRestriction(ctx, groupID, requestedModel) {
		slog.Warn("group model restriction blocked request",
			"group_id", derefGroupID(groupID),
			"model", requestedModel)
		return nil, decision, fmt.Errorf("%w supporting model: %s (group model restriction)", schedulercore.ErrNoAvailableProviders, requestedModel)
	}

	var stickyProviderID int64
	if sessionHash != "" && s.cache != nil {
		// Missing affinity is optional for advanced ordering. Cache read failure
		// leaves this pass's sticky ID zero so ordinary candidate scoring can proceed.
		if providerID, err := s.getStickySessionProviderID(ctx, groupID, sessionHash); err == nil && providerID > 0 {
			stickyProviderID = providerID
		}
	}
	effectiveSettings := s.advancedSchedulerEffectiveSettingsForRequest(ctx, groupID)
	stickyWeighted := effectiveSettings.StickyWeightedEnabled
	subscriptionPriority := effectiveSettings.SubscriptionPriorityEnabled
	stickyPreviousProviderID := int64(0)
	if stickyWeighted && previousResponseCanMove && strings.TrimSpace(previousResponseID) != "" && (platform == "" || platform == capability.PlatformOpenAI) {
		stickyPreviousProviderID = s.ResolveProviderIDByPreviousResponseIDForScheduler(ctx, groupID, previousResponseID, routingModel, excludedIDs, requiredCapability, requireCompact)
	}

	selection, decision, selectErr := scheduler.Select(ctx, schedulercore.PlatformSelectionInput{
		GroupID:                  groupID,
		Platform:                 platform,
		SessionHash:              sessionHash,
		StickyProviderID:         stickyProviderID,
		GuardianParentProviderID: guardianParentProviderID,
		StickyPreviousProviderID: stickyPreviousProviderID,
		StickyWeighted:           stickyWeighted,
		SubscriptionPriority:     subscriptionPriority,
		PreserveStickyBinding:    preserveGuardianParentBinding,
		RequirePrivacySet:        s.openAIGroupRequiresPrivacySet(ctx, groupID),
		PreviousResponseID:       previousResponseID,
		PreviousResponseCanMove:  previousResponseCanMove,
		RequestedModel:           requestedModel,
		RoutingModel:             routingModel,
		RequiredTransport:        string(requiredTransport),
		RequiredCapability:       requiredCapability,
		RequiredImageCapability:  requiredImageCapability,
		RequireCompact:           requireCompact,
		ExcludedIDs:              excludedIDs,
	})
	if selection != nil {
		selection.AdvancedScheduler = true
		feedback := effectiveSettings.Feedback
		selection.AdvancedSchedulerFeedback = &feedback
	}
	return selection, decision, selectErr
}

// providerSupportsOpenAICapabilities checks endpoint requirements before image
// capabilities for a nonnil target. Grok image requests use its
// media-generation eligibility; other targets use the provider's configured
// OpenAI image capability rule.
func providerSupportsOpenAICapabilities(ctx context.Context, provider *gatewayprovider.ExecutionProvider, requiredCapability providercore.OpenAIEndpointCapability, requiredImageCapability providercore.OpenAIImagesCapability) bool {
	if provider == nil {
		return false
	}
	if !gatewayprovider.SupportsRequestCapability(ctx, provider, requiredCapability) {
		return false
	}
	if requiredImageCapability != "" && provider.View().IsGrok() {
		return gatewayprovider.SupportsRequestCapability(ctx, provider, providercore.OpenAIEndpointCapabilityGrokMediaGeneration)
	}
	return provider.View().SupportsOpenAIImageCapability(requiredImageCapability)
}

// cloneExcludedProviderIDs returns an independent map of excluded provider IDs.
// Empty input returns nil, while populated maps can receive retry exclusions
// without changing the set supplied by the caller.
func cloneExcludedProviderIDs(excludedIDs map[int64]struct{}) map[int64]struct{} {
	if len(excludedIDs) == 0 {
		return nil
	}
	cloned := make(map[int64]struct{}, len(excludedIDs))
	for id := range excludedIDs {
		cloned[id] = struct{}{}
	}
	return cloned
}

// isOpenAIProviderTransportCompatible accepts Any and HTTP/SSE or resolves a
// target's required transport. Responses WS ingress also permits Grok and
// eligible Responses protocol conversion, while other transports use the egress
// decision.
func (s *Compatible) isOpenAIProviderTransportCompatible(provider *gatewayprovider.ExecutionProvider, requiredTransport egress.OpenAIUpstreamTransport) bool {
	if requiredTransport == egress.OpenAIUpstreamTransportAny || requiredTransport == egress.OpenAIUpstreamTransportHTTPSSE {
		return true
	}
	if s == nil || provider == nil {
		return false
	}
	if requiredTransport == egress.OpenAIUpstreamTransportResponsesWebsocketV2Ingress {
		if provider.View().IsGrok() {
			return true
		}
		return provider.Route.Protocol() == protocol.ProtocolOpenAIResponses || provider.View().SupportsResponsesWS() ||
			(provider.View().IsOpenAI() && slices.Contains(provider.View().UpstreamProtocols(), capability.ProtocolOpenAIResponses))
	}
	return s.ResolveTransport(provider).Transport == requiredTransport
}

// ReportOpenAIProviderScheduleResult accepts a target or integer ID and returns
// whether an observed health failure was handled. Unsupported values and absent
// IDs return false; callers supply a constructed selector for feedback and
// recovery state.
//
// Local model-attribute errors return before health or performance feedback.
// A successful observation resets credential retry tracking and clears this
// model's transient state. A target can also update the bound API Key health
// observer from the first supplied error, then report picker feedback when a
// health observer exists. Integer-only reporting uses the basic feedback path,
// which clears successful model state but does not publish advanced statistics.
func (s *Compatible) ReportOpenAIProviderScheduleResult(providerOrID any, model string, success bool, ttftMs *int, observedErr ...error) bool {
	if !success && len(observedErr) > 0 && routing.IsModelAttributeError(observedErr[0]) {
		return false
	}
	var provider *gatewayprovider.ExecutionProvider
	var providerID int64
	switch value := providerOrID.(type) {
	case *gatewayprovider.ExecutionProvider:
		provider = value
		if provider != nil {
			providerID = provider.Record.ID
		}
	case int64:
		providerID = value
	case int:
		providerID = int64(value)
	}
	if provider == nil && providerID == 0 {
		return false
	}

	healthTripped := false
	if provider != nil && s != nil && s.healthObserver != nil {
		if !success && len(observedErr) > 0 && observedErr[0] != nil {
			healthTripped = s.ObserveOpenAIProviderHealthFailure(context.Background(), provider, observedErr[0])
		}
	}
	// Successful dispatch provides current evidence that clears earlier retry and
	// model-failure observations before future eligibility checks reuse this provider.
	if success {
		s.runtimeBlockState().ResetRetry(providerID)
		s.clearOpenAIProviderModelTransientState(providerID, providercore.NormalizeTransientModel(model))
	}
	if provider == nil {
		s.reportOpenAIProviderScheduleResult(false, providerID, model, success, ttftMs)
		return healthTripped
	}
	if s == nil || s.healthObserver == nil {
		return healthTripped
	}
	scheduler := s.ensureOpenAIProviderScheduler()
	if scheduler == nil {
		return healthTripped
	}
	scheduler.ReportResult(providerID, success, ttftMs)
	return healthTripped
}

// ObserveOpenAIProviderHealthFailure classifies a supplied failure and
// delegates applicable API Key health action. Missing inputs or observer
// returns false; the execution target then receives the record's observed
// temporary scheduling fields.
func (s *Compatible) ObserveOpenAIProviderHealthFailure(ctx context.Context, provider *gatewayprovider.ExecutionProvider, observedErr error) bool {
	if s == nil || s.healthObserver == nil || provider == nil || observedErr == nil {
		return false
	}
	status, body, eligible := gatewayprovider.ClassifyOpenAIAPIKeyHealthFailure(observedErr)
	record := gatewayprovider.ExecutionRecord(provider)
	handled := s.healthObserver.Core.ApplyAPIKeyHealthFailure(ctx, record, status, body, eligible)
	provider.Record.TempUnschedulableUntil = record.TempUnschedulableUntil
	provider.Record.TempUnschedulableReason = record.TempUnschedulableReason
	return handled
}

// ReportOpenAIProviderScheduleResultForSelection uses captured feedback alphas
// when the result is advanced and carries them. Successful basic observations
// still clear transient model state, while only advanced observations update
// the picker statistics.
func (s *Compatible) ReportOpenAIProviderScheduleResultForSelection(selection *gatewayprovider.SelectionResult, providerID int64, model string, success bool, ttftMs *int) {
	if selection != nil && selection.AdvancedScheduler && selection.AdvancedSchedulerFeedback != nil {
		s.reportOpenAIProviderScheduleResult(true, providerID, model, success, ttftMs, *selection.AdvancedSchedulerFeedback)
		return
	}
	s.reportOpenAIProviderScheduleResult(selection != nil && selection.AdvancedScheduler, providerID, model, success, ttftMs)
}

// reportOpenAIProviderScheduleResult clears successful model cooldown state
// before optional advanced feedback. Basic observations finish after cleanup;
// advanced observations use the shared picker when available. Supplied feedback
// coefficients apply to this observation, while absent coefficients use the
// scheduler defaults and nil TTFT latency skips the TTFT sample.
func (s *Compatible) reportOpenAIProviderScheduleResult(advanced bool, providerID int64, model string, success bool, ttftMs *int, feedback ...policy.FeedbackConfig) {
	// Successful basic requests also recover provider-model eligibility, so clear
	// the cooldown before deciding whether advanced performance statistics apply.
	if success {
		s.clearOpenAIProviderModelTransientState(providerID, providercore.NormalizeTransientModel(model))
	}
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIProviderScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportResult(providerID, success, ttftMs, feedback...)
}

// RecordOpenAIProviderSwitch records a picker switch when the compatible health
// observer is bound. Nil selectors or absent observers are ignored, and the
// shared picker is initialized for the accepted metric call.
func (s *Compatible) RecordOpenAIProviderSwitch() {
	if s == nil || s.healthObserver == nil {
		return
	}
	scheduler := s.ensureOpenAIProviderScheduler()
	if scheduler != nil {
		scheduler.ReportSwitch()
	}
}

// RecordOpenAIProviderSwitchForSelection records a switch for a result marked
// as advanced. Missing or basic results are ignored so provider switches from
// those requests do not enter the advanced picker counters.
func (s *Compatible) RecordOpenAIProviderSwitchForSelection(selection *gatewayprovider.SelectionResult) {
	s.recordOpenAIProviderSwitch(selection != nil && selection.AdvancedScheduler)
}

// recordOpenAIProviderSwitch delegates one accepted advanced switch to the
// shared picker. A false advanced flag or unavailable picker is ignored,
// preserving the caller's distinction between basic and advanced observations.
func (s *Compatible) recordOpenAIProviderSwitch(advanced bool) {
	if !advanced {
		return
	}
	scheduler := s.ensureOpenAIProviderScheduler()
	if scheduler == nil {
		return
	}
	scheduler.ReportSwitch()
}

// SnapshotOpenAIProviderSchedulerMetrics initializes the shared picker when
// necessary and returns its metric value. An unavailable picker produces an
// empty snapshot, while construction supplies the feedback store used in its
// provider count.
func (s *Compatible) SnapshotOpenAIProviderSchedulerMetrics() schedulercore.PlatformMetricsSnapshot {
	scheduler := s.ensureOpenAIProviderScheduler()
	if scheduler == nil {
		return schedulercore.PlatformMetricsSnapshot{}
	}
	return scheduler.SnapshotMetrics()
}

// SessionStickyTTL returns the positive configured lifetime for compatible
// session bindings. Nil selectors or nonpositive startup values use one hour,
// letting sticky callers obtain a usable default without normalizing options
// here.
func (s *Compatible) SessionStickyTTL() time.Duration {
	if s != nil &&
		s.options.StickyTTL >
			0 {
		return s.options.StickyTTL
	}
	return time.Hour
}

// selectBySessionHash delegates the shared sticky selection path using a copied
// exclusion map. It restores the execution target through this call's scope and
// returns the core's escape indicator and selection error.
func (s *compatiblePicker) selectBySessionHash(ctx context.Context, req schedulercore.PlatformSelectionInput) (*gatewayprovider.SelectionResult, bool, error) {
	core, scope := s.platformSelector()
	value, escaped, err := core.SelectBySessionHash(ctx, cloneSelectionInput(req))
	return scope.restore(value), escaped, err
}

// openAIQuotaHeadroomFactor delegates Codex quota-headroom scoring through the
// current execution record. Provider rules apply the neutral value, snapshot
// validity, reset handling, and secondary-window discount using the supplied
// observation time.
func openAIQuotaHeadroomFactor(value *gatewayprovider.ExecutionProvider, now time.Time) float64 {
	return providercore.OpenAIQuotaHeadroomFactor(gatewayprovider.ExecutionRecord(value), now)
}

// requestRoutingModel returns a nonblank trimmed routing identity when one is
// supplied. Otherwise the client RequestedModel passes through unchanged so
// callers can still resolve the target's provider-layer model policy.
func requestRoutingModel(input schedulercore.PlatformSelectionInput) string {
	if model := strings.TrimSpace(input.RoutingModel); model != "" {
		return model
	}
	return input.RequestedModel
}

// cloneSelectionInput copies the selection value and clones its exclusion map
// for picker use. Other referenced inputs retain their ownership, while retry
// additions cannot mutate the caller's original exclusion map.
func cloneSelectionInput(input schedulercore.PlatformSelectionInput) schedulercore.PlatformSelectionInput {
	input.ExcludedIDs = maps.Clone(input.ExcludedIDs)
	return input
}
