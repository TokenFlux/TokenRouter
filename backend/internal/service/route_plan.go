// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	domain "github.com/TokenFlux/TokenRouter/internal/domain"
	ctxkey "github.com/TokenFlux/TokenRouter/internal/pkg/ctxkey"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type routePlanContextKey struct{}

// WithRoutePlan 保存一次请求或 WS turn 的不可变计划，不能写进共享账号/调度快照。
func WithRoutePlan(ctx context.Context, plan routing.RoutePlan) context.Context {
	return context.WithValue(ctx, routePlanContextKey{}, plan)
}
func routePlanFromContext(ctx context.Context) (routing.RoutePlan, bool) {
	if ctx == nil {
		return routing.RoutePlan{}, false
	}
	plan, ok := ctx.Value(routePlanContextKey{}).(routing.RoutePlan)
	return plan, ok
}

// routePlanForMapping 只接收调用者已完成准入的组；不新增 DB 查询或再次应用 Key 改写。
func routePlanForMapping(ctx context.Context, group *Group, groupID *int64, requested string, mapping ChannelMappingResult) routing.RoutePlan {
	if group == nil {
		group, _ = ctx.Value(ctxkey.Group).(*Group)
	}
	if group != nil && groupID != nil && group.ID != *groupID {
		group = nil
	}
	var view *routing.Group
	if group != nil {
		view = &routing.Group{ID: group.ID, Platform: group.Platform, SchedulerType: group.SchedulerType, AllowedProtocols: group.AllowedProtocols, ProtocolFallbacks: group.ProtocolFallbacks}
	}
	protocol, _ := ctx.Value(clientProtocolContextKey{}).(domain.ProtocolID)
	return routing.Plan(routing.PlanInput{Group: view, GroupID: groupID, ClientProtocol: protocol, RequestedModel: requested, Channel: routing.ChannelMappingResult(mapping)})
}

// PlanRoute 保留原渠道懒加载及 Key 追踪登记顺序，并产生请求独立的路由计划。
func (s *GatewayService) PlanRoute(ctx context.Context, group *Group, groupID *int64, requested string) routing.RoutePlan {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, requested)
	return routePlanForMapping(ctx, group, groupID, requested, mapping)
}
func (s *OpenAIGatewayService) PlanRoute(ctx context.Context, group *Group, groupID *int64, requested string) routing.RoutePlan {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, requested)
	return routePlanForMapping(ctx, group, groupID, requested, mapping)
}
func ChannelMappingFromRoutePlan(plan routing.RoutePlan) ChannelMappingResult {
	return ChannelMappingResult(plan.Mapping())
}

// APIKeyRouteGroup 只为旧入口提供可空的已授权分组，不查询或补全权限。
func APIKeyRouteGroup(key *APIKey) *Group {
	if key == nil {
		return nil
	}
	return key.Group
}
