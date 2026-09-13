// 本文件维护 admin 的所属能力；兼容入口复用唯一实现。
package admin

import (
	context "context"
	dto "github.com/TokenFlux/TokenRouter/internal/handler/dto"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

type groupHTTPTestPort struct{ service.AdminService }

func groupHTTPTestRows(values []service.Group) []routing.Group {
	if values == nil {
		return nil
	}
	out := make([]routing.Group, len(values))
	for i := range values {
		out[i] = *service.RoutingGroupView(&values[i])
	}
	return out
}
func newTestGroupHandler(svc service.AdminService) *GroupHandler {
	resources := routinghttp.GroupResources{Rates: svc, Keys: func(ctx context.Context, id int64, page, size int) ([]dto.APIKey, int64, error) {
		values, total, err := svc.GetGroupAPIKeys(ctx, id, page, size)
		if err != nil {
			return nil, 0, err
		}
		out := make([]dto.APIKey, 0, len(values))
		for i := range values {
			out = append(out, *dto.APIKeyFromService(&values[i]))
		}
		return out, total, nil
	}}
	return routinghttp.NewGroupHandler(groupHTTPTestPort{svc}, resources)
}

func (s groupHTTPTestPort) GetGroup(ctx context.Context, id int64) (*routing.Group, error) {
	v, e := s.AdminService.GetGroup(ctx, id)
	return service.RoutingGroupView(v), e
}
func (s groupHTTPTestPort) CreateGroup(ctx context.Context, input *routing.CreateGroupInput) (*routing.Group, error) {
	v, e := s.AdminService.CreateGroup(ctx, input)
	return service.RoutingGroupView(v), e
}
func (s groupHTTPTestPort) UpdateGroup(ctx context.Context, id int64, input *routing.UpdateGroupInput) (*routing.Group, error) {
	v, e := s.AdminService.UpdateGroup(ctx, id, input)
	return service.RoutingGroupView(v), e
}
func (s groupHTTPTestPort) DuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*routing.Group, error) {
	v, e := s.AdminService.DuplicateGroup(ctx, id, actorScope, operationKey)
	return service.RoutingGroupView(v), e
}
func (s groupHTTPTestPort) RecoverDuplicateGroup(ctx context.Context, id int64, actorScope, operationKey string) (*routing.Group, error) {
	v, e := s.AdminService.RecoverDuplicateGroup(ctx, id, actorScope, operationKey)
	return service.RoutingGroupView(v), e
}
func (s groupHTTPTestPort) ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]routing.Group, int64, error) {
	v, total, e := s.AdminService.ListGroups(ctx, page, pageSize, platform, status, search, isExclusive, sortBy, sortOrder)
	return groupHTTPTestRows(v), total, e
}
func (s groupHTTPTestPort) GetAllGroups(ctx context.Context) ([]routing.Group, error) {
	v, e := s.AdminService.GetAllGroups(ctx)
	return groupHTTPTestRows(v), e
}
func (s groupHTTPTestPort) GetAllGroupsByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	v, e := s.AdminService.GetAllGroupsByPlatform(ctx, platform)
	return groupHTTPTestRows(v), e
}
func (s groupHTTPTestPort) GetAllGroupsIncludingInactive(ctx context.Context) ([]routing.Group, error) {
	v, e := s.AdminService.GetAllGroupsIncludingInactive(ctx)
	return groupHTTPTestRows(v), e
}
