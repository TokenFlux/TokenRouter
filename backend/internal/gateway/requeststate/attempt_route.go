package requeststate

import (
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// attempt_route.go resolves provider protocol attempts from request routing
// state and retains the model selected for each admitted attempt.
// Provider capabilities can change between selections even when the request
// keeps its group plan. Candidate resolution therefore rechecks the current
// provider snapshot against the client protocol before a later attempt uses a
// route.
//
// A saved plan is usable only while its group matches the captured group.
// Without a saved plan, an existing attempt protocol can be retained for
// compatibility, otherwise the state builds a capability route from the current
// group. Only an admitted saved plan publishes a CandidatePlan to callers;
// that candidate retains the final model and metadata identity with its
// protocol. Legacy calls without a model plan resolve supplied mappings when
// requested. A new candidate reads a fresh snapshot before execution, so a
// retry can observe changed aliases without altering an in-flight attempt.

// AttemptRoute records one candidate's protocol and optional admitted model
// plan.
type AttemptRoute struct {
	// candidate is meaningful when planned is true; an unplanned compatibility
	// attempt can retain protocol alone. Captured candidate values are returned
	// by value when an admitted route supplied them.
	protocol  protocol.ProtocolID   // resolved upstream protocol identifier
	candidate routing.CandidatePlan // captured admitted candidate plan
	planned   bool                  // candidate plan presence flag
}

// Protocol returns the upstream protocol selected for this attempt.
func (a AttemptRoute) Protocol() protocol.ProtocolID { return a.protocol }

// Candidate returns the captured candidate by value and reports whether an
// admitted plan supplied it. An unplanned compatibility route can still have a
// protocol while this result remains absent.
func (a AttemptRoute) Candidate() (routing.CandidatePlan, bool) {
	return a.candidate, a.planned
}

// EffortMappingMatched reports whether this attempt's admitted candidate
// resolved its upstream model through a provider effort_model_mapping rule.
func (a AttemptRoute) EffortMappingMatched() bool {
	return a.planned && a.candidate.EffortMappingMatched
}

// ResolveAttempt rechecks the current provider's enabled protocol route while
// preserving any reasoning effort already captured on the saved route plan.
func (s RoutingState) ResolveAttempt(snapshot provider.ProviderSnapshot, previous AttemptRoute) (AttemptRoute, bool, error) {
	return s.resolveAttempt(snapshot, previous, "", false)
}

// ResolveAttemptForEffort rechecks the current provider's enabled protocol
// route with an explicit reasoning effort override and returns the resolved
// attempt with true when resolution occurs. Passing an empty effort explicitly
// clears any effort captured on the saved route plan so post-selection rewrites
// can fall back to ordinary model mappings.
func (s RoutingState) ResolveAttemptForEffort(snapshot provider.ProviderSnapshot, previous AttemptRoute, effort string) (AttemptRoute, bool, error) {
	return s.resolveAttempt(snapshot, previous, effort, true)
}

// resolveAttempt rechecks the current provider's enabled protocol route with an
// optional reasoning effort override and returns the resolved attempt with true
// when resolution occurs. A saved plan whose group differs from the captured
// group is discarded for this decision.
//
// Without a usable saved plan, an already selected protocol is retained with
// false, as is any attempt lacking a client protocol. Otherwise current group
// policy supplies a fresh capability plan carrying effort. When overrideEffort
// is true, effort replaces the saved plan's captured effort, including clearing
// it when effort is empty. Resolution failure returns an error and an empty
// attempt.
func (s RoutingState) resolveAttempt(snapshot provider.ProviderSnapshot, previous AttemptRoute, effort string, overrideEffort bool) (AttemptRoute, bool, error) {
	plan, planned := s.plan, s.planSet
	// A derived group fallback can retain its predecessor's plan. Discard that
	// plan here so the new group supplies capability rules for this selection.
	if planned && s.group != nil && plan.GroupID() != s.group.ID {
		planned = false
	}
	// Compatibility callers may already have selected a protocol before an
	// admitted model plan exists; keep that established route for their attempt.
	if previous.protocol != "" && !planned {
		return previous, false, nil
	}
	if s.clientProtocol == "" {
		return previous, false, nil
	}
	if !planned {
		plan = routing.Plan(routing.PlanInput{Group: s.group, ClientProtocol: s.clientProtocol, ReasoningEffort: effort})
	} else {
		plan = plan.WithClientProtocol(s.clientProtocol)
		if overrideEffort {
			plan = plan.WithReasoningEffort(effort)
		}
	}
	candidate, ok := plan.ResolveCandidate(snapshot)
	if !ok {
		return AttemptRoute{}, false, fmt.Errorf("provider %d has no enabled route for %s", snapshot.ID, s.clientProtocol)
	}
	result := AttemptRoute{protocol: candidate.UpstreamProtocol, planned: planned}
	if planned {
		result.candidate = candidate
	}
	return result, true, nil
}

// ResolveModel returns the captured final identity for an admitted model
// attempt or resolves ordinary mapping without an effort override.
func (a AttemptRoute) ResolveModel(id int64, mapping map[string]string, requested string) (string, bool) {
	mapped, matched, _ := a.ResolveModelForEffort(id, provider.NewModelRoutingSnapshot(mapping), requested, "")
	return mapped, matched
}

// ResolveModelForEffort returns the captured final identity for an admitted
// model attempt, reporting ordinary and effort-scoped match flags. Requested,
// group-mapped, and final spellings all select that same captured result,
// preventing a mapped target from being interpreted as another alias hop.
//
// Unplanned calls, model-free legacy plans, and auxiliary models such as an
// image-tool model inside a text attempt apply snapshot.ResolveForEffort
// instead.
func (a AttemptRoute) ResolveModelForEffort(id int64, snapshot provider.ModelRoutingSnapshot, requested, effort string) (string, bool, bool) {
	if a.planned && a.candidate.ProviderID == id {
		models := a.candidate.Models
		if models.RequestedModel != "" || models.GroupMappedModel != "" {
			if requested == models.RequestedModel || requested == models.GroupMappedModel || requested == models.ProviderMappedModel {
				return models.ProviderMappedModel, a.candidate.ModelMappingMatched, a.candidate.EffortMappingMatched
			}
		}
	}
	return snapshot.ResolveForEffort(requested, effort)
}
