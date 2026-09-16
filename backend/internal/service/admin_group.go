// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	antigravity "github.com/TokenFlux/TokenRouter/internal/upstream/antigravity"
	geminicli "github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	openai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	qoder "github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
)

func (s *adminServiceImpl) ListGroups(ctx context.Context, page, pageSize int, platform, status, search string, isExclusive *bool, sortBy, sortOrder string) ([]Group, int64, error) {
	values, total, err := s.routingAdmin.ListGroups(ctx, page, pageSize, platform, status, search, isExclusive, sortBy, sortOrder)
	return GroupsFromRouting(values), total, err
}

func (s *adminServiceImpl) GetAllGroups(ctx context.Context) ([]Group, error) {
	values, err := s.routingAdmin.GetAllGroups(ctx)
	return GroupsFromRouting(values), err
}

func (s *adminServiceImpl) GetAllGroupsByPlatform(ctx context.Context, platform string) ([]Group, error) {
	values, err := s.routingAdmin.GetAllGroupsByPlatform(ctx, platform)
	return GroupsFromRouting(values), err
}

func (s *adminServiceImpl) GetAllGroupsIncludingInactive(ctx context.Context) ([]Group, error) {
	values, err := s.routingAdmin.GetAllGroupsIncludingInactive(ctx)
	return GroupsFromRouting(values), err
}

func (s *adminServiceImpl) GetGroup(ctx context.Context, id int64) (*Group, error) {
	value, err := s.routingAdmin.GetGroup(ctx, id)
	return GroupFromRouting(value), err
}

func (s *adminServiceImpl) GetGroupModelsListCandidates(ctx context.Context, id int64, platform string) ([]string, error) {
	return s.routingAdmin.GetGroupModelsListCandidates(ctx, id, platform)
}

func defaultModelsListCandidateIDs(platform string) []string {
	switch platform {
	case PlatformOpenAI:
		return openai.DefaultModelIDs()
	case PlatformGemini:
		ids := make([]string, 0, len(geminicli.DefaultModels))
		for _, model := range geminicli.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformAntigravity:
		models := antigravity.DefaultModels()
		ids := make([]string, 0, len(models))
		for _, model := range models {
			ids = append(ids, model.ID)
		}
		return ids
	case PlatformQoder:
		return qoder.DefaultRequestModelIDs()
	case PlatformGrok:
		return xai.DefaultModelIDs()
	default:
		ids := make([]string, 0, len(claude.DefaultModels))
		for _, model := range claude.DefaultModels {
			ids = append(ids, model.ID)
		}
		return ids
	}
}

func groupSupportsOpenAIFast(platform string) bool { return routing.GroupSupportsOpenAIFast(platform) }

func (s *adminServiceImpl) CreateGroup(ctx context.Context, input *CreateGroupInput) (*Group, error) {
	value, err := s.routingAdmin.CreateGroup(ctx, input)
	return GroupFromRouting(value), err
}

func (s *adminServiceImpl) UpdateGroup(ctx context.Context, id int64, input *UpdateGroupInput) (*Group, error) {
	value, err := s.routingAdmin.UpdateGroup(ctx, id, input)
	return GroupFromRouting(value), err
}

func (s *adminServiceImpl) DeleteGroup(ctx context.Context, id int64) error {
	return s.routingAdmin.DeleteGroup(ctx, id)
}

func (s *adminServiceImpl) GetGroupAPIKeys(ctx context.Context, groupID int64, page, pageSize int) ([]APIKey, int64, error) {
	values, total, err := s.keyAdministration().GetGroupAPIKeys(ctx, groupID, page, pageSize)
	var out []APIKey
	if values != nil {
		out = make([]APIKey, len(values))
		for i := range values {
			out[i] = *APIKeyFromView(&values[i])
		}
	}
	return out, total, err
}

func (s *adminServiceImpl) GetGroupRateMultipliers(ctx context.Context, groupID int64) ([]UserGroupRateEntry, error) {
	return s.groupRateAdministration().GetGroupRateMultipliers(ctx, groupID)
}

func (s *adminServiceImpl) ClearGroupRateMultipliers(ctx context.Context, groupID int64) error {
	return s.groupRateAdministration().ClearGroupRateMultipliers(ctx, groupID)
}

func (s *adminServiceImpl) BatchSetGroupRateMultipliers(ctx context.Context, groupID int64, entries []GroupRateMultiplierInput) error {
	return s.groupRateAdministration().BatchSetGroupRateMultipliers(ctx, groupID, entries)
}

func (s *adminServiceImpl) ClearGroupRPMOverrides(ctx context.Context, groupID int64) error {
	return s.groupRateAdministration().ClearGroupRPMOverrides(ctx, groupID)
}

func (s *adminServiceImpl) BatchSetGroupRPMOverrides(ctx context.Context, groupID int64, entries []GroupRPMOverrideInput) error {
	return s.groupRateAdministration().BatchSetGroupRPMOverrides(ctx, groupID, entries)
}

func (s *adminServiceImpl) UpdateGroupSortOrders(ctx context.Context, updates []GroupSortOrderUpdate) error {
	return s.routingAdmin.UpdateGroupSortOrders(ctx, updates)
}

func (s *adminServiceImpl) AdminUpdateAPIKeyGroupID(ctx context.Context, keyID int64, groupID *int64) (*AdminUpdateAPIKeyGroupIDResult, error) {
	v, e := s.keyAdministration().AdminUpdateAPIKeyGroupID(ctx, keyID, groupID)
	if v == nil {
		return nil, e
	}
	return &AdminUpdateAPIKeyGroupIDResult{APIKey: APIKeyFromView(v.APIKey), AutoGrantedGroupAccess: v.AutoGrantedGroupAccess, GrantedGroupID: v.GrantedGroupID, GrantedGroupName: v.GrantedGroupName}, e
}

func (s *adminServiceImpl) AdminResetAPIKeyRateLimitUsage(ctx context.Context, keyID int64) (*APIKey, error) {
	v, e := s.keyAdministration().AdminResetAPIKeyRateLimitUsage(ctx, keyID)
	return APIKeyFromView(v), e
}

func (s *adminServiceImpl) ReplaceUserGroup(ctx context.Context, userID, oldGroupID, newGroupID int64) (*ReplaceUserGroupResult, error) {
	return s.identityAdministration().ReplaceUserGroup(ctx, userID, oldGroupID, newGroupID)
}

// DefaultGroupModelCandidates 为旧平台目录提供读取入口，S09 替换来源。
func DefaultGroupModelCandidates(platform string) []string {
	return defaultModelsListCandidateIDs(platform)
}

func (s *adminServiceImpl) groupRateAdministration() *billing.GroupRateAdmin {
	if s.groupRates != nil {
		return s.groupRates
	}
	return billing.NewGroupRateAdmin(s.userGroupRateRepo, s.authCacheInvalidator)
}
