// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"

	context "context"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// routePlanForMapping 只接收调用者已完成准入的组；不新增 DB 查询或再次应用 Key 改写。
func routePlanForMapping(ctx context.Context, group *routing.Group, groupID *int64, requested string, mapping routing.ChannelMappingResult) routing.RoutePlan {
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
	return routing.Plan(routing.PlanInput{Group: view, GroupID: groupID, ClientProtocol: protocol, RequestedModel: requested, Channel: routing.ChannelMappingResult(mapping)})
}

// PlanRoute 保留原渠道懒加载及 Key 追踪登记顺序，并产生请求独立的路由计划。
func (s *GatewayService) PlanRoute(ctx context.Context, group *routing.Group, groupID *int64, requested string) routing.RoutePlan {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, requested)
	return routePlanForMapping(ctx, group, groupID, requested, mapping)
}
func (s *OpenAIGatewayService) PlanRoute(ctx context.Context, group *routing.Group, groupID *int64, requested string) routing.RoutePlan {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, requested)
	return routePlanForMapping(ctx, group, groupID, requested, mapping)
}
func ChannelMappingFromRoutePlan(plan routing.RoutePlan) routing.ChannelMappingResult {
	return routing.ChannelMappingResult(plan.Mapping())
}

// APIKeyRouteGroup 只为旧入口提供可空的已授权分组，不查询或补全权限。
func APIKeyRouteGroup(key *apikey.APIKey) *routing.Group {
	if key == nil {
		return nil
	}
	return key.Group
}
