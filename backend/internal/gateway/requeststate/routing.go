package requeststate

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// RoutingState 固化入口或当次尝试的分组、协议与模型计划，不持有共享缓存指针。
type RoutingState struct {
	group          *routing.Group
	plan           routing.RoutePlan
	planSet        bool
	clientProtocol protocol.ProtocolID
}

type routingStateKey struct{}

func RoutingStateFromContext(ctx context.Context) RoutingState {
	if ctx == nil {
		return RoutingState{}
	}
	state, _ := ctx.Value(routingStateKey{}).(RoutingState)
	return state
}

// WithRoutingState 只保存已构造的不可变值；身份与资金资格仍由原准入步骤决定。
func WithRoutingState(ctx context.Context, state RoutingState) context.Context {
	return context.WithValue(ctx, routingStateKey{}, state)
}

func WithGroup(ctx context.Context, group *routing.Group) context.Context {
	state := RoutingStateFromContext(ctx)
	state.group = routing.CloneGroup(group)
	return WithRoutingState(ctx, state)
}

func GroupFromContext(ctx context.Context) (*routing.Group, bool) {
	state := RoutingStateFromContext(ctx)
	return routing.CloneGroup(state.group), state.group != nil
}

func WithRoutePlan(ctx context.Context, plan routing.RoutePlan) context.Context {
	state := RoutingStateFromContext(ctx)
	state.plan, state.planSet = plan, true
	return WithRoutingState(ctx, state)
}

func RoutePlanFromContext(ctx context.Context) (routing.RoutePlan, bool) {
	state := RoutingStateFromContext(ctx)
	return state.plan, state.planSet
}

func WithClientProtocol(ctx context.Context, source protocol.ProtocolID) context.Context {
	state := RoutingStateFromContext(ctx)
	state.clientProtocol = source
	return WithRoutingState(ctx, state)
}

func ClientProtocolFromContext(ctx context.Context) (protocol.ProtocolID, bool) {
	state := RoutingStateFromContext(ctx)
	return state.clientProtocol, state.clientProtocol != ""
}
