//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/stretchr/testify/require"
)

func ptrString[T ~string](v T) *string {
	s := string(v)
	return &s
}

// groupRepoStubForAdmin 用于测试 AdminService 的 GroupRepository Stub
type groupRepoStubForAdmin struct {
	created *Group // 记录 Create 调用的参数
	updated *Group // 记录 Update 调用的参数
	getByID *Group // GetByID 返回值
	getErr  error  // GetByID 返回的错误

	listWithFiltersCalls       int
	listWithFiltersParams      pagination.PaginationParams
	listWithFiltersPlatform    string
	listWithFiltersStatus      string
	listWithFiltersSearch      string
	listWithFiltersIsExclusive *bool
	listWithFiltersGroups      []Group
	listWithFiltersResult      *pagination.PaginationResult
	listWithFiltersErr         error
}

func (s *groupRepoStubForAdmin) Create(_ context.Context, g *Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForAdmin) Update(_ context.Context, g *Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForAdmin) GetByID(_ context.Context, _ int64) (*Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) GetByIDLite(_ context.Context, _ int64) (*Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForAdmin) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForAdmin) List(_ context.Context, _ pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForAdmin) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]Group, *pagination.PaginationResult, error) {
	s.listWithFiltersCalls++
	s.listWithFiltersParams = params
	s.listWithFiltersPlatform = platform
	s.listWithFiltersStatus = status
	s.listWithFiltersSearch = search
	s.listWithFiltersIsExclusive = isExclusive

	if s.listWithFiltersErr != nil {
		return nil, nil, s.listWithFiltersErr
	}

	result := s.listWithFiltersResult
	if result == nil {
		result = &pagination.PaginationResult{
			Total:    int64(len(s.listWithFiltersGroups)),
			Page:     params.Page,
			PageSize: params.PageSize,
		}
	}

	return s.listWithFiltersGroups, result, nil
}

func (s *groupRepoStubForAdmin) ListActive(_ context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatform(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (s *groupRepoStubForAdmin) ListActiveByPlatformLite(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStubForAdmin) ExistsByName(_ context.Context, _ string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForAdmin) GetAccountCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForAdmin) DeleteAccountGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForAdmin) BindAccountsToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForAdmin) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForAdmin) UpdateSortOrders(_ context.Context, _ []GroupSortOrderUpdate) error {
	return nil
}

func TestAdminService_ListGroups_PassesSortParams(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []Group{{ID: 1, Name: "g1"}},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, _, err := svc.ListGroups(context.Background(), 3, 25, PlatformOpenAI, StatusActive, "needle", nil, "account_count", "ASC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      3,
		PageSize:  25,
		SortBy:    "account_count",
		SortOrder: "ASC",
	}, repo.listWithFiltersParams)
}

func TestAdminService_ListGroups_PassesSessionIsolationSortParams(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []Group{{ID: 1, Name: "g1"}},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, _, err := svc.ListGroups(context.Background(), 1, 20, "", "", "", nil, "session_isolation_enabled", "DESC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      1,
		PageSize:  20,
		SortBy:    "session_isolation_enabled",
		SortOrder: "DESC",
	}, repo.listWithFiltersParams)
}

// groupModelsListAccountRepoStub 只实现候选模型测试需要的可调度账号查询。
type groupModelsListAccountRepoStub struct {
	*accountRepoStub
	accounts      []Account
	calledGroupID int64
}

func (s *groupModelsListAccountRepoStub) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]Account, error) {
	s.calledGroupID = groupID
	out := make([]Account, len(s.accounts))
	copy(out, s.accounts)
	return out, nil
}

// TestAdminService_GetGroupModelsListCandidates_UsesConfiguredRequestModels 确保 OpenAI-compatible 分组不会混入 OpenAI 默认模型。
func TestAdminService_GetGroupModelsListCandidates_UsesConfiguredRequestModels(t *testing.T) {
	groupID := int64(10)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &Group{ID: groupID, Platform: PlatformOpenAI},
	}
	accountRepo := &groupModelsListAccountRepoStub{
		accountRepoStub: &accountRepoStub{},
		accounts: []Account{
			{
				ID:       1,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"model_whitelist": []any{"deepseek-v4-pro", "deepseek-v4-flash"},
				},
			},
			{
				ID:       2,
				Platform: PlatformAnthropic,
				Credentials: map[string]any{
					"model_whitelist": []any{"claude-sonnet-4-6"},
				},
			},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, groupID, accountRepo.calledGroupID)
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, models)
}

// TestAdminService_GetGroupModelsListCandidates_UsesCustomModelsList 确保已有分组不会因为 OpenAI 接入格式回退出 GPT 默认模型。
func TestAdminService_GetGroupModelsListCandidates_UsesCustomModelsList(t *testing.T) {
	groupID := int64(12)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &Group{
			ID:       groupID,
			Platform: PlatformOpenAI,
			ModelsListConfig: GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
			},
		},
	}
	accountRepo := &groupModelsListAccountRepoStub{
		accountRepoStub: &accountRepoStub{},
		accounts: []Account{
			{ID: 1, Platform: PlatformOpenAI},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, groupID, accountRepo.calledGroupID)
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, models)
}

// TestAdminService_GetGroupModelsListCandidates_FiltersCustomModelsList 确保候选存在有限模型时按自定义模型列表取交集。
func TestAdminService_GetGroupModelsListCandidates_FiltersCustomModelsList(t *testing.T) {
	groupID := int64(13)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &Group{
			ID:       groupID,
			Platform: PlatformOpenAI,
			ModelsListConfig: GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-pro", "gpt-5.5", "deepseek-v4-flash"},
			},
		},
	}
	accountRepo := &groupModelsListAccountRepoStub{
		accountRepoStub: &accountRepoStub{},
		accounts: []Account{
			{
				ID:       1,
				Platform: PlatformOpenAI,
				Credentials: map[string]any{
					"model_whitelist": []any{"deepseek-v4-flash", "deepseek-v4-pro"},
				},
			},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-pro", "deepseek-v4-flash"}, models)
}

// TestAdminService_GetGroupModelsListCandidates_IgnoresCustomModelsListForPlatformSwitch 确保编辑时切换平台不会沿用旧平台的自定义模型。
func TestAdminService_GetGroupModelsListCandidates_IgnoresCustomModelsListForPlatformSwitch(t *testing.T) {
	groupID := int64(14)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &Group{
			ID:       groupID,
			Platform: PlatformOpenAI,
			ModelsListConfig: GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-flash"},
			},
		},
	}
	accountRepo := &groupModelsListAccountRepoStub{
		accountRepoStub: &accountRepoStub{},
		accounts: []Account{
			{
				ID:       1,
				Platform: PlatformAnthropic,
				Credentials: map[string]any{
					"model_whitelist": []any{"claude-sonnet-4-6"},
				},
			},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, PlatformAnthropic)

	require.NoError(t, err)
	require.Equal(t, []string{"claude-sonnet-4-6"}, models)
}

// TestAdminService_GetGroupModelsListCandidates_FallsBackToPlatformDefaults 确保未配置有限模型时仍保留旧的默认候选。
func TestAdminService_GetGroupModelsListCandidates_FallsBackToPlatformDefaults(t *testing.T) {
	groupID := int64(11)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &Group{ID: groupID, Platform: PlatformOpenAI},
	}
	accountRepo := &groupModelsListAccountRepoStub{
		accountRepoStub: &accountRepoStub{},
		accounts: []Account{
			{ID: 1, Platform: PlatformOpenAI},
		},
	}
	svc := &adminServiceImpl{groupRepo: groupRepo, accountRepo: accountRepo}

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, defaultModelsListCandidateIDs(PlatformOpenAI), models)
}

// TestAdminService_CreateGroup_WithImagePricing 测试创建分组时 ImagePrice 字段正确传递
func TestAdminService_CreateGroup_WithImagePricing(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	price1K := 0.10
	price2K := 0.15
	price4K := 0.30

	input := &CreateGroupInput{
		Name:           "test-group",
		Description:    "Test group",
		Platform:       PlatformAntigravity,
		RateMultiplier: 1.0,
		ImagePrice1K:   &price1K,
		ImagePrice2K:   &price2K,
		ImagePrice4K:   &price4K,
	}

	group, err := svc.CreateGroup(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, group)

	// 验证 repo 收到了正确的字段
	require.NotNil(t, repo.created)
	require.NotNil(t, repo.created.ImagePrice1K)
	require.NotNil(t, repo.created.ImagePrice2K)
	require.NotNil(t, repo.created.ImagePrice4K)
	require.InDelta(t, 0.10, *repo.created.ImagePrice1K, 0.0001)
	require.InDelta(t, 0.15, *repo.created.ImagePrice2K, 0.0001)
	require.InDelta(t, 0.30, *repo.created.ImagePrice4K, 0.0001)
}

// TestAdminService_CreateGroup_NilImagePricing 测试 ImagePrice 为 nil 时正常创建
func TestAdminService_CreateGroup_NilImagePricing(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	input := &CreateGroupInput{
		Name:           "test-group",
		Description:    "Test group",
		Platform:       PlatformAntigravity,
		RateMultiplier: 1.0,
		// ImagePrice 字段全部为 nil
	}

	group, err := svc.CreateGroup(context.Background(), input)
	require.NoError(t, err)
	require.NotNil(t, group)

	// 验证 ImagePrice 字段为 nil
	require.NotNil(t, repo.created)
	require.Nil(t, repo.created.ImagePrice1K)
	require.Nil(t, repo.created.ImagePrice2K)
	require.Nil(t, repo.created.ImagePrice4K)
}

func TestAdminService_CreateGroup_DefaultsGrokMediaGenerationEnabled(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:           "grok-media",
		Description:    "Grok media group",
		Platform:       PlatformGrok,
		RateMultiplier: 1.0,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.True(t, repo.created.AllowImageGeneration)
	require.True(t, group.AllowImageGeneration)
}

func TestAdminService_CreateGroup_PreservesNonGrokImageGenerationDisabled(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:           "anthropic-text",
		Description:    "Anthropic text group",
		Platform:       PlatformAnthropic,
		RateMultiplier: 1.0,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.False(t, repo.created.AllowImageGeneration)
	require.False(t, group.AllowImageGeneration)
}

func TestAdminService_CreateGroup_WithSessionIsolation(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                    "isolated-group",
		Platform:                PlatformAnthropic,
		RateMultiplier:          1.0,
		SessionIsolationEnabled: true,
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.True(t, repo.created.SessionIsolationEnabled)
	require.True(t, group.SessionIsolationEnabled)
}

// TestAdminService_UpdateGroup_WithImagePricing 测试更新分组时 ImagePrice 字段正确更新
func TestAdminService_UpdateGroup_WithImagePricing(t *testing.T) {
	existingGroup := &Group{
		ID:       1,
		Name:     "existing-group",
		Platform: PlatformAntigravity,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	price1K := 0.12
	price2K := 0.18
	price4K := 0.36

	input := &UpdateGroupInput{
		ImagePrice1K: &price1K,
		ImagePrice2K: &price2K,
		ImagePrice4K: &price4K,
	}

	group, err := svc.UpdateGroup(context.Background(), 1, input)
	require.NoError(t, err)
	require.NotNil(t, group)

	// 验证 repo 收到了更新后的字段
	require.NotNil(t, repo.updated)
	require.NotNil(t, repo.updated.ImagePrice1K)
	require.NotNil(t, repo.updated.ImagePrice2K)
	require.NotNil(t, repo.updated.ImagePrice4K)
	require.InDelta(t, 0.12, *repo.updated.ImagePrice1K, 0.0001)
	require.InDelta(t, 0.18, *repo.updated.ImagePrice2K, 0.0001)
	require.InDelta(t, 0.36, *repo.updated.ImagePrice4K, 0.0001)
}

// TestAdminService_UpdateGroup_PartialImagePricing 测试仅更新部分 ImagePrice 字段
func TestAdminService_UpdateGroup_PartialImagePricing(t *testing.T) {
	oldPrice2K := 0.15
	existingGroup := &Group{
		ID:           1,
		Name:         "existing-group",
		Platform:     PlatformAntigravity,
		Status:       StatusActive,
		ImagePrice2K: &oldPrice2K, // 已有 2K 价格
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	// 只更新 1K 价格
	price1K := 0.10
	input := &UpdateGroupInput{
		ImagePrice1K: &price1K,
		// ImagePrice2K 和 ImagePrice4K 为 nil，不更新
	}

	group, err := svc.UpdateGroup(context.Background(), 1, input)
	require.NoError(t, err)
	require.NotNil(t, group)

	// 验证：1K 被更新，2K 保持原值，4K 仍为 nil
	require.NotNil(t, repo.updated)
	require.NotNil(t, repo.updated.ImagePrice1K)
	require.InDelta(t, 0.10, *repo.updated.ImagePrice1K, 0.0001)
	require.NotNil(t, repo.updated.ImagePrice2K)
	require.InDelta(t, 0.15, *repo.updated.ImagePrice2K, 0.0001) // 原值保持
	require.Nil(t, repo.updated.ImagePrice4K)
}

func TestAdminService_UpdateGroup_PreservesImageGenerationControlsWhenOmitted(t *testing.T) {
	imageMultiplier := 0.5
	existingGroup := &Group{
		ID:                   1,
		Name:                 "existing-group",
		Platform:             PlatformOpenAI,
		Status:               StatusActive,
		AllowImageGeneration: true,
		ImageRateIndependent: true,
		ImageRateMultiplier:  imageMultiplier,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	updatedDesc := "updated"
	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		Description: &updatedDesc,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.True(t, repo.updated.AllowImageGeneration)
	require.True(t, repo.updated.ImageRateIndependent)
	require.InDelta(t, 0.5, repo.updated.ImageRateMultiplier, 1e-12)
}

func TestAdminService_UpdateGroup_WithSessionIsolation(t *testing.T) {
	existingGroup := &Group{
		ID:       1,
		Name:     "existing-group",
		Platform: PlatformAnthropic,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}
	enabled := true

	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		SessionIsolationEnabled: &enabled,
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.True(t, repo.updated.SessionIsolationEnabled)
	require.True(t, group.SessionIsolationEnabled)
}

func TestAdminService_UpdateGroup_ClearsDescriptionWhenEmptyString(t *testing.T) {
	existingGroup := &Group{
		ID:          1,
		Name:        "existing-group",
		Description: "Auto-created default group",
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	empty := ""
	_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		Description: &empty,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, "", repo.updated.Description, "空字符串应清空分组描述")
}

func TestAdminService_UpdateGroup_PreservesDescriptionWhenNil(t *testing.T) {
	existingGroup := &Group{
		ID:          1,
		Name:        "existing-group",
		Description: "keep me",
		Platform:    PlatformOpenAI,
		Status:      StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		Description: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, "keep me", repo.updated.Description, "nil 应保留原有分组描述")
}

func TestAdminService_UpdateGroup_RejectsNegativeImageRateMultiplier(t *testing.T) {
	existingGroup := &Group{
		ID:                  1,
		Name:                "existing-group",
		Platform:            PlatformOpenAI,
		Status:              StatusActive,
		ImageRateMultiplier: 1,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}
	negative := -0.1

	_, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		ImageRateMultiplier: &negative,
	})
	require.Error(t, err)
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_InvalidatesAuthCacheOnRPMLimitChange(t *testing.T) {
	existingGroup := &Group{
		ID:       1,
		Name:     "existing-group",
		Platform: PlatformAnthropic,
		Status:   StatusActive,
		RPMLimit: 10,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	invalidator := &authCacheInvalidatorStub{}
	svc := &adminServiceImpl{
		groupRepo:            repo,
		authCacheInvalidator: invalidator,
	}

	rpmLimit := 60
	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		RPMLimit: &rpmLimit,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, 60, repo.updated.RPMLimit)
	require.Equal(t, []int64{1}, invalidator.groupIDs, "分组 RPMLimit 写入 auth snapshot，变更后必须失效 API Key 认证缓存")
}

func TestAdminService_UpdateGroup_NormalizesPeakRateWhenDisabled(t *testing.T) {
	existingGroup := &Group{
		ID:                 1,
		Name:               "existing-group",
		Platform:           PlatformOpenAI,
		Status:             StatusActive,
		PeakRateEnabled:    true,
		PeakStart:          "14:00",
		PeakEnd:            "18:00",
		PeakRateMultiplier: 3,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	disabled := false
	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		PeakRateEnabled: &disabled,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.False(t, repo.updated.PeakRateEnabled)
	require.Equal(t, "14:00", repo.updated.PeakStart)
	require.Equal(t, "18:00", repo.updated.PeakEnd)
	require.Equal(t, 3.0, repo.updated.PeakRateMultiplier)
}

func TestAdminService_UpdateGroup_ScrubsInvalidDisabledPeakRate(t *testing.T) {
	existingGroup := &Group{
		ID:                 1,
		Name:               "existing-group",
		Platform:           PlatformOpenAI,
		Status:             StatusActive,
		PeakRateEnabled:    false,
		PeakStart:          "bad",
		PeakEnd:            "18:00",
		PeakRateMultiplier: -1,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.False(t, repo.updated.PeakRateEnabled)
	require.Equal(t, "", repo.updated.PeakStart)
	require.Equal(t, "18:00", repo.updated.PeakEnd)
	require.Equal(t, 1.0, repo.updated.PeakRateMultiplier)
}

func TestAdminService_CreateGroup_NormalizesMessagesDispatchModelConfig(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:           "dispatch-group",
		Description:    "dispatch config",
		Platform:       PlatformOpenAI,
		RateMultiplier: 1.0,
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			OpusMappedModel:   " gpt-5.4-high ",
			SonnetMappedModel: " gpt-5.3-codex ",
			HaikuMappedModel:  " gpt-5.4-mini-medium ",
			ExactModelMappings: map[string]string{
				" claude-sonnet-4-5-20250929 ": " gpt-5.2-high ",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.Equal(t, OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   "gpt-5.4",
		SonnetMappedModel: "gpt-5.3-codex",
		HaikuMappedModel:  "gpt-5.4-mini",
		ExactModelMappings: map[string]string{
			"claude-sonnet-4-5-20250929": "gpt-5.2",
		},
	}, repo.created.MessagesDispatchModelConfig)
}

func TestAdminService_UpdateGroup_NormalizesMessagesDispatchModelConfig(t *testing.T) {
	existingGroup := &Group{
		ID:       1,
		Name:     "existing-group",
		Platform: PlatformOpenAI,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		MessagesDispatchModelConfig: &OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: " gpt-5.4-medium ",
			ExactModelMappings: map[string]string{
				" claude-haiku-4-5-20251001 ": " gpt-5.4-mini-high ",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, OpenAIMessagesDispatchModelConfig{
		SonnetMappedModel: "gpt-5.4",
		ExactModelMappings: map[string]string{
			"claude-haiku-4-5-20251001": "gpt-5.4-mini",
		},
	}, repo.updated.MessagesDispatchModelConfig)
}

func TestAdminService_CreateGroup_ClearsMessagesDispatchFieldsForNonOpenAIPlatform(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                  "anthropic-group",
		Description:           "non-openai",
		Platform:              PlatformAnthropic,
		RateMultiplier:        1.0,
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			OpusMappedModel: "gpt-5.4",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.False(t, repo.created.AllowMessagesDispatch)
	require.Empty(t, repo.created.DefaultMappedModel)
	require.Equal(t, OpenAIMessagesDispatchModelConfig{}, repo.created.MessagesDispatchModelConfig)
}

func TestAdminService_UpdateGroup_ClearsMessagesDispatchFieldsWhenPlatformChangesAwayFromOpenAI(t *testing.T) {
	existingGroup := &Group{
		ID:                    1,
		Name:                  "existing-openai-group",
		Platform:              PlatformOpenAI,
		Status:                StatusActive,
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.3-codex",
		},
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), 1, &UpdateGroupInput{
		Platform: PlatformAnthropic,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, PlatformAnthropic, repo.updated.Platform)
	require.False(t, repo.updated.AllowMessagesDispatch)
	require.Empty(t, repo.updated.DefaultMappedModel)
	require.Equal(t, OpenAIMessagesDispatchModelConfig{}, repo.updated.MessagesDispatchModelConfig)
}

func TestAdminService_ListGroups_WithSearch(t *testing.T) {
	// 测试：
	// 1. search 参数正常传递到 repository 层
	// 2. search 为空字符串时的行为
	// 3. search 与其他过滤条件组合使用

	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []Group{{ID: 1, Name: "alpha"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 1},
		}
		svc := &adminServiceImpl{groupRepo: repo}

		groups, total, err := svc.ListGroups(context.Background(), 1, 20, "", "", "alpha", nil, "", "")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.Equal(t, []Group{{ID: 1, Name: "alpha"}}, groups)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 1, PageSize: 20}, repo.listWithFiltersParams)
		require.Equal(t, "alpha", repo.listWithFiltersSearch)
		require.Nil(t, repo.listWithFiltersIsExclusive)
	})

	t.Run("search 为空字符串时传递空字符串", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []Group{},
			listWithFiltersResult: &pagination.PaginationResult{Total: 0},
		}
		svc := &adminServiceImpl{groupRepo: repo}

		groups, total, err := svc.ListGroups(context.Background(), 2, 10, "", "", "", nil, "", "")
		require.NoError(t, err)
		require.Empty(t, groups)
		require.Equal(t, int64(0), total)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 2, PageSize: 10}, repo.listWithFiltersParams)
		require.Equal(t, "", repo.listWithFiltersSearch)
		require.Nil(t, repo.listWithFiltersIsExclusive)
	})

	t.Run("search 与其他过滤条件组合使用", func(t *testing.T) {
		isExclusive := true
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []Group{{ID: 2, Name: "beta"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 42},
		}
		svc := &adminServiceImpl{groupRepo: repo}

		groups, total, err := svc.ListGroups(context.Background(), 3, 50, PlatformAntigravity, StatusActive, "beta", &isExclusive, "", "")
		require.NoError(t, err)
		require.Equal(t, int64(42), total)
		require.Equal(t, []Group{{ID: 2, Name: "beta"}}, groups)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 3, PageSize: 50}, repo.listWithFiltersParams)
		require.Equal(t, PlatformAntigravity, repo.listWithFiltersPlatform)
		require.Equal(t, StatusActive, repo.listWithFiltersStatus)
		require.Equal(t, "beta", repo.listWithFiltersSearch)
		require.NotNil(t, repo.listWithFiltersIsExclusive)
		require.True(t, *repo.listWithFiltersIsExclusive)
	})
}

func TestAdminService_ValidateFallbackGroup_DetectsCycle(t *testing.T) {
	groupID := int64(1)
	fallbackID := int64(2)
	repo := &groupRepoStubForFallbackCycle{
		groups: map[int64]*Group{
			groupID: {
				ID:              groupID,
				FallbackGroupID: &fallbackID,
			},
			fallbackID: {
				ID:              fallbackID,
				FallbackGroupID: &groupID,
			},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	err := svc.validateFallbackGroup(context.Background(), groupID, fallbackID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group cycle")
}

type groupRepoStubForFallbackCycle struct {
	groups map[int64]*Group
}

func (s *groupRepoStubForFallbackCycle) Create(_ context.Context, _ *Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStubForFallbackCycle) Update(_ context.Context, _ *Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStubForFallbackCycle) GetByID(ctx context.Context, id int64) (*Group, error) {
	return s.GetByIDLite(ctx, id)
}

func (s *groupRepoStubForFallbackCycle) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, ErrGroupNotFound
}

func (s *groupRepoStubForFallbackCycle) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForFallbackCycle) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForFallbackCycle) List(_ context.Context, _ pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForFallbackCycle) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _ string, _ *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStubForFallbackCycle) ListActive(_ context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForFallbackCycle) ListActiveByPlatform(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (s *groupRepoStubForFallbackCycle) ListActiveByPlatformLite(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStubForFallbackCycle) ExistsByName(_ context.Context, _ string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForFallbackCycle) GetAccountCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForFallbackCycle) DeleteAccountGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForFallbackCycle) BindAccountsToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForFallbackCycle) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForFallbackCycle) UpdateSortOrders(_ context.Context, _ []GroupSortOrderUpdate) error {
	return nil
}

type groupRepoStubForInvalidRequestFallback struct {
	groups  map[int64]*Group
	created *Group
	updated *Group
}

func (s *groupRepoStubForInvalidRequestFallback) Create(_ context.Context, g *Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForInvalidRequestFallback) Update(_ context.Context, g *Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForInvalidRequestFallback) GetByID(ctx context.Context, id int64) (*Group, error) {
	return s.GetByIDLite(ctx, id)
}

func (s *groupRepoStubForInvalidRequestFallback) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, ErrGroupNotFound
}

func (s *groupRepoStubForInvalidRequestFallback) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForInvalidRequestFallback) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForInvalidRequestFallback) List(_ context.Context, _ pagination.PaginationParams) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _ string, _ *bool) ([]Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListActive(_ context.Context) ([]Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListActiveByPlatform(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatform call")
}
func (s *groupRepoStubForInvalidRequestFallback) ListActiveByPlatformLite(_ context.Context, _ string) ([]Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStubForInvalidRequestFallback) ExistsByName(_ context.Context, _ string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStubForInvalidRequestFallback) GetAccountCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetAccountCount call")
}

func (s *groupRepoStubForInvalidRequestFallback) DeleteAccountGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteAccountGroupsByGroupID call")
}

func (s *groupRepoStubForInvalidRequestFallback) GetAccountIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetAccountIDsByGroupIDs call")
}

func (s *groupRepoStubForInvalidRequestFallback) BindAccountsToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindAccountsToGroup call")
}

func (s *groupRepoStubForInvalidRequestFallback) UpdateSortOrders(_ context.Context, _ []GroupSortOrderUpdate) error {
	return nil
}

func TestAdminService_CreateGroup_InvalidRequestFallbackRejectsUnsupportedPlatform(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			fallbackID: {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                            "g1",
		Platform:                        PlatformOpenAI,
		RateMultiplier:                  1.0,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request fallback only supported for anthropic or antigravity groups")
	require.Nil(t, repo.created)
}

func TestAdminService_CreateGroup_InvalidRequestFallbackRejectsFallbackGroup(t *testing.T) {
	tests := []struct {
		name        string
		fallback    *Group
		wantMessage string
	}{
		{
			name:        "openai_target",
			fallback:    &Group{ID: 10, Platform: PlatformOpenAI},
			wantMessage: "fallback group must be anthropic platform",
		},
		{
			name:        "antigravity_target",
			fallback:    &Group{ID: 10, Platform: PlatformAntigravity},
			wantMessage: "fallback group must be anthropic platform",
		},
		{
			name: "nested_fallback",
			fallback: &Group{
				ID:                              10,
				Platform:                        PlatformAnthropic,
				FallbackGroupIDOnInvalidRequest: func() *int64 { v := int64(99); return &v }(),
			},
			wantMessage: "fallback group cannot have invalid request fallback configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fallbackID := tc.fallback.ID
			repo := &groupRepoStubForInvalidRequestFallback{
				groups: map[int64]*Group{
					fallbackID: tc.fallback,
				},
			}
			svc := &adminServiceImpl{groupRepo: repo}

			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name:                            "g1",
				Platform:                        PlatformAnthropic,
				RateMultiplier:                  1.0,
				FallbackGroupIDOnInvalidRequest: &fallbackID,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)
			require.Nil(t, repo.created)
		})
	}
}

func TestAdminService_CreateGroup_InvalidRequestFallbackNotFound(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                            "g1",
		Platform:                        PlatformAnthropic,
		RateMultiplier:                  1.0,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group not found")
	require.Nil(t, repo.created)
}

func TestAdminService_CreateGroup_InvalidRequestFallbackAllowsAnthropic(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			fallbackID: {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                            "g1",
		Platform:                        PlatformAnthropic,
		RateMultiplier:                  1.0,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.Equal(t, fallbackID, *repo.created.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_CreateGroup_InvalidRequestFallbackAllowsAntigravity(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			fallbackID: {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                            "g1",
		Platform:                        PlatformAntigravity,
		RateMultiplier:                  1.0,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.Equal(t, fallbackID, *repo.created.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_CreateGroup_InvalidRequestFallbackClearsOnZero(t *testing.T) {
	zero := int64(0)
	repo := &groupRepoStubForInvalidRequestFallback{}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                            "g1",
		Platform:                        PlatformAnthropic,
		RateMultiplier:                  1.0,
		FallbackGroupIDOnInvalidRequest: &zero,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.Nil(t, repo.created.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_CreateGroup_UnavailableFallbackAllowsSamePlatformActiveGroup(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			fallbackID: {ID: fallbackID, Platform: PlatformOpenAI, Status: StatusActive},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
		Name:                       "g1",
		Platform:                   PlatformOpenAI,
		RateMultiplier:             1.0,
		UnavailableFallbackGroupID: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.Equal(t, fallbackID, *repo.created.UnavailableFallbackGroupID)
}

func TestAdminService_CreateGroup_UnavailableFallbackRejectsInvalidGroup(t *testing.T) {
	tests := []struct {
		name        string
		fallback    *Group
		wantMessage string
	}{
		{
			name:        "platform_mismatch",
			fallback:    &Group{ID: 10, Platform: PlatformGemini, Status: StatusActive},
			wantMessage: "unavailable fallback group must use the same platform",
		},
		{
			name:        "inactive_target",
			fallback:    &Group{ID: 10, Platform: PlatformOpenAI, Status: StatusDisabled},
			wantMessage: "unavailable fallback group must be active",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fallbackID := tc.fallback.ID
			repo := &groupRepoStubForInvalidRequestFallback{
				groups: map[int64]*Group{
					fallbackID: tc.fallback,
				},
			}
			svc := &adminServiceImpl{groupRepo: repo}

			_, err := svc.CreateGroup(context.Background(), &CreateGroupInput{
				Name:                       "g1",
				Platform:                   PlatformOpenAI,
				RateMultiplier:             1.0,
				UnavailableFallbackGroupID: &fallbackID,
			})
			require.Error(t, err)
			require.Contains(t, err.Error(), tc.wantMessage)
			require.Nil(t, repo.created)
		})
	}
}

func TestAdminService_UpdateGroup_UnavailableFallbackRejectsSelf(t *testing.T) {
	existing := &Group{
		ID:       1,
		Name:     "g1",
		Platform: PlatformOpenAI,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{existing.ID: existing},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		UnavailableFallbackGroupID: &existing.ID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot set self as unavailable fallback group")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_UnavailableFallbackClearsOnZero(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:                         1,
		Name:                       "g1",
		Platform:                   PlatformOpenAI,
		Status:                     StatusActive,
		UnavailableFallbackGroupID: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformOpenAI, Status: StatusActive},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		UnavailableFallbackGroupID: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.UnavailableFallbackGroupID)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackPlatformMismatch(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:                              1,
		Name:                            "g1",
		Platform:                        PlatformAnthropic,
		Status:                          StatusActive,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		Platform: PlatformOpenAI,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request fallback only supported for anthropic or antigravity groups")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackClearsOnZero(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:                              1,
		Name:                            "g1",
		Platform:                        PlatformAnthropic,
		Status:                          StatusActive,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		Platform:                        PlatformOpenAI,
		FallbackGroupIDOnInvalidRequest: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackRejectsFallbackGroup(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:       1,
		Name:     "g1",
		Platform: PlatformAnthropic,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformOpenAI},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group must be anthropic platform")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackSetSuccess(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:       1,
		Name:     "g1",
		Platform: PlatformAnthropic,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackAllowsAntigravity(t *testing.T) {
	fallbackID := int64(10)
	existing := &Group{
		ID:       1,
		Name:     "g1",
		Platform: PlatformAntigravity,
		Status:   StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: PlatformAnthropic},
		},
	}
	svc := &adminServiceImpl{groupRepo: repo}

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}
