package provider

import (
	"context"
	"fmt"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/modeltrace"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// RoutePlanner 共享渠道读取器，只组合当前请求已经通过准入的分组和映射。
type RoutePlanner struct{ channels *routing.ChannelService }

func NewRoutePlanner(channels *routing.ChannelService) *RoutePlanner {
	return &RoutePlanner{channels: channels}
}

func (p *RoutePlanner) PlanKey(ctx context.Context, key *apikey.APIKey, requested string) routing.RoutePlan {
	var group *routing.Group
	var id *int64
	if key != nil {
		group, id = key.Group, key.GroupID
	}
	return p.PlanRoute(ctx, group, id, requested)
}

func (p *RoutePlanner) PlanRoute(ctx context.Context, group *routing.Group, id *int64, requested string) routing.RoutePlan {
	mapping := routing.ChannelMappingResult{MappedModel: requested}
	if p.channels != nil {
		mapping, _ = p.channels.ResolveChannelMappingAndRestrict(ctx, id, requested)
	}
	mapping = modeltrace.WithChannelRedirect(mapping, ctx, requested)
	return RoutePlanForMapping(ctx, group, id, requested, mapping)
}

// RoutePlanForMapping 保留请求分组回退、ID 复核和协议投影，不新增查询或 Key 改写。
func RoutePlanForMapping(ctx context.Context, group *routing.Group, groupID *int64, requested string, mapping routing.ChannelMappingResult) routing.RoutePlan {
	if group == nil {
		group, _ = requeststate.GroupFromContext(ctx)
	}
	if group != nil && groupID != nil && group.ID != *groupID {
		group = nil
	}
	var view *routing.Group
	if group != nil {
		view = &routing.Group{ID: group.ID, Platform: group.Platform, SchedulerType: group.SchedulerType, AllowedProtocols: group.AllowedProtocols, ProtocolFallbacks: group.ProtocolFallbacks}
	}
	protocol, _ := requeststate.ClientProtocolFromContext(ctx)
	return routing.Plan(routing.PlanInput{Group: view, GroupID: groupID, ClientProtocol: protocol, RequestedModel: requested, Channel: mapping})
}

// AccountForProtocolAttempt 使用当前计划重新验证候选，模型规则仍在原匹配时机读取。
func AccountForProtocolAttempt(ctx context.Context, value *ExecutionAccount) (*ExecutionAccount, error) {
	if value == nil {
		return nil, fmt.Errorf("account is nil")
	}
	attempt, resolved, err := requeststate.RoutingStateFromContext(ctx).ResolveAttempt(ExecutionSnapshot(value), value.Route)
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
