package selection

import (
	"context"
	"slices"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	schedulercore "github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
)

// diagnostic_ports.go converts provider and group records into scheduler
// diagnostics while retaining their per-invocation identities.
// Diagnostic scoring can read several provider or group records with the same
// persisted ID during one call. Each conversion allocates a separate invocation
// identifier, keeping the record used by a filtering callback associated with
// the metadata it evaluates. Credential-bearing execution targets stay in
// gateway lookup maps.
//
// The scheduler receives diagnostic values with copied group memberships and
// cloned advanced overrides. Some time pointers still refer to the source
// record, so the diagnostic core treats those observations as read-only.
// Eligibility and quota callbacks recover their target by conversion ID, while
// group callbacks recover the corresponding policy source. A new scope belongs
// to each overview or detail invocation, and the core controls calls into its
// unsynchronized maps.

// diagnosticScope retains provider and group references for one diagnostic
// invocation. Callers serialize conversions and callbacks because its counter
// and lookup maps are mutated without synchronization.
type diagnosticScope struct {
	source DiagnosticSource // bound diagnostic reader
	// next allocates identifiers shared by providerValues and groupValues in this
	// invocation. Both maps retain distinct conversions even when persisted IDs are equal.
	next           uint64                                        // per-call association counter
	providerValues map[uint64]*gatewayprovider.ExecutionProvider // associated execution target pointers
	groupValues    map[uint64]*routing.Group                     // associated policy group pointers
}

// diagnosticCore constructs a diagnostic service with one new target
// association scope. A missing source stays absent in the core, allowing its
// unavailable-source handling to report the construction limitation.
//
// The adapter binds production feedback, parameter resolution, eligibility
// preparation, hard-filter reasons, and quota-headroom observations to that
// scope. Every conversion of a record receives its own ID so callbacks can
// recover the exact provider or group input used for that diagnostic value. The
// returned core reads shared runtime services while producing diagnostic
// metadata.
func (s *Diagnostics) diagnosticCore() (*schedulercore.DiagnosticService, *diagnosticScope) {
	scope := &diagnosticScope{source: s.source, providerValues: map[uint64]*gatewayprovider.ExecutionProvider{}, groupValues: map[uint64]*routing.Group{}}
	var source schedulercore.DiagnosticSource
	if s.source != nil {
		source = scope
	}
	core := schedulercore.NewDiagnosticService(source, s.concurrencyService, schedulercore.DiagnosticPorts{
		Now: time.Now,
		Stats: func() *schedulercore.RuntimeStats {
			return s.feedback
		},
		Effective: func(ctx context.Context, g *schedulercore.DiagnosticGroup) (policy.EffectiveSettings, policy.RuntimeSettings) {
			effective, runtime := s.effectiveSettings(ctx, scope.originalGroup(g))
			return effective, runtime
		},
		Prepare: func(ctx context.Context, g *schedulercore.DiagnosticGroup, providers []schedulercore.DiagnosticProvider) context.Context {
			var values []gatewayprovider.ExecutionProvider
			if providers != nil {
				values = make([]gatewayprovider.ExecutionProvider, len(providers))
				for i, v := range providers {
					values[i] = *scope.providerValues[v.ProjectionID]
				}
			}
			return s.prepareEligibilityContext(ctx, scope.originalGroup(g), values)
		},
		Filter: func(ctx context.Context, a *schedulercore.DiagnosticProvider, g *schedulercore.DiagnosticGroup, request schedulercore.AdvancedSchedulerScoreDiagnosticRequest, now time.Time) string {
			return s.diagnosticPlatformFilterReason(ctx, scope.providerValues[a.ProjectionID], scope.originalGroup(g), request, now)
		},
		Quota: func(id uint64, now time.Time) float64 {
			return openAIQuotaHeadroomFactor(scope.providerValues[id], now)
		},
	})
	return core, scope
}

// originalGroup returns the group reference registered for a diagnostic value's
// per-call ID. Nil input returns nil; callers supply a value converted by the
// same scope to retain the intended policy association.
func (s *diagnosticScope) originalGroup(g *schedulercore.DiagnosticGroup) *routing.Group {
	if g == nil {
		return nil
	}
	return s.groupValues[g.ProjectionID]
}

// group registers the source group and returns diagnostic fields under a new
// per-call identifier. Nil input returns nil, and repeated conversions of the
// same persisted group keep separate lookup entries.
//
// The metadata copies identity and scheduling flags and clones advanced
// overrides through accessview, while the source pointer remains available to
// policy callbacks. This lets effective-setting resolution find the group that
// produced each diagnostic record instead of assuming a later read with the
// same persisted ID is interchangeable.
func (s *diagnosticScope) group(v *routing.Group) *schedulercore.DiagnosticGroup {
	if v == nil {
		return nil
	}
	// A later group read may retain the same persisted ID with different settings.
	// Register its own association before the core resolves diagnostic parameter overrides.
	s.next++
	id := s.next
	s.groupValues[id] = v
	return &schedulercore.DiagnosticGroup{ProjectionID: id, ID: v.ID, Name: v.Name, SortOrder: v.SortOrder, Advanced: v.UsesAdvancedScheduler(), RequireOAuthOnly: v.RequireOAuthOnly, RequirePrivacySet: v.RequirePrivacySet, AdvancedSchedulerOverrides: accessview.CloneGroupAdvancedSchedulerOverrides(v.AdvancedSchedulerOverrides)}
}

// provider registers an execution target and returns credential-free diagnostic
// fields with a new per-call identifier. Nil input returns nil, and source
// pointers remain in the scope for platform filtering and quota callbacks.
//
// Group IDs are cloned, and both group membership representations are converted
// while retaining nil-versus-empty slice shape. Scalar observations are copied,
// but expiration, cooldown, and window time pointers remain shared with the
// source record. The core reads these observations while callbacks recover the
// exact associated target, including same-ID records from distinct reads.
func (s *diagnosticScope) provider(v *gatewayprovider.ExecutionProvider) *schedulercore.DiagnosticProvider {
	if v == nil {
		return nil
	}
	// Diagnostic callbacks need the record that produced this metadata, including
	// distinct same-ID reads. Register the target before exposing its conversion identifier.
	s.next++
	id := s.next
	s.providerValues[id] = v
	a := &schedulercore.DiagnosticProvider{ProjectionID: id, ID: v.Record.ID, Name: v.Record.Name, Platform: v.Record.Platform, Type: v.Record.Type, Status: v.Record.Status, Priority: v.Record.Priority, LoadFactor: v.View().EffectiveLoadFactor(), Schedulable: v.Record.Schedulable, AutoPauseOnExpired: v.Record.AutoPauseOnExpired, IsOAuth: v.View().IsOAuth(), PrivacySet: v.View().IsPrivacySet(), SubscriptionPriority: v.View().IsOpenAIChatGPTSubscription(), ExpiresAt: v.Record.ExpiresAt, OverloadUntil: v.Record.OverloadUntil, RateLimitResetAt: v.Record.RateLimitResetAt, TempUnschedulableUntil: v.Record.TempUnschedulableUntil, SessionWindowEnd: v.Record.SessionWindowEnd, GroupIDs: slices.Clone(v.Record.GroupIDs)}
	if v.Record.ProviderGroups != nil {
		a.ProviderGroups = make([]schedulercore.DiagnosticProviderGroup, len(v.Record.ProviderGroups))
		for i, g := range v.Record.ProviderGroups {
			a.ProviderGroups[i] = schedulercore.DiagnosticProviderGroup{Group: s.group((*routing.Group)(g.Group))}
		}
	}
	if v.Record.Groups != nil {
		a.Groups = make([]*schedulercore.DiagnosticGroup, len(v.Record.Groups))
		for i, g := range v.Record.Groups {
			a.Groups[i] = s.group((*routing.Group)(g))
		}
	}
	return a
}

// providerSlice converts candidate values and registers each source element in
// this invocation's lookup. Nil input remains nil, while populated results use
// new diagnostic slice storage and retain target pointers for callbacks.
func (s *diagnosticScope) providerSlice(values []gatewayprovider.ExecutionProvider) []schedulercore.DiagnosticProvider {
	if values == nil {
		return nil
	}
	out := make([]schedulercore.DiagnosticProvider, len(values))
	for i := range values {
		out[i] = *s.provider(&values[i])
	}
	return out
}

// GetProvider reads the source execution target and registers its diagnostic
// conversion in this scope. Nil source results remain nil, and the original
// read error accompanies the converted value for the core.
func (s *diagnosticScope) GetProvider(ctx context.Context, id int64) (*schedulercore.DiagnosticProvider, error) {
	v, err := s.source.GetProvider(ctx, id)
	return s.provider(v), err
}

// GetGroup reads the source group and registers its diagnostic conversion in
// this invocation. A nil group stays nil, and the source error passes through
// to the diagnostic core using this adapter.
func (s *diagnosticScope) GetGroup(ctx context.Context, id int64) (*schedulercore.DiagnosticGroup, error) {
	v, err := s.source.GetGroup(ctx, id)
	return s.group(v), err
}

// ListProvidersForSchedulerScoreFilter delegates the unpaged diagnostic pool
// read with all supplied filters. Every returned element receives a scope
// association, while the source error and nil slice shape pass through.
func (s *diagnosticScope) ListProvidersForSchedulerScoreFilter(ctx context.Context, platform, kind, status, search string, groupID int64, privacy string) ([]schedulercore.DiagnosticProvider, error) {
	v, err := s.source.ListProvidersForSchedulerScoreFilter(ctx, platform, kind, status, search, groupID, privacy)
	return s.providerSlice(v), err
}

// ListSchedulableProvidersForAdvancedSchedulerScore delegates candidate reads
// for the supplied group and platform. Results receive invocation associations
// before scoring, while source errors and nil slice shape pass through to the
// diagnostic core.
func (s *diagnosticScope) ListSchedulableProvidersForAdvancedSchedulerScore(ctx context.Context, groupID *int64, platform string) ([]schedulercore.DiagnosticProvider, error) {
	v, err := s.source.ListSchedulableProvidersForAdvancedSchedulerScore(ctx, groupID, platform)
	return s.providerSlice(v), err
}
