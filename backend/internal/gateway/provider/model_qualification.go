package provider

import (
	"context"
	"slices"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
)

// model_qualification.go checks protocol and model-window eligibility and
// reports remaining cooldown time.
// A provider can have a model-specific cooldown and a shared-scope cooldown
// applying to the same request. Qualification checks protocol permission and
// provider health before asking whether those scopes permit execution;
// reporting their remaining wait uses the longest active duration because
// the expiry windows run concurrently.
//
// Candidate snapshots copy enabled protocols for route resolution, while the
// current group determines OAuth, privacy, and protocol requirements. Limit
// inspection remains separate from permission: Antigravity overages can admit
// a model with an active cooldown, so Limited reports raw window activity and
// LimitRemaining reports its observed wait even when Schedulable allows work.

// CandidateSnapshot returns routing provider data with copied enabled
// protocols resolved for the current attempt. Nil records produce an empty
// snapshot; protocol selection applies the target's legacy API protocol when
// needed.
func (p ModelPolicy) CandidateSnapshot() provider.ProviderSnapshot {
	if p.Record == nil {
		return provider.ProviderSnapshot{}
	}
	snapshot := p.Record.RoutingSnapshot()
	if p.models != nil {
		snapshot.ModelPolicy = p.models.RoutingSnapshot()
	} else {
		snapshot.ModelPolicy = provider.NewModelRoutingSnapshotWithEffort(
			provider.ResolveModelMapping(p.Record, provideradapter.ModelDefaults()),
			provider.ResolveEffortModelMapping(p.Record),
		)
	}
	snapshot.EnabledProtocols = slices.Clone(p.Record.UpstreamProtocolsForLegacy(p.protocolTarget().GetAPIProtocol()))
	return snapshot
}

// ProtocolRoute checks provider protocol support against the supplied group
// and client protocol independently of model identity. Nil records return false;
// AllowsProtocol adds the model-specific check for a known model.
func (p ModelPolicy) ProtocolRoute(group *routing.Group, source protocol.ProtocolID) (protocol.ProtocolID, bool) {
	if p.Record == nil {
		return "", false
	}
	var fallbacks map[capability.ProtocolID][]capability.ProtocolID
	if group != nil {
		fallbacks = group.ProtocolFallbacks
	}
	return capability.ResolveRoute(p.CandidateSnapshot().Protocols(), source, fallbacks)
}

// AllowsProtocol checks group OAuth/privacy requirements and the protocol
// route for model. Missing provider records fail. An empty model asks only
// whether the provider can serve the client protocol at all, which pool
// checks use before a model is known.
//
// Providers whose models each declare one protocol also need a route for the
// final mapped model, so a known model resolves a complete candidate. That
// resolution reuses the retained request plan when model is one of its
// spellings, keeping group mapping and conversion policy, and builds a fresh
// plan for other models. Without a client protocol, the other providers pass
// because their execution adapters validate the selected entry later.
func (p ModelPolicy) AllowsProtocol(ctx context.Context, model string) bool {
	group, _ := requeststate.GroupFromContext(ctx)
	if p.Record == nil || group != nil && (group.RequireOAuthOnly && !p.Record.IsOAuth() || group.RequirePrivacySet && !p.Record.IsPrivacySet()) {
		return false
	}
	source, _ := requeststate.ClientProtocolFromContext(ctx)
	snapshot := p.CandidateSnapshot()
	if model == "" || snapshot.ModelProtocols == nil {
		if source == "" {
			return true
		}
		_, ok := p.ProtocolRoute(group, source)
		return ok
	}
	effort := p.effectiveEffort(ctx)
	plan := routing.Plan(routing.PlanInput{Group: group, ClientProtocol: source, RequestedModel: model, ReasoningEffort: effort})
	if retained, planned := requeststate.RoutePlanFromContext(ctx); planned && (group == nil || retained.GroupID() == group.ID) {
		models := retained.Models()
		if model == models.RequestedModel || model == models.GroupMappedModel {
			plan = retained.WithClientProtocol(source)
			if effort != "" {
				plan = plan.WithReasoningEffort(effort)
			}
		}
	}
	_, ok := plan.ResolveCandidate(snapshot)
	return ok
}

// Schedulable applies protocol eligibility, provider scheduling state, and
// applicable model window permission in that order. Nil records fail
// immediately, and each rejected stage prevents later eligibility work.
func (p ModelPolicy) Schedulable(ctx context.Context, model string) bool {
	if p.Record == nil {
		return false
	}
	if !p.AllowsProtocol(ctx, model) || !p.Record.IsSchedulable() {
		return false
	}
	return p.AllowsModel(ctx, model)
}

// Limited reports whether any model or shared-scope limit selected by
// LimitKeys is active. An empty key set yields false, keeping a missing model
// window distinct from an active one.
func (p ModelPolicy) Limited(ctx context.Context, model string) bool {
	return slices.ContainsFunc(p.LimitKeys(ctx, model), p.Record.ModelRateLimitActive)
}

// LimitRemaining returns the longest observed remaining duration across the
// model and shared-scope windows selected by LimitKeys. Empty, invalid, or
// elapsed windows leave zero, using the provider record's clock for each read.
//
// Several scopes can restrict one request simultaneously. Their waits overlap,
// so adding durations would overstate the cooldown; the maximum identifies the
// last observed expiry. This is raw window evidence, and callers use
// Schedulable separately when overage policy can permit execution during a
// model cooldown.
func (p ModelPolicy) LimitRemaining(ctx context.Context, model string) time.Duration {
	remaining := time.Duration(0)
	for _, key := range p.LimitKeys(ctx, model) {
		if value := p.Record.ModelRateLimitRemaining(key); value > remaining {
			remaining = value
		}
	}
	return remaining
}

// FinalAntigravityModel resolves the upstream model using the request's
// optional routing reasoning effort and thinking hint. It delegates directly
// to the provider adapter, which owns platform mapping and unsupported-model
// handling.
func (p ModelPolicy) FinalAntigravityModel(ctx context.Context, model string) string {
	return provideradapter.FinalAntigravityModelForEffort(p.Record, model, p.effectiveEffort(ctx), modelThinking(ctx))
}
