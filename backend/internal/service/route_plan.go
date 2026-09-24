// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	context "context"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

// routePlanForMapping 只接收调用者已完成准入的组；不新增 DB 查询或再次应用 Key 改写。

func (s *OpenAIGatewayService) PlanRoute(ctx context.Context, group *routing.Group, groupID *int64, requested string) routing.RoutePlan {
	mapping, _ := s.ResolveChannelMappingAndRestrict(ctx, groupID, requested)
	return gatewayprovider.RoutePlanForMapping(ctx, group, groupID, requested, mapping)
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
