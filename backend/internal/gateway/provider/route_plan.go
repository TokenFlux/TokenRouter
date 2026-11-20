package provider

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// route_plan.go builds request route plans and attaches revalidated protocol
// attempts to execution targets.
// A request route combines an admitted group with group-model mapping and the
// client protocol stored in request state. Planning carries API Key redirects
// into that model chain, and attempt revalidation attaches a resolved
// candidate to a shallow copy of the execution target.

// RoutePlanner reads group-model policy while assembling routes for admitted
// requests.
type RoutePlanner struct {
	// optional group-mapping policy reader
	groupPolicies *routing.PricingConfigService
}

// NewRoutePlanner binds the optional group-policy service used during route
// planning. A nil service leaves the requested model as the group mapping
// before redirect metadata is applied from request context.
func NewRoutePlanner(groupPolicies *routing.PricingConfigService) *RoutePlanner {
	return &RoutePlanner{groupPolicies: groupPolicies}
}

// PlanKey builds a route from the API Key's admitted group, group ID, and
// requested model. A nil key delegates with no group information, allowing
// PlanRoute to use request context where available.
func (p *RoutePlanner) PlanKey(ctx context.Context, key *apikey.APIKey, requested string) routing.RoutePlan {
	var group *routing.Group
	var id *int64
	if key != nil {
		group, id = key.Group, key.GroupID
	}
	return p.PlanRoute(ctx, group, id, requested)
}

// PlanRoute resolves group mapping when both a policy service and group ID are
// available, then carries request redirect metadata into the route. Otherwise
// the requested model supplies the initial mapping, before RoutePlanForMapping
// checks group identity.
func (p *RoutePlanner) PlanRoute(ctx context.Context, group *routing.Group, id *int64, requested string) routing.RoutePlan {
	mapping := routing.GroupMappingResult{MappedModel: requested}
	if p.groupPolicies != nil && id != nil {
		mapping = p.groupPolicies.ResolveGroupMapping(ctx, *id, requested)
	}
	mapping = modeltrace.WithGroupRedirect(mapping, ctx, requested)
	return RoutePlanForMapping(ctx, group, id, requested, mapping)
}

// RoutePlanForMapping builds a protocol route from supplied mapping and group
// information, using the context group when group is nil. A group whose ID
// disagrees with groupID is discarded before planning, so mismatched group
// policy cannot be attached.
func RoutePlanForMapping(ctx context.Context, group *routing.Group, groupID *int64, requested string, mapping routing.GroupMappingResult) routing.RoutePlan {
	// A missing supplied group uses the admitted request snapshot; the following
	// ID check prevents a snapshot from another group influencing protocol policy.
	if group == nil {
		group, _ = requeststate.GroupFromContext(ctx)
	}
	if group != nil && groupID != nil && group.ID != *groupID {
		group = nil
	}
	var view *routing.Group
	if group != nil {
		view = &routing.Group{ID: group.ID, SchedulerType: group.SchedulerType, AllowedProtocols: group.AllowedProtocols, ProtocolFallbacks: group.ProtocolFallbacks}
	}
	protocol, _ := requeststate.ClientProtocolFromContext(ctx)
	effort := requeststate.RoutingReasoningEffortFromContext(ctx)
	return routing.Plan(routing.PlanInput{Group: view, GroupID: groupID, ClientProtocol: protocol, RequestedModel: requested, GroupMapping: mapping, ReasoningEffort: effort})
}

// ProviderForProtocolAttempt revalidates the target against request routing
// state, active reasoning effort, and its existing candidate. Nil targets
// return an error; unresolved context routing returns the original target,
// while a resolved attempt returns a shallow copy carrying the new Route.
func ProviderForProtocolAttempt(ctx context.Context, value *ExecutionProvider) (*ExecutionProvider, error) {
	if value == nil {
		return nil, fmt.Errorf("provider is nil")
	}
	state := requeststate.RoutingStateFromContext(ctx)
	snapshot := ExecutionSnapshot(value)
	var (
		attempt  requeststate.AttemptRoute
		resolved bool
		err      error
	)
	if effort, hasEffort := requeststate.RoutingReasoningEffortLookupFromContext(ctx); hasEffort {
		attempt, resolved, err = state.ResolveAttemptForEffort(snapshot, value.Route, effort)
	} else {
		attempt, resolved, err = state.ResolveAttempt(snapshot, value.Route)
	}
	if err != nil {
		return nil, err
	}
	if !resolved {
		return value, nil
	}
	copied := *value
	copied.Route = attempt
	return &copied, nil
}
