//go:build unit

package routing_test

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	context "context"

	http "net/http"

	testing "testing"

	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"

	s15httpx "github.com/TokenFlux/TokenRouter/internal/server/httpx"

	require "github.com/stretchr/testify/require"
)

func ptrGroupClientProtocols(value []protocol.ProtocolID) *[]protocol.ProtocolID {
	return &value
}

func TestAdminServiceCreateGroupUsesPlatformClientProtocolDefaults(t *testing.T) {
	tests := []struct {
		platform string
		want     []protocol.ProtocolID
	}{
		{capability.PlatformAnthropic, []protocol.ProtocolID{protocol.ProtocolAnthropicMessages}},
		{capability.PlatformOpenAI, []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions}},
		{capability.PlatformGemini, []protocol.ProtocolID{protocol.ProtocolGeminiGenerateContent}},
		{capability.PlatformAntigravity, []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolGeminiGenerateContent}},
		{capability.PlatformQoder, []protocol.ProtocolID{}},
		{capability.PlatformGrok, []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, "openai_images_generations", "openai_images_edits"}},
	}

	for _, tt := range tests {
		t.Run(tt.platform, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{Name: tt.platform, Platform: tt.platform, RateMultiplier: 1})

			require.NoError(t, err)
			require.Equal(t, tt.want, group.AllowedProtocols)
			require.NotNil(t, group.AllowedProtocols)
			require.False(t, group.AllowMessagesDispatch)
		})
	}
}

func TestAdminServiceCreateGroupDefaultsLongContextPricingOn(t *testing.T) {
	t.Run("omitted defaults on", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "default-long-context", Platform: capability.PlatformOpenAI, RateMultiplier: 1,
		})

		require.NoError(t, err)
		require.True(t, group.LongContextPricingEnabled)
		require.True(t, repo.created.LongContextPricingEnabled)
	})

	t.Run("explicit false remains off", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)
		disabled := false

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "disabled-long-context", Platform: capability.PlatformOpenAI, RateMultiplier: 1,
			LongContextPricingEnabled: &disabled,
		})

		require.NoError(t, err)
		require.False(t, group.LongContextPricingEnabled)
		require.False(t, repo.created.LongContextPricingEnabled)
	})
}

func TestAdminServiceGroupAvailabilityProbeConfigReturnsBadRequest(t *testing.T) {
	invalidRetries := routing.MaxGroupAvailabilityProbeMaxRetries + 1
	invalidConfig := routing.GroupAvailabilityProbeConfig{
		Enabled:    true,
		ModelID:    "gpt-5.4",
		Prompt:     "hi",
		MaxRetries: &invalidRetries,
	}

	t.Run("create rejects invalid config", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "invalid-probe", Platform: capability.PlatformOpenAI, RateMultiplier: 1,
			AvailabilityProbeConfig: invalidConfig,
		})

		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
		require.Equal(t, routing.InvalidGroupAvailabilityProbeConfigReason, apperror.Reason(err))
		require.Nil(t, repo.created)
	})

	t.Run("update rejects invalid config", func(t *testing.T) {
		existing := &routing.Group{ID: 7, Name: "existing", Platform: capability.PlatformOpenAI, Status: billing.StatusActive}
		repo := &groupRepoStubForAdmin{getByID: existing}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
			AvailabilityProbeConfig: &invalidConfig,
		})

		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
		require.Equal(t, routing.InvalidGroupAvailabilityProbeConfigReason, apperror.Reason(err))
		require.Nil(t, repo.updated)
	})
}

func TestAdminServiceGroupSchedulerTypeDefaultsValidatesAndUpdates(t *testing.T) {
	t.Run("create defaults to basic", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "default-scheduler", Platform: capability.PlatformGemini, RateMultiplier: 1,
		})

		require.NoError(t, err)
		require.Equal(t, routing.GroupSchedulerTypeBasic, group.SchedulerType)
		require.Equal(t, routing.GroupSchedulerTypeBasic, repo.created.SchedulerType)
	})

	t.Run("create accepts advanced", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "advanced-scheduler", Platform: capability.PlatformQoder, RateMultiplier: 1, SchedulerType: string(routing.GroupSchedulerTypeAdvanced),
		})

		require.NoError(t, err)
		require.Equal(t, routing.GroupSchedulerTypeAdvanced, group.SchedulerType)
	})

	t.Run("invalid value is rejected", func(t *testing.T) {
		svc := newOriginalGroupAdmin(&groupRepoStubForAdmin{}, nil, nil)

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "invalid-scheduler", Platform: capability.PlatformAnthropic, RateMultiplier: 1, SchedulerType: "weighted",
		})

		require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
		require.Equal(t, "INVALID_SCHEDULER_TYPE", apperror.Reason(err))
	})

	t.Run("update preserves explicit advanced choice", func(t *testing.T) {
		existing := &routing.Group{ID: 7, Name: "basic", Platform: capability.PlatformAnthropic, Status: billing.StatusActive, SchedulerType: routing.GroupSchedulerTypeBasic}
		repo := &groupRepoStubForAdmin{getByID: existing}
		svc := newOriginalGroupAdmin(repo, nil, nil)
		advanced := string(routing.GroupSchedulerTypeAdvanced)

		group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{SchedulerType: &advanced})

		require.NoError(t, err)
		require.Equal(t, routing.GroupSchedulerTypeAdvanced, group.SchedulerType)
		require.Equal(t, routing.GroupSchedulerTypeAdvanced, repo.updated.SchedulerType)
	})
}

func TestAdminServiceCreateGroupClientProtocolCompatibilityPrecedence(t *testing.T) {
	t.Run("legacy OpenAI switch is accepted when new field is omitted", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "legacy", Platform: capability.PlatformOpenAI, RateMultiplier: 1, AllowMessagesDispatch: true,
		})

		require.NoError(t, err)
		require.Equal(t, []protocol.ProtocolID{
			protocol.ProtocolAnthropicMessages,
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		}, group.AllowedProtocols)
		require.True(t, group.AllowMessagesDispatch)
	})

	t.Run("new field wins over legacy OpenAI switch", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name:                  "new-field",
			Platform:              capability.PlatformOpenAI,
			RateMultiplier:        1,
			AllowMessagesDispatch: true,
			AllowedProtocols: []protocol.ProtocolID{
				protocol.ProtocolOpenAIChatCompletions,
				protocol.ProtocolOpenAIResponses,
			},
		})

		require.NoError(t, err)
		require.Equal(t, []protocol.ProtocolID{
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		}, group.AllowedProtocols)
		require.False(t, group.AllowMessagesDispatch)
	})
}

func TestAdminServiceRejectsInvalidGroupClientProtocols(t *testing.T) {
	tests := []struct {
		name      string
		platform  string
		protocols []protocol.ProtocolID
	}{
		{"unknown", capability.PlatformQoder, []protocol.ProtocolID{"unknown"}},
		{"duplicate", capability.PlatformQoder, []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolAnthropicMessages}},
		{"unsupported", capability.PlatformOpenAI, []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions, protocol.ProtocolGeminiGenerateContent}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newOriginalGroupAdmin(&groupRepoStubForAdmin{}, nil, nil)
			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: tt.name, Platform: tt.platform, RateMultiplier: 1, AllowedProtocols: tt.protocols,
			})

			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, s15httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", apperror.Reason(err))
		})
	}
}

func TestAdminServiceAllowsEmptyGroupClientProtocolsForEveryPlatform(t *testing.T) {
	platforms := []string{
		capability.PlatformAnthropic,
		capability.PlatformOpenAI,
		capability.PlatformGemini,
		capability.PlatformAntigravity,
		capability.PlatformQoder,
		capability.PlatformGrok,
	}
	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			svc := newOriginalGroupAdmin(&groupRepoStubForAdmin{}, nil, nil)

			group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: platform, Platform: platform, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{},
			})

			require.NoError(t, err)
			require.NotNil(t, group.AllowedProtocols)
			require.Empty(t, group.AllowedProtocols)
		})
	}
}

func TestAdminServiceUpdateGroupPreservesExplicitEmptyClientProtocols(t *testing.T) {
	existing := &routing.Group{ID: 1, Name: "openai", Platform: capability.PlatformOpenAI, Status: billing.StatusActive, AllowedProtocols: []protocol.ProtocolID{}}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{})

	require.NoError(t, err)
	require.NotNil(t, group.AllowedProtocols)
	require.Empty(t, group.AllowedProtocols)
}

func TestAdminServiceUpdateGroupFiltersUnsupportedProtocolsWhenPlatformChanges(t *testing.T) {
	tests := []struct {
		name     string
		from     string
		to       string
		initial  []protocol.ProtocolID
		expected []protocol.ProtocolID
	}{
		{
			name: "Gemini to OpenAI",
			from: capability.PlatformGemini,
			to:   capability.PlatformOpenAI,
			initial: []protocol.ProtocolID{
				protocol.ProtocolAnthropicMessages,
				protocol.ProtocolOpenAIResponses,
				protocol.ProtocolOpenAIChatCompletions,
				protocol.ProtocolGeminiGenerateContent,
			},
			expected: []protocol.ProtocolID{
				protocol.ProtocolAnthropicMessages,
				protocol.ProtocolOpenAIResponses,
				protocol.ProtocolOpenAIChatCompletions,
			},
		},
		{
			name:    "OpenAI to Anthropic",
			from:    capability.PlatformOpenAI,
			to:      capability.PlatformAnthropic,
			initial: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions},
			expected: []protocol.ProtocolID{
				protocol.ProtocolOpenAIResponses,
				protocol.ProtocolOpenAIChatCompletions,
			},
		},
		{
			name:     "Qoder empty to Grok",
			from:     capability.PlatformQoder,
			to:       capability.PlatformGrok,
			initial:  []protocol.ProtocolID{},
			expected: []protocol.ProtocolID{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &routing.Group{
				ID: 1, Name: tt.name, Platform: tt.from, Status: billing.StatusActive,
				AllowedProtocols: tt.initial,
			}
			repo := &groupRepoStubForAdmin{getByID: existing}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{Platform: tt.to})

			require.NoError(t, err)
			require.Equal(t, tt.expected, group.AllowedProtocols)
		})
	}
}

func TestAdminServiceUpdateGroupNewClientProtocolsOverrideLegacySwitch(t *testing.T) {
	existing := &routing.Group{
		ID: 1, Name: "openai", Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
		AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions},
	}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	legacyEnabled := false

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		AllowedProtocols: ptrGroupClientProtocols([]protocol.ProtocolID{
			protocol.ProtocolAnthropicMessages,
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		}),
		AllowMessagesDispatch: &legacyEnabled,
	})

	require.NoError(t, err)
	require.True(t, group.AllowMessagesDispatch)
	require.True(t, group.AllowsClientProtocol(protocol.ProtocolAnthropicMessages))
}

// groupRepoStubForAdmin 用于测试 AdminService 的 GroupRepository Stub
type groupRepoStubForAdmin struct {
	created *routing.
		Group
	updated *routing.
		Group
	getByID *routing.
		Group
	getErr error // GetByID 返回的错误

	listWithFiltersCalls       int
	listWithFiltersParams      pagination.PaginationParams
	listWithFiltersPlatform    string
	listWithFiltersStatus      string
	listWithFiltersSearch      string
	listWithFiltersIsExclusive *bool
	listWithFiltersGroups      []routing.Group
	listWithFiltersResult      *pagination.PaginationResult
	listWithFiltersErr         error
	groupSortOrderLockCalls    int
}

func (s *groupRepoStubForAdmin) Create(_ context.Context, g *routing.Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForAdmin) Update(_ context.Context, g *routing.Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForAdmin) GetByID(_ context.Context, _ int64) (*routing.Group, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.getByID, nil
}

func (s *groupRepoStubForAdmin) GetByIDLite(_ context.Context, _ int64) (*routing.Group, error) {
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

func (s *groupRepoStubForAdmin) List(_ context.Context, _ pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForAdmin) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
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

func (s *groupRepoStubForAdmin) ListActive(_ context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatform(_ context.Context, _ string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForAdmin) ListActiveByPlatformLite(_ context.Context, _ string) ([]routing.Group, error) {
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

func (s *groupRepoStubForAdmin) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
}

// LockGroupSortOrder 记录创建流程是否申请了排序位置锁。
func (s *groupRepoStubForAdmin) LockGroupSortOrder(_ context.Context) error {
	s.groupSortOrderLockCalls++
	return nil
}

func TestAdminService_ListGroups_PassesSortParams(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []routing.Group{{ID: 1, Name: "g1"}},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, _, err := svc.ListGroups(context.Background(), 3, 25, capability.PlatformOpenAI, billing.StatusActive, "needle", nil, "account_count", "ASC")
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
		listWithFiltersGroups: []routing.Group{{ID: 1, Name: "g1"}},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, _, err := svc.ListGroups(context.Background(), 1, 20, "", "", "", nil, "session_isolation_enabled", "DESC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      1,
		PageSize:  20,
		SortBy:    "session_isolation_enabled",
		SortOrder: "DESC",
	}, repo.listWithFiltersParams)
}

func TestAdminService_CreateGroup_DefaultsGrokMediaGenerationEnabled(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:           "grok-media",
		Description:    "Grok media group",
		Platform:       capability.PlatformGrok,
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:           "anthropic-text",
		Description:    "Anthropic text group",
		Platform:       capability.PlatformAnthropic,
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                    "isolated-group",
		Platform:                capability.PlatformAnthropic,
		RateMultiplier:          1.0,
		SessionIsolationEnabled: true,
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.True(t, repo.created.SessionIsolationEnabled)
	require.True(t, group.SessionIsolationEnabled)
}

func TestAdminService_CreateGroup_DisablesBatchImageWhenImageGenerationDisabled(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                      "gemini-no-image",
		Description:               "Gemini group without image generation",
		Platform:                  capability.PlatformGemini,
		RateMultiplier:            1.0,
		AllowImageGeneration:      false,
		AllowBatchImageGeneration: true,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)

	require.True(t, repo.created.AllowImageGeneration)
	require.False(t, repo.created.AllowBatchImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
}

func TestAdminService_CreateGroup_DisablesBatchImageForNonGeminiPlatform(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                      "openai-image",
		Description:               "OpenAI image group",
		Platform:                  capability.PlatformOpenAI,
		RateMultiplier:            1.0,
		AllowImageGeneration:      true,
		AllowBatchImageGeneration: true,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.True(t, repo.created.AllowImageGeneration)
	require.False(t, repo.created.AllowBatchImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
}

// TestAdminService_CreateGroup_NormalizesOpenAIFastByPlatform 验证两个组级 Fast
// 开关只在 OpenAI 分组中保留。
func TestAdminService_CreateGroup_NormalizesOpenAIFastByPlatform(t *testing.T) {
	for _, tt := range []struct {
		name     string
		platform string
		want     bool
	}{
		{name: "openai", platform: capability.PlatformOpenAI, want: true},
		{name: "anthropic", platform: capability.PlatformAnthropic, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: "fast-" + tt.name, Platform: tt.platform, RateMultiplier: 1,
				ForceOpenAIFast: true, FreeOpenAIFast: true,
			})

			require.NoError(t, err)
			require.NotNil(t, group)
			require.Equal(t, tt.want, repo.created.ForceOpenAIFast)
			require.Equal(t, tt.want, repo.created.FreeOpenAIFast)
		})
	}
}

// TestAdminService_UpdateGroup_ClearsOpenAIFastWhenPlatformChanges 防止平台切换后
// 把旧分组的 Fast 配置带到不支持的协议。
func TestAdminService_UpdateGroup_ClearsOpenAIFastWhenPlatformChanges(t *testing.T) {
	existingGroup := &routing.Group{
		ID: 1, Name: "existing-fast", Platform: capability.PlatformOpenAI, Status: billing.StatusActive,
		ForceOpenAIFast: true, FreeOpenAIFast: true,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existingGroup.ID, &routing.UpdateGroupInput{Platform: capability.PlatformAnthropic})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.False(t, repo.updated.ForceOpenAIFast)
	require.False(t, repo.updated.FreeOpenAIFast)
}

func TestAdminService_UpdateGroup_PreservesImageGenerationControlsWhenOmitted(t *testing.T) {

	existingGroup := &routing.Group{
		ID:                   1,
		Name:                 "existing-group",
		Platform:             capability.PlatformOpenAI,
		Status:               billing.StatusActive,
		AllowImageGeneration: true,
		AllowedProtocols:     []protocol.ProtocolID{"openai_images_generations", "openai_images_edits"},
		ProtocolFallbacks:    map[protocol.ProtocolID]protocol.ProtocolID{},
		ResponsesImagePolicy: "inherit",
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	updatedDesc := "updated"
	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Description: &updatedDesc,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.True(t, repo.updated.AllowImageGeneration)
}

func TestAdminService_UpdateGroup_WithSessionIsolation(t *testing.T) {
	existingGroup := &routing.Group{
		ID:       1,
		Name:     "existing-group",
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	enabled := true

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		SessionIsolationEnabled: &enabled,
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.True(t, repo.updated.SessionIsolationEnabled)
	require.True(t, group.SessionIsolationEnabled)
}

func TestAdminService_UpdateGroup_DisablesBatchImageWhenImageGenerationDisabled(t *testing.T) {
	existingGroup := &routing.Group{
		ID:                        1,
		Name:                      "existing-gemini",
		Platform:                  capability.PlatformGemini,
		Status:                    billing.StatusActive,
		AllowImageGeneration:      true,
		AllowBatchImageGeneration: true,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	disabled := false

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		AllowImageGeneration: &disabled,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.False(t, repo.updated.AllowImageGeneration)
	require.False(t, repo.updated.AllowBatchImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
}

func TestAdminService_UpdateGroup_DisablesBatchImageWhenPlatformChangesFromGemini(t *testing.T) {
	existingGroup := &routing.Group{
		ID:                        1,
		Name:                      "existing-gemini",
		Platform:                  capability.PlatformGemini,
		Status:                    billing.StatusActive,
		AllowImageGeneration:      true,
		AllowBatchImageGeneration: true,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Platform: capability.PlatformOpenAI,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, capability.PlatformOpenAI, repo.updated.Platform)
	require.False(t, repo.updated.AllowBatchImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
}

func TestAdminService_UpdateGroup_ClearsDescriptionWhenEmptyString(t *testing.T) {
	existingGroup := &routing.Group{
		ID:          1,
		Name:        "existing-group",
		Description: "Auto-created default group",
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	empty := ""
	_, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Description: &empty,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, "", repo.updated.Description, "空字符串应清空分组描述")
}

func TestAdminService_UpdateGroup_PreservesDescriptionWhenNil(t *testing.T) {
	existingGroup := &routing.Group{
		ID:          1,
		Name:        "existing-group",
		Description: "keep me",
		Platform:    capability.PlatformOpenAI,
		Status:      billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Description: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, "keep me", repo.updated.Description, "nil 应保留原有分组描述")
}

func TestAdminService_CreateGroup_BatchImagePricingSettings(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	discount := 0.8
	hold := 0.9

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                         "batch-image-pricing",
		Platform:                     capability.PlatformGemini,
		RateMultiplier:               1,
		BatchImageDiscountMultiplier: &discount,
		BatchImageHoldMultiplier:     &hold,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.InDelta(t, 0.8, repo.created.BatchImageDiscountMultiplier, 1e-12)
	require.InDelta(t, 0.9, repo.created.BatchImageHoldMultiplier, 1e-12)
}

func TestAdminService_CreateGroup_RejectsHoldBelowDiscount(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	discount := 0.8
	hold := 0.6

	_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                         "batch-image-pricing-invalid",
		Platform:                     capability.PlatformGemini,
		RateMultiplier:               1,
		BatchImageDiscountMultiplier: &discount,
		BatchImageHoldMultiplier:     &hold,
	})
	require.Error(t, err)
	require.Nil(t, repo.created)
}

func TestAdminService_GroupBatchImagePricingValidation(t *testing.T) {
	tests := []struct {
		name  string
		input *routing.CreateGroupInput
	}{
		{
			name: "negative_discount",
			input: func() *routing.CreateGroupInput {
				v := -0.1
				return &routing.CreateGroupInput{Name: "bad-discount", RateMultiplier: 1, BatchImageDiscountMultiplier: &v}
			}(),
		},
		{
			name: "negative_hold",
			input: func() *routing.CreateGroupInput {
				v := -0.1
				return &routing.CreateGroupInput{Name: "bad-hold", RateMultiplier: 1, BatchImageHoldMultiplier: &v}
			}(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			_, err := svc.CreateGroup(context.Background(), tt.input)
			require.Error(t, err)
			require.Nil(t, repo.created)
		})
	}
}

func TestAdminService_UpdateGroup_ReasoningEffortMappingsTriState(t *testing.T) {
	tests := []struct {
		name  string
		input *routing.UpdateGroupInput
		want  []routing.ReasoningEffortMapping
	}{
		{
			name:  "nil preserves existing mappings",
			input: &routing.UpdateGroupInput{},
			want:  []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}},
		},
		{
			name: "empty array clears mappings",
			input: func() *routing.UpdateGroupInput {
				empty := []routing.ReasoningEffortMapping{}
				return &routing.UpdateGroupInput{ReasoningEffortMappings: &empty}
			}(),
			want: []routing.ReasoningEffortMapping{},
		},
		{
			name: "non empty array replaces and canonicalizes mappings",
			input: func() *routing.UpdateGroupInput {
				replacement := []routing.ReasoningEffortMapping{{From: " X-HIGH ", To: " high "}}
				return &routing.UpdateGroupInput{ReasoningEffortMappings: &replacement}
			}(),
			want: []routing.ReasoningEffortMapping{{From: "xhigh", To: "high"}},
		},
		{
			name: "model scoped mappings are canonicalized independently",
			input: func() *routing.UpdateGroupInput {
				replacement := []routing.ReasoningEffortMapping{
					{From: " MAX ", To: " low ", MatchType: "PREFIX", Model: " gpt "},
					{From: "max", To: "medium", Model: "gpt-5.4"},
				}
				return &routing.UpdateGroupInput{ReasoningEffortMappings: &replacement}
			}(),
			want: []routing.ReasoningEffortMapping{
				{From: "max", To: "low", MatchType: "prefix", Model: "gpt"},
				{From: "max", To: "medium", MatchType: "exact", Model: "gpt-5.4"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			existing := &routing.Group{
				ID:                      1,
				Name:                    "openai-group",
				Platform:                capability.PlatformOpenAI,
				Status:                  billing.StatusActive,
				ReasoningEffortMappings: []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}},
			}
			repo := &groupRepoStubForAdmin{getByID: existing}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			_, err := svc.UpdateGroup(context.Background(), existing.ID, tt.input)

			require.NoError(t, err)
			require.Equal(t, tt.want, repo.updated.ReasoningEffortMappings)
		})
	}
}

func TestAdminService_UpdateGroup_RejectsInvalidReasoningEffortMappings(t *testing.T) {
	existing := &routing.Group{
		ID:             1,
		Name:           "openai",
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1,
		Status:         billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{groups: map[int64]*routing.Group{existing.ID: existing}}
	svc := newOriginalGroupAdmin(repo, nil, nil)
	invalid := []routing.ReasoningEffortMapping{
		{From: "max", To: "xhigh"},
		{From: " MAX ", To: "high"},
	}

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		ReasoningEffortMappings: &invalid,
	})

	require.Error(t, err)
	require.Contains(t, err.Error(), "duplicate reasoning effort mapping source")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_ClearsReasoningPolicyForUnsupportedPlatform(t *testing.T) {
	existing := &routing.Group{
		ID:                          1,
		Name:                        "openai-group",
		Platform:                    capability.PlatformOpenAI,
		Status:                      billing.StatusActive,
		MaxReasoningEffort:          "medium",
		MaxReasoningEffortOverLimit: routing.ReasoningEffortOverLimitDeny,
		ReasoningEffortMappings:     []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}},
	}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{Platform: capability.PlatformGemini})

	require.NoError(t, err)
	require.Empty(t, repo.updated.MaxReasoningEffort)
	require.Equal(t, routing.ReasoningEffortOverLimitDowngrade, repo.updated.MaxReasoningEffortOverLimit)
	require.Empty(t, repo.updated.ReasoningEffortMappings)
}

func TestAdminService_UpdateGroup_NormalizesPeakRateWhenDisabled(t *testing.T) {
	existingGroup := &routing.Group{
		ID:                 1,
		Name:               "existing-group",
		Platform:           capability.PlatformOpenAI,
		Status:             billing.StatusActive,
		PeakRateEnabled:    true,
		PeakStart:          "14:00",
		PeakEnd:            "18:00",
		PeakRateMultiplier: 3,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	disabled := false
	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
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
	existingGroup := &routing.Group{
		ID:                 1,
		Name:               "existing-group",
		Platform:           capability.PlatformOpenAI,
		Status:             billing.StatusActive,
		PeakRateEnabled:    false,
		PeakStart:          "bad",
		PeakEnd:            "18:00",
		PeakRateMultiplier: -1,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{})
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:           "dispatch-group",
		Description:    "dispatch config",
		Platform:       capability.PlatformOpenAI,
		RateMultiplier: 1.0,
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
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
	require.Equal(t, routing.OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   "gpt-5.4",
		SonnetMappedModel: "gpt-5.3-codex",
		HaikuMappedModel:  "gpt-5.4-mini",
		ExactModelMappings: map[string]string{
			"claude-sonnet-4-5-20250929": "gpt-5.2",
		},
	}, repo.created.MessagesDispatchModelConfig)
}

func TestAdminService_UpdateGroup_NormalizesMessagesDispatchModelConfig(t *testing.T) {
	existingGroup := &routing.Group{
		ID:       1,
		Name:     "existing-group",
		Platform: capability.PlatformOpenAI,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		MessagesDispatchModelConfig: &routing.OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: " gpt-5.4-medium ",
			ExactModelMappings: map[string]string{
				" claude-haiku-4-5-20251001 ": " gpt-5.4-mini-high ",
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, routing.OpenAIMessagesDispatchModelConfig{
		SonnetMappedModel: "gpt-5.4",
		ExactModelMappings: map[string]string{
			"claude-haiku-4-5-20251001": "gpt-5.4-mini",
		},
	}, repo.updated.MessagesDispatchModelConfig)
}

func TestAdminService_CreateGroup_ClearsMessagesDispatchFieldsForNonOpenAIPlatform(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                  "anthropic-group",
		Description:           "non-openai",
		Platform:              capability.PlatformAnthropic,
		RateMultiplier:        1.0,
		AllowMessagesDispatch: true,
		AllowLive:             true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			OpusMappedModel: "gpt-5.4",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)
	require.False(t, repo.created.AllowMessagesDispatch)
	require.False(t, repo.created.AllowLive)
	require.Empty(t, repo.created.DefaultMappedModel)
	require.Equal(t, routing.OpenAIMessagesDispatchModelConfig{}, repo.created.MessagesDispatchModelConfig)
}

func TestAdminService_UpdateGroup_ClearsMessagesDispatchFieldsWhenPlatformChangesAwayFromOpenAI(t *testing.T) {
	existingGroup := &routing.Group{
		ID:                    1,
		Name:                  "existing-openai-group",
		Platform:              capability.PlatformOpenAI,
		Status:                billing.StatusActive,
		AllowMessagesDispatch: true,
		AllowLive:             true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.3-codex",
		},
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Platform: capability.PlatformAnthropic,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, capability.PlatformAnthropic, repo.updated.Platform)
	require.False(t, repo.updated.AllowMessagesDispatch)
	require.False(t, repo.updated.AllowLive)
	require.Empty(t, repo.updated.DefaultMappedModel)
	require.Equal(t, routing.OpenAIMessagesDispatchModelConfig{}, repo.updated.MessagesDispatchModelConfig)
}

func TestAdminService_ListGroups_WithSearch(t *testing.T) {

	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []routing.Group{{ID: 1, Name: "alpha"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 1},
		}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		groups, total, err := svc.ListGroups(context.Background(), 1, 20, "", "", "alpha", nil, "", "")
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.Equal(t, []routing.Group{{ID: 1, Name: "alpha"}}, groups)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 1, PageSize: 20}, repo.listWithFiltersParams)
		require.Equal(t, "alpha", repo.listWithFiltersSearch)
		require.Nil(t, repo.listWithFiltersIsExclusive)
	})

	t.Run("search 为空字符串时传递空字符串", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []routing.Group{},
			listWithFiltersResult: &pagination.PaginationResult{Total: 0},
		}
		svc := newOriginalGroupAdmin(repo, nil, nil)

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
			listWithFiltersGroups: []routing.Group{{ID: 2, Name: "beta"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 42},
		}
		svc := newOriginalGroupAdmin(repo, nil, nil)

		groups, total, err := svc.ListGroups(context.Background(), 3, 50, capability.PlatformAntigravity, billing.StatusActive, "beta", &isExclusive, "", "")
		require.NoError(t, err)
		require.Equal(t, int64(42), total)
		require.Equal(t, []routing.Group{{ID: 2, Name: "beta"}}, groups)

		require.Equal(t, 1, repo.listWithFiltersCalls)
		require.Equal(t, pagination.PaginationParams{Page: 3, PageSize: 50}, repo.listWithFiltersParams)
		require.Equal(t, capability.PlatformAntigravity, repo.listWithFiltersPlatform)
		require.Equal(t, billing.StatusActive, repo.listWithFiltersStatus)
		require.Equal(t, "beta", repo.listWithFiltersSearch)
		require.NotNil(t, repo.listWithFiltersIsExclusive)
		require.True(t, *repo.listWithFiltersIsExclusive)
	})
}

func TestAdminService_ValidateFallbackGroup_DetectsCycle(t *testing.T) {
	groupID := int64(1)
	fallbackID := int64(2)
	repo := &groupRepoStubForFallbackCycle{
		groups: map[int64]*routing.Group{
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	err := svc.ValidateFallbackGroup(context.Background(), groupID, fallbackID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group cycle")
}

type groupRepoStubForFallbackCycle struct {
	groups map[int64]*routing.Group
}

func (s *groupRepoStubForFallbackCycle) Create(_ context.Context, _ *routing.Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStubForFallbackCycle) Update(_ context.Context, _ *routing.Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStubForFallbackCycle) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	return s.GetByIDLite(ctx, id)
}

func (s *groupRepoStubForFallbackCycle) GetByIDLite(_ context.Context, id int64) (*routing.Group, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

func (s *groupRepoStubForFallbackCycle) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForFallbackCycle) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForFallbackCycle) List(_ context.Context, _ pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForFallbackCycle) ListWithFilters(_ context.Context, _ pagination.PaginationParams, _, _, _ string, _ *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStubForFallbackCycle) ListActive(_ context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForFallbackCycle) ListActiveByPlatform(_ context.Context, _ string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForFallbackCycle) ListActiveByPlatformLite(_ context.Context, _ string) ([]routing.Group, error) {
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

func (s *groupRepoStubForFallbackCycle) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
}

type groupRepoStubForInvalidRequestFallback struct {
	groups  map[int64]*routing.Group
	created *routing.Group
	updated *routing.Group
}

func (s *groupRepoStubForInvalidRequestFallback) Create(_ context.Context, g *routing.Group) error {
	s.created = g
	return nil
}

func (s *groupRepoStubForInvalidRequestFallback) Update(_ context.Context, g *routing.Group) error {
	s.updated = g
	return nil
}

func (s *groupRepoStubForInvalidRequestFallback) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	return s.GetByIDLite(ctx, id)
}

func (s *groupRepoStubForInvalidRequestFallback) GetByIDLite(_ context.Context, id int64) (*routing.Group, error) {
	if g, ok := s.groups[id]; ok {
		return g, nil
	}
	return nil, routing.ErrGroupNotFound
}

func (s *groupRepoStubForInvalidRequestFallback) Delete(_ context.Context, _ int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStubForInvalidRequestFallback) DeleteCascade(_ context.Context, _ int64) ([]int64, error) {
	panic("unexpected DeleteCascade call")
}

func (s *groupRepoStubForInvalidRequestFallback) List(_ context.Context, _ pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListWithFilters(_ context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {

	if params.Page != 1 || params.PageSize != 1 || params.SortBy != "sort_order" || params.SortOrder != "desc" || platform != "" || status != "" || search != "" || isExclusive != nil {
		panic("unexpected ListWithFilters call")
	}
	var last *routing.Group
	for _, group := range s.groups {
		group := group
		if last == nil || group.SortOrder > last.SortOrder {
			last = group
		}
	}
	if last == nil {
		return nil, &pagination.PaginationResult{Page: 1, PageSize: 1}, nil
	}
	return []routing.Group{*last}, &pagination.PaginationResult{Total: int64(len(s.groups)), Page: 1, PageSize: 1}, nil
}

func (s *groupRepoStubForInvalidRequestFallback) ListActive(_ context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListActiveByPlatform(_ context.Context, _ string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStubForInvalidRequestFallback) ListActiveByPlatformLite(_ context.Context, _ string) ([]routing.Group, error) {
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

func (s *groupRepoStubForInvalidRequestFallback) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
}

func TestAdminService_CreateGroup_InvalidRequestFallbackRejectsUnsupportedPlatform(t *testing.T) {
	fallbackID := int64(10)
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			fallbackID: {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                            "g1",
		Platform:                        capability.PlatformOpenAI,
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
		fallback    *routing.Group
		wantMessage string
	}{
		{
			name:        "openai_target",
			fallback:    &routing.Group{ID: 10, Platform: capability.PlatformOpenAI},
			wantMessage: "fallback group must be anthropic platform",
		},
		{
			name:        "antigravity_target",
			fallback:    &routing.Group{ID: 10, Platform: capability.PlatformAntigravity},
			wantMessage: "fallback group must be anthropic platform",
		},
		{
			name: "nested_fallback",
			fallback: &routing.Group{
				ID:                              10,
				Platform:                        capability.PlatformAnthropic,
				FallbackGroupIDOnInvalidRequest: func() *int64 { v := int64(99); return &v }(),
			},
			wantMessage: "fallback group cannot have invalid request fallback configured",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fallbackID := tc.fallback.ID
			repo := &groupRepoStubForInvalidRequestFallback{
				groups: map[int64]*routing.Group{
					fallbackID: tc.fallback,
				},
			}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name:                            "g1",
				Platform:                        capability.PlatformAnthropic,
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                            "g1",
		Platform:                        capability.PlatformAnthropic,
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
		groups: map[int64]*routing.Group{
			fallbackID: {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                            "g1",
		Platform:                        capability.PlatformAnthropic,
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
		groups: map[int64]*routing.Group{
			fallbackID: {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                            "g1",
		Platform:                        capability.PlatformAntigravity,
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
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                            "g1",
		Platform:                        capability.PlatformAnthropic,
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
		groups: map[int64]*routing.Group{
			fallbackID: {ID: fallbackID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:                       "g1",
		Platform:                   capability.PlatformOpenAI,
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
		fallback    *routing.Group
		wantMessage string
	}{
		{
			name:        "platform_mismatch",
			fallback:    &routing.Group{ID: 10, Platform: capability.PlatformGemini, Status: billing.StatusActive},
			wantMessage: "unavailable fallback group must use the same platform",
		},
		{
			name:        "inactive_target",
			fallback:    &routing.Group{ID: 10, Platform: capability.PlatformOpenAI, Status: billing.StatusDisabled},
			wantMessage: "unavailable fallback group must be active",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fallbackID := tc.fallback.ID
			repo := &groupRepoStubForInvalidRequestFallback{
				groups: map[int64]*routing.Group{
					fallbackID: tc.fallback,
				},
			}
			svc := newOriginalGroupAdmin(repo, nil, nil)

			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name:                       "g1",
				Platform:                   capability.PlatformOpenAI,
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
	existing := &routing.Group{
		ID:       1,
		Name:     "g1",
		Platform: capability.PlatformOpenAI,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{existing.ID: existing},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		UnavailableFallbackGroupID: &existing.ID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "cannot set self as unavailable fallback group")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_UnavailableFallbackClearsOnZero(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:                         1,
		Name:                       "g1",
		Platform:                   capability.PlatformOpenAI,
		Status:                     billing.StatusActive,
		UnavailableFallbackGroupID: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformOpenAI, Status: billing.StatusActive},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		UnavailableFallbackGroupID: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.UnavailableFallbackGroupID)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackPlatformMismatch(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:                              1,
		Name:                            "g1",
		Platform:                        capability.PlatformAnthropic,
		Status:                          billing.StatusActive,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		Platform: capability.PlatformOpenAI,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid request fallback only supported for anthropic or antigravity groups")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackClearsOnZero(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:                              1,
		Name:                            "g1",
		Platform:                        capability.PlatformAnthropic,
		Status:                          billing.StatusActive,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		Platform:                        capability.PlatformOpenAI,
		FallbackGroupIDOnInvalidRequest: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackRejectsFallbackGroup(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:       1,
		Name:     "g1",
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformOpenAI},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group must be anthropic platform")
	require.Nil(t, repo.updated)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackSetSuccess(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:       1,
		Name:     "g1",
		Platform: capability.PlatformAnthropic,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackAllowsAntigravity(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:       1,
		Name:     "g1",
		Platform: capability.PlatformAntigravity,
		Status:   billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Platform: capability.PlatformAnthropic},
		},
	}
	svc := newOriginalGroupAdmin(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}
