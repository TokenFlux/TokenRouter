package provider

import (
	"maps"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

// model_routing_snapshot.go captures a private model mapping and optional
// effort-scoped overrides for subsequent request resolution.
// A request's model mapping stays fixed while its provider configuration can
// be edited elsewhere. Capturing the mappings detaches string entries from the
// source maps and pivots effort rules by canonical effort label so resolution
// looks up the request's effort slice directly before falling back to ordinary
// one-hop model aliases.

// ModelRoutingSnapshot retains copied model aliases and effort-scoped model
// overrides for subsequent request matching.
type ModelRoutingSnapshot struct {
	// mapping and effortMapping hold constructor-copied one-hop alias tables
	// read by Resolve and ResolveForEffort. effortMapping is indexed by
	// canonical reasoning effort label to source-model pattern to target model.
	// A zero snapshot has nil tables and resolves requests as unmatched.
	mapping       map[string]string            // retained model alias mapping
	effortMapping map[string]map[string]string // canonical effort -> source -> target
}

// NewModelRoutingSnapshot copies an ordinary model mapping for subsequent
// request matching. Nil and empty inputs retain their collection shape, and
// later source-map edits cannot change the captured string entries used by
// Resolve. Keep the input map stable until copying finishes.
func NewModelRoutingSnapshot(mapping map[string]string) ModelRoutingSnapshot {
	return ModelRoutingSnapshot{mapping: maps.Clone(mapping)}
}

// NewModelRoutingSnapshotWithEffort copies ordinary model mappings alongside
// effort-scoped overrides for request matching. Effort entries are pivoted by
// canonical effort label into independent nested maps, so later mutations to
// caller-owned maps cannot alter captured resolution results.
func NewModelRoutingSnapshotWithEffort(mapping map[string]string, effortMapping EffortModelMapping) ModelRoutingSnapshot {
	return ModelRoutingSnapshot{
		mapping:       maps.Clone(mapping),
		effortMapping: groupEffortMappingByEffort(effortMapping),
	}
}

// Resolve applies the captured one-hop ordinary model mapping and reports a
// match. Exact and wildcard precedence follows ResolveMappedModel. A miss
// returns requested unchanged with false. A match returns its stored target
// with true, including empty and identity targets, so callers use the flag to
// distinguish a match from an unchanged request.
func (s ModelRoutingSnapshot) Resolve(requested string) (string, bool) {
	mapped, matched, _ := s.ResolveForEffort(requested, "")
	return mapped, matched
}

// ResolveForEffort applies captured effort-scoped overrides before falling back
// to ordinary model mappings. A recognized nonblank effort checks its effort
// table first using ResolveMappedModel precedence; on miss or empty effort, it
// resolves against the ordinary mapping. The third return reports whether an
// effort-scoped rule produced the match.
func (s ModelRoutingSnapshot) ResolveForEffort(requested, effort string) (mapped string, matched bool, effortMatched bool) {
	normalized := protocol.NormalizeReasoningEffort(effort)
	if normalized != "" && len(s.effortMapping) > 0 {
		if subset := s.effortMapping[normalized]; len(subset) > 0 {
			if target, ok := ResolveMappedModel(subset, requested); ok {
				return target, true, true
			}
		}
	}
	target, ok := ResolveMappedModel(s.mapping, requested)
	return target, ok, false
}

// HasEffortMapping reports whether this snapshot captured any effort-scoped
// model override rules.
func (s ModelRoutingSnapshot) HasEffortMapping() bool {
	return len(s.effortMapping) > 0
}

// MatchedEfforts returns every canonical effort label in deterministic order
// that matches requested in the captured effort overrides. Unmatched requests
// or empty effort tables return nil.
func (s ModelRoutingSnapshot) MatchedEfforts(requested string) []string {
	if len(s.effortMapping) == 0 || requested == "" {
		return nil
	}
	var matched []string
	for _, effort := range protocol.AllReasoningEfforts() {
		subset := s.effortMapping[effort]
		if len(subset) == 0 {
			continue
		}
		if _, ok := ResolveMappedModel(subset, requested); ok {
			matched = append(matched, effort)
		}
	}
	return matched
}

// PossibleTargets returns distinct upstream targets reachable for requested
// across ordinary mapping and every matching effort override, ordered with the
// ordinary target first followed by canonical effort order. Duplicate spellings
// are omitted; an empty requested name returns nil.
func (s ModelRoutingSnapshot) PossibleTargets(requested string) []string {
	if requested == "" {
		return nil
	}
	plain, _ := ResolveMappedModel(s.mapping, requested)
	targets := make([]string, 0, 1+len(s.effortMapping))
	seen := make(map[string]struct{}, 1+len(s.effortMapping))
	add := func(model string) {
		if _, exists := seen[model]; exists {
			return
		}
		seen[model] = struct{}{}
		targets = append(targets, model)
	}
	add(plain)
	for _, effort := range protocol.AllReasoningEfforts() {
		subset := s.effortMapping[effort]
		if len(subset) == 0 {
			continue
		}
		if target, ok := ResolveMappedModel(subset, requested); ok {
			add(target)
		}
	}
	return targets
}
