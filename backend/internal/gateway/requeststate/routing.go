package requeststate

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// routing.go captures selected groups, route plans, and client protocols in
// request contexts. Group storage and retrieval clone the group's mutable
// policy collections, and the context carries the client protocol alongside the
// selected model route and any Chat function-tool requirement.
//
// The saved RoutePlan is retained by value when a derived context replaces its
// group. That plan still identifies the group for which it was built, so
// ResolveAttempt checks group identity before using it and can rebuild
// capability policy for the new group. Storing state is separate from
// admission: the HTTP caller validates identity, group eligibility, and funding
// before executing a route.

// RoutingState holds a captured group, entry protocol, and model plan for
// request execution.
type RoutingState struct {
	// group is cloned by WithGroup and GroupFromContext. plan retains its own
	// group identity, and planSet distinguishes explicit zero plans from absence;
	// clientProtocol determines subsequent candidate capability resolution.
	group             *routing.Group      // captured admitted group policy
	plan              routing.RoutePlan   // captured model route plan
	planSet           bool                // route plan presence flag
	clientProtocol    protocol.ProtocolID // entry protocol for candidate resolution
	chatFunctionTools bool                // admitted Chat function-tool requirement
}

// routingStateKey distinguishes prepared routing state from other context
// values.
type routingStateKey struct{}

// RoutingStateFromContext returns the routing value stored in ctx or an empty
// state. Nil contexts and unrelated value types are tolerated; the returned
// value retains its internally captured group pointer.
func RoutingStateFromContext(ctx context.Context) RoutingState {
	if ctx == nil {
		return RoutingState{}
	}
	state, _ := ctx.Value(routingStateKey{}).(RoutingState)
	return state
}

// WithRoutingState stores prepared state in a derived context with a nonnil
// parent. It retains the state's captured references; admission and
// construction of any group snapshot remain caller responsibilities.
func WithRoutingState(ctx context.Context, state RoutingState) context.Context {
	return context.WithValue(ctx, routingStateKey{}, state)
}

// WithGroup derives state containing a cloned group, retaining its existing
// plan and client protocol. A nil group clears group presence; callers supply a
// nonnil context and replan or let ResolveAttempt detect a group mismatch.
func WithGroup(ctx context.Context, group *routing.Group) context.Context {
	state := RoutingStateFromContext(ctx)
	state.group = routing.CloneGroup(group)
	return WithRoutingState(ctx, state)
}

// GroupFromContext returns a new clone of the captured group and its presence
// flag. Missing state yields nil and false, and mutations to the returned group
// leave the captured context group unchanged.
func GroupFromContext(ctx context.Context) (*routing.Group, bool) {
	state := RoutingStateFromContext(ctx)
	return routing.CloneGroup(state.group), state.group != nil
}

// WithRoutePlan records a supplied plan by value and marks it present in a
// derived context. Callers provide a nonnil parent and a plan built for their
// admitted group; existing group/protocol values are retained.
func WithRoutePlan(ctx context.Context, plan routing.RoutePlan) context.Context {
	state := RoutingStateFromContext(ctx)
	state.plan, state.planSet = plan, true
	return WithRoutingState(ctx, state)
}

// RoutePlanFromContext returns the saved plan and its separate presence flag. A
// zero plan may still be present when explicitly stored, and reading a nil
// context produces an absent zero value.
func RoutePlanFromContext(ctx context.Context) (routing.RoutePlan, bool) {
	state := RoutingStateFromContext(ctx)
	return state.plan, state.planSet
}

// WithClientProtocol stores the entry protocol in a derived routing context.
// Empty source clears protocol presence while retaining group and plan, and
// callers provide a nonnil parent context.
func WithClientProtocol(ctx context.Context, source protocol.ProtocolID) context.Context {
	state := RoutingStateFromContext(ctx)
	state.clientProtocol = source
	return WithRoutingState(ctx, state)
}

// ClientProtocolFromContext returns the stored client protocol and reports
// whether it is nonempty. Empty or missing state, including nil ctx, yields an
// empty protocol with false presence.
func ClientProtocolFromContext(ctx context.Context) (protocol.ProtocolID, bool) {
	state := RoutingStateFromContext(ctx)
	return state.clientProtocol, state.clientProtocol != ""
}

// WithChatFunctionTools records a Chat request that requires function tools.
// The derived context retains that requirement across provider and group
// changes while leaving the parent routing state intact.
func WithChatFunctionTools(ctx context.Context) context.Context {
	state := RoutingStateFromContext(ctx)
	state.chatFunctionTools = true
	return WithRoutingState(ctx, state)
}

// ChatFunctionToolsRequired reports whether the admitted Chat request uses functions.
func ChatFunctionToolsRequired(ctx context.Context) bool {
	return RoutingStateFromContext(ctx).chatFunctionTools
}
