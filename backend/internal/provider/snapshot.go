package provider

import (
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// snapshot.go captures provider routing data and resolves one model attempt.
// Candidate checks need provider identity and eligibility while credentials
// stay in the execution record. RoutingSnapshot copies configured aliases,
// enabled protocols, per-model protocol declarations, and optional identity
// pointers before a route is selected. Adapter callers add their platform
// mapping defaults to that captured configuration.
//
// Model resolution performs one alias hop and selects a protocol from the
// resulting identity. Most platforms accept every enabled protocol for every
// model, so their ModelProtocols table is nil. A platform whose models each
// declare one protocol fills the table, and the final model then narrows the
// enabled choices. Execution consumers retain the resolved model and protocol
// together; retries resolve a fresh snapshot, and structural capability checks
// use the separate Protocols view when no model has been chosen.

// ProviderSnapshot holds provider identity, eligibility, and runtime selection data.
type ProviderSnapshot struct {
	// ModelPolicy captures configured aliases before protocol resolution. Adapter
	// callers replace it with platform defaults when their execution policy supplies them.
	ModelPolicy ModelRoutingSnapshot `json:"-"`
	ID          int64                // provider record identifier
	// ParentProviderID retains optional parent identity. RoutingSnapshot copies
	// its value so editing this pointer does not change the source record's parent.
	ParentProviderID *int64 // optional credential parent identifier
	Platform         string // configured upstream platform identity
	Type             string // configured provider authentication type
	AuthMode         string // resolved protocol authentication mode
	// ModelProtocols maps exact final models to their single declared protocol.
	// Nil means protocol choice is model independent; a nonnil table rejects
	// models it does not list. RoutingSnapshot gives each snapshot its own copy.
	ModelProtocols map[string]capability.ProtocolID
	// EnabledProtocols is resolved and copied by RoutingSnapshot; Protocols
	// copies it again before returning a capability view to its caller.
	EnabledProtocols []capability.ProtocolID // resolved upstream protocol choices
	Status           string                  // stored provider lifecycle status
	Schedulable      bool                    // configured candidate scheduling flag
	Concurrency      int                     // configured request concurrency limit
	RequestIDHeader  string                  `json:"-"` // selected diagnostic response header
	Priority         int                     // configured candidate ordering priority
	// ExpiresAt retains the optional stored expiry. RoutingSnapshot copies that
	// value so changing the snapshot's timestamp leaves the source record unchanged.
	ExpiresAt *time.Time // optional provider expiry time
}

// ResolvedModelRoute binds the outgoing model and protocol selected from one snapshot.
// Consumers send Model directly and resolve a fresh result after selecting a
// different provider or configuration, including retries that change service mode.
type ResolvedModelRoute struct {
	Model                string                // final upstream model identity
	Protocol             capability.ProtocolID // selected upstream execution protocol
	MappingMatched       bool                  // provider alias match indicator
	EffortMappingMatched bool                  // effort-scoped override match indicator
}

// RoutingSnapshot copies selection fields with protocols resolved from the
// record's configuration. Nil records return a zero snapshot; parent, expiry,
// and protocol values are detached. Every platform captures its saved aliases
// without adapter defaults. Keep the source record stable during capture so its identity and protocol
// fields describe the same configuration.
func (r *Record) RoutingSnapshot() ProviderSnapshot {
	if r == nil {
		return ProviderSnapshot{}
	}
	snapshot := ProviderSnapshot{
		ID: r.ID, Platform: r.Platform, Type: r.Type, AuthMode: ProtocolAuthMode(r),
		ModelProtocols:   r.ModelProtocols(),
		EnabledProtocols: slices.Clone(r.UpstreamProtocols()), Status: r.Status, Schedulable: r.Schedulable,
		Concurrency: r.Concurrency, Priority: r.Priority, RequestIDHeader: UpstreamRequestIDHeaderName(r),
	}
	// Core snapshots capture saved mappings; execution adapters install defaults
	// for platforms such as Antigravity before resolving an executable attempt.
	snapshot.ModelPolicy = NewModelRoutingSnapshotWithEffort(
		ResolveModelMapping(r, ModelMappingDefaults{}),
		ResolveEffortModelMapping(r),
	)
	if r.ParentProviderID != nil {
		id := *r.ParentProviderID
		snapshot.ParentProviderID = &id
	}
	if r.ExpiresAt != nil {
		at := *r.ExpiresAt
		snapshot.ExpiresAt = &at
	}
	return snapshot
}

// Protocols returns structural capabilities with copied enabled protocols.
// Callers use ProtocolsForModel for executable model checks. Nil and empty
// enabled selections retain their meaning during capability resolution.
func (s ProviderSnapshot) Protocols() capability.ProviderProtocols {
	return capability.ProviderProtocols{Platform: s.Platform, Type: s.Type, AuthMode: s.AuthMode, Enabled: slices.Clone(s.EnabledProtocols)}
}

// ProtocolsForModel returns capabilities narrowed to an already mapped model.
// A snapshot with per-model declarations exposes only the model's enabled
// declared protocol, returning an empty selection for unknown or disabled
// models. Callers must pass the final one-hop identity.
func (s ProviderSnapshot) ProtocolsForModel(model string) capability.ProviderProtocols {
	result := s.Protocols()
	if s.ModelProtocols != nil {
		target, known := s.ModelProtocols[model]
		result.Enabled = []capability.ProtocolID{}
		if known && slices.Contains(s.EnabledProtocols, target) {
			result.Enabled = []capability.ProtocolID{target}
		}
	}
	return result
}

// ResolveModelRoute selects the final model and protocol from this snapshot
// without a request reasoning effort. It delegates to ResolveModelRouteForEffort
// with an empty effort so effort-free probes and legacy callers reuse the same
// protocol selection path.
func (s ProviderSnapshot) ResolveModelRoute(requested string, source capability.ProtocolID, fallbacks map[capability.ProtocolID][]capability.ProtocolID) (ResolvedModelRoute, bool) {
	return s.ResolveModelRouteForEffort(requested, "", source, fallbacks)
}

// ResolveModelRouteForEffort selects the final model and protocol from this
// snapshot using an optional request reasoning effort. Effort-scoped overrides
// take precedence over ordinary mappings in a single hop, and a missing source
// selects the first enabled native protocol for automatic model probes.
//
// Per-model declarations narrow native choices to the resolved model's protocol
// before client conversion policy applies. Unknown models and explicitly
// disabled protocols then fail even when the provider supports the protocol
// generally. Callers keep the returned values together during an attempt and
// resolve again against a fresh snapshot when retrying another configuration.
func (s ProviderSnapshot) ResolveModelRouteForEffort(requested, effort string, source capability.ProtocolID, fallbacks map[capability.ProtocolID][]capability.ProtocolID) (ResolvedModelRoute, bool) {
	model, matched, effortMatched := s.ModelPolicy.ResolveForEffort(requested, effort)
	protocols := s.ProtocolsForModel(model)
	if source == "" {
		for _, target := range protocols.Enabled {
			if slices.Contains(capability.NativeProtocolOptions(s.Platform, s.Type, s.AuthMode), target) {
				source = target
				break
			}
		}
	}
	target, ok := capability.ResolveRoute(protocols, source, fallbacks)
	if !ok {
		return ResolvedModelRoute{}, false
	}
	return ResolvedModelRoute{
		Model:                model,
		Protocol:             target,
		MappingMatched:       matched,
		EffortMappingMatched: effortMatched,
	}, true
}
