package routing_test

import (
	"context"
	"errors"
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/apperror"
	"github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	"github.com/TokenFlux/TokenRouter/internal/server/httpx"
)

type groupRepoStub struct {
	affectedUserIDs []int64
	deleteErr       error
	deleteCalls     []int64
}

type deleteGroupAPIKeyRepoStub struct {
	keys         []string
	listErr      error
	listGroupIDs []int64
}

// groupModelsListProviderRepoStub 返回候选模型测试中的可调度提供商。
type groupModelsListProviderRepoStub struct {
	routing.GroupProviders
	providers     []provider.Record
	calledGroupID int64
}

// authCacheInvalidatorStub 记录管理操作提交后的缓存失效调用。
type authCacheInvalidatorStub struct {
	groupIDs []int64
	keys     []string
}

type groupRepoStubForFallbackCycle struct {
	groups map[int64]*routing.Group
}

type groupRepoStubForInvalidRequestFallback struct {
	groups  map[int64]*routing.Group
	created *routing.Group
	updated *routing.Group
}

// groupPlatformRepoStub 为 UpdateGroup 提供读取和更新方法，其余方法来自内嵌接口。
type groupPlatformRepoStub struct {
	routing.GroupRepository

	group     *routing.Group
	updated   *routing.Group
	updateErr error
}

type pricingConfigCacheInvalidatorSpy struct {
	calls int
}

func TestAdminService_DeleteGroup_Success(t *testing.T) {
	repo := &groupRepoStub{affectedUserIDs: []int64{11, 12}}
	svc := newGroupAdminForTest(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
}

func TestAdminService_DeleteGroup_InvalidatesAuthCacheForBoundKeys(t *testing.T) {
	repo := &groupRepoStub{}
	apiKeyRepo := &deleteGroupAPIKeyRepoStub{keys: []string{"k1", "k2"}}
	invalidator := &authCacheInvalidatorStub{}
	svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, invalidator, nil, apiKeyRepo)

	err := svc.DeleteGroup(context.Background(), 5)
	require.NoError(t, err)
	require.Equal(t, []int64{5}, repo.deleteCalls)
	require.Equal(t, []int64{5}, apiKeyRepo.listGroupIDs)
	require.Equal(t, []string{"k1", "k2"}, invalidator.keys)
}

func TestAdminService_DeleteGroup_NotFound(t *testing.T) {
	repo := &groupRepoStub{deleteErr: routing.ErrGroupNotFound}
	svc := newGroupAdminForTest(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 99)
	require.ErrorIs(t, err, routing.ErrGroupNotFound)
}

func TestAdminService_DeleteGroup_Error(t *testing.T) {
	deleteErr := errors.New("delete failed")
	repo := &groupRepoStub{deleteErr: deleteErr}
	svc := newGroupAdminForTest(repo, nil, nil)

	err := svc.DeleteGroup(context.Background(), 42)
	require.ErrorIs(t, err, deleteErr)
}

func TestAdminServiceGroupAdvancedSchedulerOverrides(t *testing.T) {
	t.Run("create deep copies sparse overrides", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newGroupAdminForTest(repo, nil, nil)
		overrides := routing.GroupAdvancedSchedulerOverrides{
			StickyWeightedEnabled: groupAdvancedSchedulerOverrideTestPointer(false),
			LBTopK:                groupAdvancedSchedulerOverrideTestPointer(3),
		}

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "advanced-overrides", RateMultiplier: 1,
			SchedulerType: string(routing.GroupSchedulerTypeAdvanced), AdvancedSchedulerOverrides: overrides,
		})

		require.NoError(t, err)
		require.False(t, *group.AdvancedSchedulerOverrides.StickyWeightedEnabled)
		require.Equal(t, 3, *repo.created.AdvancedSchedulerOverrides.LBTopK)
		*overrides.LBTopK = 99
		require.Equal(t, 3, *repo.created.AdvancedSchedulerOverrides.LBTopK)
	})

	t.Run("update retains omission and clears explicit empty object", func(t *testing.T) {
		existing := &routing.Group{
			ID: 7, Name: "advanced", Status: billing.StatusActive,
			SchedulerType: routing.GroupSchedulerTypeAdvanced,
			AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
				LBTopK: groupAdvancedSchedulerOverrideTestPointer(3),
			},
		}
		repo := &groupRepoStubForAdmin{getByID: existing}
		svc := newGroupAdminForTest(repo, nil, nil)

		unchanged, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{})
		require.NoError(t, err)
		require.Equal(t, 3, *unchanged.AdvancedSchedulerOverrides.LBTopK)

		empty := routing.GroupAdvancedSchedulerOverrides{}
		cleared, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{AdvancedSchedulerOverrides: &empty})
		require.NoError(t, err)
		require.Zero(t, cleared.AdvancedSchedulerOverrides)
		require.Zero(t, repo.updated.AdvancedSchedulerOverrides)
	})

	t.Run("invalid overrides are rejected", func(t *testing.T) {
		svc := newGroupAdminForTest(&groupRepoStubForAdmin{}, nil, nil)
		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "invalid-advanced-overrides", RateMultiplier: 1,
			AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
				LBTopK: groupAdvancedSchedulerOverrideTestPointer(0),
			},
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, "INVALID_ADVANCED_SCHEDULER_OVERRIDES", apperror.Reason(err))
	})

	t.Run("merged weight overflow is rejected", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		weights := policy.ConfigScoreWeights{Priority: math.MaxFloat64 * 0.75}
		svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, nil, &weights)

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "overflowing-advanced-overrides", RateMultiplier: 1,
			AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
				WeightLoad: groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64 * 0.75),
			},
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, "INVALID_ADVANCED_SCHEDULER_OVERRIDES", apperror.Reason(err))
		require.Nil(t, repo.created)
	})

	t.Run("update rejects merged weight overflow", func(t *testing.T) {
		existing := &routing.Group{
			ID: 8, Name: "existing-advanced-overrides",
			Status: billing.StatusActive, SchedulerType: routing.GroupSchedulerTypeAdvanced,
		}
		repo := &groupRepoStubForAdmin{getByID: existing}
		weights := policy.ConfigScoreWeights{Priority: math.MaxFloat64 * 0.75}
		svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, nil, &weights)
		overrides := routing.GroupAdvancedSchedulerOverrides{
			WeightLoad: groupAdvancedSchedulerOverrideTestPointer(math.MaxFloat64 * 0.75),
		}

		_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
			AdvancedSchedulerOverrides: &overrides,
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, "INVALID_ADVANCED_SCHEDULER_OVERRIDES", apperror.Reason(err))
		require.Nil(t, repo.updated)
	})

	t.Run("all zero base weights remain writable", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newGroupAdminForTest(repo, nil, nil)
		zero := 0.0

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "zero-base-advanced-overrides", RateMultiplier: 1,
			AdvancedSchedulerOverrides: routing.GroupAdvancedSchedulerOverrides{
				WeightPriority:      &zero,
				WeightLoad:          &zero,
				WeightQueue:         &zero,
				WeightErrorRate:     &zero,
				WeightTTFT:          &zero,
				WeightReset:         &zero,
				WeightQuotaHeadroom: &zero,
			},
		})

		require.NoError(t, err)
		require.NotNil(t, repo.created)
	})
}

// TestAdminService_GetGroupModelsListCandidates_UsesConfiguredRequestModels 检查 OpenAI-compatible 分组的候选来自配置的请求模型。
func TestAdminService_GetGroupModelsListCandidates_UsesConfiguredRequestModels(t *testing.T) {
	groupID := int64(10)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &routing.Group{ID: groupID},
	}
	providerRepo := &groupModelsListProviderRepoStub{
		providers: []provider.Record{
			{
				ID:       1,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"model_whitelist": []any{"deepseek-v4-pro", "deepseek-v4-flash"},
				},
			},
			{
				ID:       2,
				Platform: capability.PlatformAnthropic,
				Credentials: map[string]any{
					"model_whitelist": []any{"claude-sonnet-4-6"},
				},
			},
		},
	}
	svc := newGroupAdminPortsForTest(groupRepo, nil, nil, nil, providerRepo, nil, nil)

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, groupID, providerRepo.calledGroupID)
	require.Equal(t, []string{"claude-sonnet-4-6", "deepseek-v4-flash", "deepseek-v4-pro"}, models)
}

// TestGroupModelsListCandidatesApplyGroupMappingAndRestrictions 覆盖请求、分组映射和上游三个白名单阶段。
func TestGroupModelsListCandidatesApplyGroupMappingAndRestrictions(t *testing.T) {
	for _, stage := range []string{routing.BillingModelSourceRequested, routing.BillingModelSourceGroupMapped, routing.BillingModelSourceUpstream} {
		t.Run(stage, func(t *testing.T) {
			group := &routing.Group{ID: 59, RoutingPolicy: routing.GroupRoutingPolicy{
				Enabled: true, RestrictModels: true, RestrictionModelSource: stage,
				ModelMapping: map[string]string{
					"gemini-3.8-flash":  "gemini-3.8-flash-tiered",
					"gemini-3.7-flash":  "gemini-3.7-flash-tiered",
					"gemini-3.1-pro":    "gemini-3.1-pro-high",
					"unavailable-alias": "missing-model",
				},
			}}
			requested := []string{"gemini-3.1-pro", "gemini-3.7-flash", "gemini-3.8-flash"}
			mapped := []string{"gemini-3.1-pro-high", "gemini-3.7-flash-tiered", "gemini-3.8-flash-tiered"}
			allowed := requested
			if stage != routing.BillingModelSourceRequested {
				allowed = mapped
			}
			group.RoutingPolicy.AllowedModels = append(append([]string{}, allowed...), "unavailable-alias")
			providers := &groupModelsListProviderRepoStub{providers: []provider.Record{{
				ID: 3678, Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{"model_whitelist": mapped},
			}}}
			svc := newGroupAdminPortsForTest(&groupRepoStubForAdmin{getByID: group}, nil, nil, nil, providers, nil, nil)
			models, err := svc.GetGroupModelsListCandidates(context.Background(), group.ID, "")
			require.NoError(t, err)
			if stage == routing.BillingModelSourceRequested {
				require.Equal(t, requested, models)
			} else {
				require.ElementsMatch(t, append(append([]string{}, requested...), mapped...), models)
			}
			// 自定义列表筛选可请求的模型，已失效的上游名称会被排除。
			group.ModelsListConfig = routing.GroupModelsListConfig{Enabled: true, Models: []string{"gemini-3.8-flash", "unavailable-alias"}}
			models, err = svc.GetGroupModelsListCandidates(context.Background(), group.ID, "")
			require.NoError(t, err)
			require.Equal(t, []string{"gemini-3.8-flash"}, models)
		})
	}
}

// TestAdminServiceCustomModelsCannotInventUnsupportedModels 检查自定义模型缺少提供商支持时返回空列表。
func TestAdminServiceCustomModelsCannotInventUnsupportedModels(t *testing.T) {
	groupID := int64(12)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &routing.Group{
			ID: groupID,

			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-flash", "deepseek-v4-pro"},
			},
		},
	}
	providerRepo := &groupModelsListProviderRepoStub{
		providers: []provider.Record{
			{ID: 1, Platform: capability.PlatformOpenAI, Credentials: map[string]any{"model_whitelist": []string{"gpt-5.6-sol"}}},
		},
	}
	svc := newGroupAdminPortsForTest(groupRepo, nil, nil, nil, providerRepo, nil, nil)

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, groupID, providerRepo.calledGroupID)
	require.Empty(t, models)
	providerRepo.providers[0].Credentials["model_whitelist"] = []string{}
	models, err = svc.GetGroupModelsListCandidates(context.Background(), groupID, "")
	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-flash", "deepseek-v4-pro"}, models)
}

// TestAdminService_GetGroupModelsListCandidates_FiltersCustomModelsList 检查候选模型与自定义模型列表取交集。
func TestAdminService_GetGroupModelsListCandidates_FiltersCustomModelsList(t *testing.T) {
	groupID := int64(13)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &routing.Group{
			ID: groupID,

			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-pro", "gpt-5.5", "deepseek-v4-flash"},
			},
		},
	}
	providerRepo := &groupModelsListProviderRepoStub{
		providers: []provider.Record{
			{
				ID:       1,
				Platform: capability.PlatformOpenAI,
				Credentials: map[string]any{
					"model_whitelist": []any{"deepseek-v4-flash", "deepseek-v4-pro"},
				},
			},
		},
	}
	svc := newGroupAdminPortsForTest(groupRepo, nil, nil, nil, providerRepo, nil, nil)

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Equal(t, []string{"deepseek-v4-pro", "deepseek-v4-flash"}, models)
}

// TestAdminServiceGetGroupModelsListCandidatesKeepsEmptyIntersection 检查切换平台后的空模型交集。
func TestAdminServiceGetGroupModelsListCandidatesKeepsEmptyIntersection(t *testing.T) {
	groupID := int64(14)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &routing.Group{
			ID: groupID,

			ModelsListConfig: routing.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-flash"},
			},
		},
	}
	providerRepo := &groupModelsListProviderRepoStub{
		providers: []provider.Record{
			{
				ID:       1,
				Platform: capability.PlatformAnthropic,
				Credentials: map[string]any{
					"model_whitelist": []any{"claude-sonnet-4-6"},
				},
			},
		},
	}
	svc := newGroupAdminPortsForTest(groupRepo, nil, nil, nil, providerRepo, nil, nil)

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	require.Empty(t, models)
}

// TestAdminService_GetGroupModelsListCandidates_FallsBackToPlatformDefaults 默认目录仍受分组协议限制。
func TestAdminService_GetGroupModelsListCandidates_FallsBackToPlatformDefaults(t *testing.T) {
	groupID := int64(11)
	groupRepo := &groupRepoStubForAdmin{
		getByID: &routing.Group{ID: groupID},
	}
	providerRepo := &groupModelsListProviderRepoStub{
		providers: []provider.Record{
			{ID: 1, Platform: capability.PlatformOpenAI},
		},
	}
	svc := newGroupAdminPortsForTest(groupRepo, nil, nil, nil, providerRepo, nil, nil)

	models, err := svc.GetGroupModelsListCandidates(context.Background(), groupID, "")

	require.NoError(t, err)
	expected := provideradapter.DefaultProviderModels(&provider.Record{Platform: capability.PlatformOpenAI, Type: capability.ProviderTypeAPIKey})
	expected = slices.DeleteFunc(expected, func(model string) bool { return strings.HasPrefix(model, "text-embedding-") })
	require.ElementsMatch(t, expected, models)
	require.NotContains(t, models, "text-embedding-3-small")
}

func TestAdminService_CreateGroup_AppendsSortOrder(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []routing.Group{{ID: 9, SortOrder: 40}},
	}
	svc := newGroupAdminPortsForTest(repo, nil, nil, repo, nil, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:           "appended-group",
		RateMultiplier: 1,
	})

	require.NoError(t, err)
	require.Equal(t, 50, group.SortOrder)
	require.Equal(t, 50, repo.created.SortOrder)
	require.Equal(t, 1, repo.groupSortOrderLockCalls)
	require.Equal(t, pagination.PaginationParams{
		Page:      1,
		PageSize:  1,
		SortBy:    "sort_order",
		SortOrder: "desc",
	}, repo.listWithFiltersParams)
}

func TestAdminService_CreateGroup_PreservesExplicitSortOrder(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newGroupAdminPortsForTest(repo, nil, nil, repo, nil, nil, nil)
	explicitSortOrder := 5

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:           "explicit-sort-group",
		RateMultiplier: 1,
		SortOrder:      &explicitSortOrder,
	})

	require.NoError(t, err)
	require.Equal(t, explicitSortOrder, group.SortOrder)
	require.Equal(t, 0, repo.groupSortOrderLockCalls)
	require.Equal(t, 0, repo.listWithFiltersCalls)
}

// TestAdminService_UpdateGroup_OpenAIFastInvalidatesAuthCache 检查两个 Fast 字段更新后按分组失效认证缓存。
func TestAdminService_UpdateGroup_OpenAIFastInvalidatesAuthCache(t *testing.T) {
	existingGroup := &routing.Group{ID: 1, Name: "existing-fast", Status: billing.StatusActive}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	invalidator := &authCacheInvalidatorStub{}
	svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, invalidator, nil)
	enabled := true

	group, err := svc.UpdateGroup(context.Background(), existingGroup.ID, &routing.UpdateGroupInput{
		ForceOpenAIFast: &enabled,
	})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.True(t, repo.updated.ForceOpenAIFast)
	require.Equal(t, []int64{existingGroup.ID}, invalidator.groupIDs)
}

func TestAdminService_UpdateGroup_InvalidatesAuthCacheOnRPMLimitChange(t *testing.T) {
	existingGroup := &routing.Group{
		ID:   1,
		Name: "existing-group",

		Status:   billing.StatusActive,
		RPMLimit: 10,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	invalidator := &authCacheInvalidatorStub{}
	svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, invalidator, nil)

	rpmLimit := 60
	group, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		RPMLimit: &rpmLimit,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.Equal(t, 60, repo.updated.RPMLimit)
	require.Equal(t, []int64{1}, invalidator.groupIDs, "分组 RPMLimit 写入 auth snapshot，变更后必须失效 API Key 认证缓存")
}

// TestAdminGroupOpenAIFastPolicy 检查 Fast 策略优先于布尔输入，以及更新后的策略值和缓存失效。
func TestAdminGroupOpenAIFastPolicy(t *testing.T) {
	for _, policy := range []string{"follow_request", "force_priority", "force_ultrafast", "force_off"} {
		t.Run(policy, func(t *testing.T) {
			repo := &groupRepoStubForAdmin{}
			invalidator := &authCacheInvalidatorStub{}
			svc := newGroupAdminPortsForTest(repo, nil, nil, nil, nil, invalidator, nil)
			group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "fast", RateMultiplier: 1, ForceOpenAIFast: true, OpenAIFastPolicy: &policy})
			require.NoError(t, err)
			require.Equal(t, policy, group.OpenAIFastPolicy)
			require.Equal(t, policy == "force_priority", group.ForceOpenAIFast)
			group.ID = 1
			repo.getByID = group
			kept, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{})
			require.NoError(t, err)
			require.Equal(t, policy, kept.OpenAIFastPolicy)
			off := false
			changed, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{ForceOpenAIFast: &off})
			require.NoError(t, err)
			require.Equal(t, "follow_request", changed.OpenAIFastPolicy)
			changed, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{OpenAIFastPolicy: &policy})
			require.NoError(t, err)
			require.Equal(t, policy, changed.OpenAIFastPolicy)
			require.Contains(t, invalidator.groupIDs, int64(1))
			changed, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{})
			require.NoError(t, err)
			require.Equal(t, policy, changed.OpenAIFastPolicy)
		})
	}
}

// TestAdminServiceCreateGroupUsesUnifiedClientProtocolDefaults 验证新分组统一开放三个文本协议，非文本入口保持关闭。
func TestAdminServiceCreateGroupUsesUnifiedClientProtocolDefaults(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	group, err := newGroupAdminForTest(repo, nil, nil).CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "mixed", RateMultiplier: 1})
	require.NoError(t, err)
	require.Equal(t, []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions}, group.AllowedProtocols)
	require.Empty(t, group.ProtocolFallbacks)
	require.False(t, group.AllowImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
	require.False(t, group.AllowLive)
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
		svc := newGroupAdminForTest(repo, nil, nil)

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "invalid-probe", RateMultiplier: 1,
			AvailabilityProbeConfig: invalidConfig,
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, routing.InvalidGroupAvailabilityProbeConfigReason, apperror.Reason(err))
		require.Nil(t, repo.created)
	})

	t.Run("update rejects invalid config", func(t *testing.T) {
		existing := &routing.Group{ID: 7, Name: "existing", Status: billing.StatusActive}
		repo := &groupRepoStubForAdmin{getByID: existing}
		svc := newGroupAdminForTest(repo, nil, nil)

		_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
			AvailabilityProbeConfig: &invalidConfig,
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, routing.InvalidGroupAvailabilityProbeConfigReason, apperror.Reason(err))
		require.Nil(t, repo.updated)
	})
}

func TestAdminServiceGroupSchedulerTypeDefaultsValidatesAndUpdates(t *testing.T) {
	t.Run("create defaults to basic", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newGroupAdminForTest(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "default-scheduler", RateMultiplier: 1,
		})

		require.NoError(t, err)
		require.Equal(t, routing.GroupSchedulerTypeBasic, group.SchedulerType)
		require.Equal(t, routing.GroupSchedulerTypeBasic, repo.created.SchedulerType)
	})

	t.Run("create accepts advanced", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{}
		svc := newGroupAdminForTest(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "advanced-scheduler", RateMultiplier: 1, SchedulerType: string(routing.GroupSchedulerTypeAdvanced),
		})

		require.NoError(t, err)
		require.Equal(t, routing.GroupSchedulerTypeAdvanced, group.SchedulerType)
	})

	t.Run("invalid value is rejected", func(t *testing.T) {
		svc := newGroupAdminForTest(&groupRepoStubForAdmin{}, nil, nil)

		_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "invalid-scheduler", RateMultiplier: 1, SchedulerType: "weighted",
		})

		require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
		require.Equal(t, "INVALID_SCHEDULER_TYPE", apperror.Reason(err))
	})

	t.Run("update preserves explicit advanced choice", func(t *testing.T) {
		existing := &routing.Group{ID: 7, Name: "basic", Status: billing.StatusActive, SchedulerType: routing.GroupSchedulerTypeBasic}
		repo := &groupRepoStubForAdmin{getByID: existing}
		svc := newGroupAdminForTest(repo, nil, nil)
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
		svc := newGroupAdminForTest(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "legacy", RateMultiplier: 1, AllowMessagesDispatch: true,
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
		svc := newGroupAdminForTest(repo, nil, nil)

		group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
			Name: "new-field",

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := newGroupAdminForTest(&groupRepoStubForAdmin{}, nil, nil)
			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: tt.name, RateMultiplier: 1, AllowedProtocols: tt.protocols,
			})

			require.Error(t, err)
			require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
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
			svc := newGroupAdminForTest(&groupRepoStubForAdmin{}, nil, nil)

			group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: platform, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{},
			})

			require.NoError(t, err)
			require.NotNil(t, group.AllowedProtocols)
			require.Empty(t, group.AllowedProtocols)
		})
	}
}

func TestAdminServiceUpdateGroupPreservesExplicitEmptyClientProtocols(t *testing.T) {
	existing := &routing.Group{ID: 1, Name: "openai", Status: billing.StatusActive, AllowedProtocols: []protocol.ProtocolID{}}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{})

	require.NoError(t, err)
	require.NotNil(t, group.AllowedProtocols)
	require.Empty(t, group.AllowedProtocols)
}

// TestAdminServiceUpdateGroupPreservesAllConfiguredProtocols 验证更新名称或其他策略不会隐式修改客户端入口。
func TestAdminServiceUpdateGroupPreservesAllConfiguredProtocols(t *testing.T) {
	protocols := []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolOpenAIResponses, protocol.ProtocolGeminiGenerateContent, protocol.ProtocolImageBatches}
	repo := &groupRepoStubForAdmin{getByID: &routing.Group{ID: 1, Name: "before", Status: billing.StatusActive, AllowedProtocols: protocols}}
	group, err := newGroupAdminForTest(repo, nil, nil).UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{Name: "after"})
	require.NoError(t, err)
	require.Equal(t, protocols, group.AllowedProtocols)
	require.True(t, group.AllowBatchImageGeneration)
}

func TestAdminServiceUpdateGroupNewClientProtocolsOverrideLegacySwitch(t *testing.T) {
	existing := &routing.Group{
		ID: 1, Name: "openai", Status: billing.StatusActive,
		AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses, protocol.ProtocolOpenAIChatCompletions},
	}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newGroupAdminForTest(repo, nil, nil)
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

func TestAdminService_ListGroups_PassesSortParams(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []routing.Group{{ID: 1, Name: "g1"}},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	_, _, err := svc.ListGroups(context.Background(), 3, 25, capability.PlatformOpenAI, billing.StatusActive, "needle", nil, "provider_count", "ASC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      3,
		PageSize:  25,
		SortBy:    "provider_count",
		SortOrder: "ASC",
	}, repo.listWithFiltersParams)
}

func TestAdminService_ListGroups_PassesSessionIsolationSortParams(t *testing.T) {
	repo := &groupRepoStubForAdmin{
		listWithFiltersGroups: []routing.Group{{ID: 1, Name: "g1"}},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	_, _, err := svc.ListGroups(context.Background(), 1, 20, "", "", "", nil, "session_isolation_enabled", "DESC")
	require.NoError(t, err)
	require.Equal(t, pagination.PaginationParams{
		Page:      1,
		PageSize:  20,
		SortBy:    "session_isolation_enabled",
		SortOrder: "DESC",
	}, repo.listWithFiltersParams)
}

func TestAdminService_CreateGroup_PreservesNonGrokImageGenerationDisabled(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:        "anthropic-text",
		Description: "Anthropic text group",

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
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "isolated-group",

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
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name:        "gemini-no-image",
		Description: "Gemini group without image generation",

		RateMultiplier:            1.0,
		AllowImageGeneration:      false,
		AllowBatchImageGeneration: true,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.created)

	require.False(t, repo.created.AllowImageGeneration)
	require.False(t, repo.created.AllowBatchImageGeneration)
	require.False(t, group.AllowBatchImageGeneration)
}

// TestAdminServiceCreateGroupAllowsExplicitBatchProtocol 检查各平台分组均可配置批量图片协议，提供商在创建任务时选择。
func TestAdminServiceCreateGroupAllowsExplicitBatchProtocol(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	group, err := newGroupAdminForTest(repo, nil, nil).CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "batch", RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolImageBatches}})
	require.NoError(t, err)
	require.True(t, group.AllowBatchImageGeneration)
}

// TestAdminServiceCreateGroupPreservesFastPolicies 验证功能策略保存于分组，执行时仅由适用提供商使用。
func TestAdminServiceCreateGroupPreservesFastPolicies(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	group, err := newGroupAdminForTest(repo, nil, nil).CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "fast", RateMultiplier: 1, ForceOpenAIFast: true})
	require.NoError(t, err)
	require.True(t, group.ForceOpenAIFast)
}

func TestAdminServiceUpdateGroupPreservesFastPolicies(t *testing.T) {
	existingGroup := &routing.Group{
		ID: 1, Name: "existing-fast", Status: billing.StatusActive,
		ForceOpenAIFast: true,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existingGroup.ID, &routing.UpdateGroupInput{})

	require.NoError(t, err)
	require.NotNil(t, group)
	require.True(t, repo.updated.ForceOpenAIFast)
}

func TestAdminService_UpdateGroup_PreservesImageGenerationControlsWhenOmitted(t *testing.T) {
	existingGroup := &routing.Group{
		ID:   1,
		Name: "existing-group",

		Status:               billing.StatusActive,
		AllowImageGeneration: true,
		AllowedProtocols:     []protocol.ProtocolID{"openai_images_generations", "openai_images_edits"},
		ProtocolFallbacks:    map[protocol.ProtocolID][]protocol.ProtocolID{},
		ResponsesImagePolicy: "inherit",
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)

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
		ID:   1,
		Name: "existing-group",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)
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
		ID:   1,
		Name: "existing-gemini",

		Status:                    billing.StatusActive,
		AllowImageGeneration:      true,
		AllowBatchImageGeneration: true,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)
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

func TestAdminService_UpdateGroup_ClearsDescriptionWhenEmptyString(t *testing.T) {
	existingGroup := &routing.Group{
		ID:          1,
		Name:        "existing-group",
		Description: "Auto-created default group",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)

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

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForAdmin{getByID: existingGroup}
	svc := newGroupAdminForTest(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
		Description: nil,
	})
	require.NoError(t, err)
	require.NotNil(t, repo.updated)
	require.Equal(t, "keep me", repo.updated.Description, "nil 应保留原有分组描述")
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
				ID:   1,
				Name: "openai-group",

				Status:                  billing.StatusActive,
				ReasoningEffortMappings: []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}},
			}
			repo := &groupRepoStubForAdmin{getByID: existing}
			svc := newGroupAdminForTest(repo, nil, nil)

			_, err := svc.UpdateGroup(context.Background(), existing.ID, tt.input)

			require.NoError(t, err)
			require.Equal(t, tt.want, repo.updated.ReasoningEffortMappings)
		})
	}
}

func TestAdminService_UpdateGroup_RejectsInvalidReasoningEffortMappings(t *testing.T) {
	existing := &routing.Group{
		ID:   1,
		Name: "openai",

		RateMultiplier: 1,
		Status:         billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{groups: map[int64]*routing.Group{existing.ID: existing}}
	svc := newGroupAdminForTest(repo, nil, nil)
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

func TestAdminServiceUpdateGroupPreservesReasoningPolicy(t *testing.T) {
	existing := &routing.Group{
		ID:   1,
		Name: "openai-group",

		Status:                      billing.StatusActive,
		MaxReasoningEffort:          "medium",
		MaxReasoningEffortOverLimit: routing.ReasoningEffortOverLimitDeny,
		ReasoningEffortMappings:     []routing.ReasoningEffortMapping{{From: "max", To: "xhigh"}},
	}
	repo := &groupRepoStubForAdmin{getByID: existing}
	svc := newGroupAdminForTest(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{})

	require.NoError(t, err)
	require.Equal(t, "medium", repo.updated.MaxReasoningEffort)
	require.Equal(t, routing.ReasoningEffortOverLimitDeny, repo.updated.MaxReasoningEffortOverLimit)
	require.Equal(t, existing.ReasoningEffortMappings, repo.updated.ReasoningEffortMappings)
}

func TestAdminServiceUpdateGroupPreservesMessagesDispatchPolicy(t *testing.T) {
	existing := &routing.Group{ID: 1, Name: "mixed", Status: billing.StatusActive, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolLive}, DefaultMappedModel: "gpt-test"}
	repo := &groupRepoStubForAdmin{getByID: existing}
	group, err := newGroupAdminForTest(repo, nil, nil).UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.True(t, group.AllowMessagesDispatch)
	require.True(t, group.AllowLive)
	require.Equal(t, "gpt-test", group.DefaultMappedModel)
}

func TestAdminService_ListGroups_WithSearch(t *testing.T) {
	t.Run("search 参数正常传递到 repository 层", func(t *testing.T) {
		repo := &groupRepoStubForAdmin{
			listWithFiltersGroups: []routing.Group{{ID: 1, Name: "alpha"}},
			listWithFiltersResult: &pagination.PaginationResult{Total: 1},
		}
		svc := newGroupAdminForTest(repo, nil, nil)

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
		svc := newGroupAdminForTest(repo, nil, nil)

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
		svc := newGroupAdminForTest(repo, nil, nil)

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
	svc := newGroupAdminForTest(repo, nil, nil)

	err := svc.ValidateFallbackGroup(context.Background(), groupID, fallbackID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "fallback group cycle")
}

func TestAdminService_CreateGroup_InvalidRequestFallbackRejectsFallbackGroup(t *testing.T) {
	tests := []struct {
		name        string
		fallback    *routing.Group
		wantMessage string
	}{
		{
			name: "nested_fallback",
			fallback: &routing.Group{
				ID: 10,

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
			svc := newGroupAdminForTest(repo, nil, nil)

			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: "g1",

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
	svc := newGroupAdminForTest(repo, nil, nil)

	_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "g1",

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
			fallbackID: {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "g1",

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
			fallbackID: {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "g1",

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
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "g1",

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
			fallbackID: {ID: fallbackID, Status: billing.StatusActive},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
		Name: "g1",

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
			name:        "inactive_target",
			fallback:    &routing.Group{ID: 10, Status: billing.StatusDisabled},
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
			svc := newGroupAdminForTest(repo, nil, nil)

			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: "g1",

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
		ID:   1,
		Name: "g1",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{existing.ID: existing},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

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
		ID:   1,
		Name: "g1",

		Status:                     billing.StatusActive,
		UnavailableFallbackGroupID: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID, Status: billing.StatusActive},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		UnavailableFallbackGroupID: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.UnavailableFallbackGroupID)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackClearsOnZero(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:   1,
		Name: "g1",

		Status:                          billing.StatusActive,
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	clear := int64(0)
	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &clear,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Nil(t, repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminServiceUpdateGroupAllowsConfiguredFallback(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:   1,
		Name: "g1",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	_, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}

func TestAdminService_UpdateGroup_InvalidRequestFallbackSetSuccess(t *testing.T) {
	fallbackID := int64(10)
	existing := &routing.Group{
		ID:   1,
		Name: "g1",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

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
		ID:   1,
		Name: "g1",

		Status: billing.StatusActive,
	}
	repo := &groupRepoStubForInvalidRequestFallback{
		groups: map[int64]*routing.Group{
			existing.ID: existing,
			fallbackID:  {ID: fallbackID},
		},
	}
	svc := newGroupAdminForTest(repo, nil, nil)

	group, err := svc.UpdateGroup(context.Background(), existing.ID, &routing.UpdateGroupInput{
		FallbackGroupIDOnInvalidRequest: &fallbackID,
	})
	require.NoError(t, err)
	require.NotNil(t, group)
	require.NotNil(t, repo.updated)
	require.Equal(t, fallbackID, *repo.updated.FallbackGroupIDOnInvalidRequest)
}

// TestUpdateGroupDoesNotInvalidateIndependentPricingCache 检查更新分组名称和策略时的价格配置缓存失效次数。
func TestUpdateGroupDoesNotInvalidateIndependentPricingCache(t *testing.T) {
	repo := &groupPlatformRepoStub{group: &routing.Group{ID: 7, Name: "before"}}
	spy := &pricingConfigCacheInvalidatorSpy{}
	svc := newGroupAdminForTest(repo, nil, spy)
	got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{Name: "after"})
	require.NoError(t, err)
	require.Equal(t, "after", got.Name)
	require.Zero(t, spy.calls)
}

// TestUpdateGroupWithoutPricingConfigCacheInvalidator 检查缓存失效接口缺失时更新成功，缓存按 TTL 重建。
func TestUpdateGroupWithoutPricingConfigCacheInvalidator(t *testing.T) {
	repo := &groupPlatformRepoStub{group: &routing.Group{ID: 7, Name: "g"}}
	svc := newGroupAdminForTest(repo, nil, nil)

	got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{})
	require.NoError(t, err)
	require.Equal(t, "g", got.Name)
}

// TestUpdateGroupDoesNotInvalidatePricingConfigCacheWhenUpdateFails 检查分组更新失败时的价格配置缓存失效次数。
func TestUpdateGroupDoesNotInvalidatePricingConfigCacheWhenUpdateFails(t *testing.T) {
	repo := &groupPlatformRepoStub{
		group:     &routing.Group{ID: 7, Name: "g"},
		updateErr: errors.New("update failed"),
	}
	spy := &pricingConfigCacheInvalidatorSpy{}
	svc := newGroupAdminForTest(repo, nil, spy)

	got, err := svc.UpdateGroup(context.Background(), 7, &routing.UpdateGroupInput{})
	require.Error(t, err)
	require.Nil(t, got)
	require.Zero(t, spy.calls)
}

func TestProtocolGroupPersistenceAndCacheIsolation(t *testing.T) {
	repo := &groupRepoStubForAdmin{}
	svc := newGroupAdminForTest(repo, nil, nil)
	created, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{Name: "protocol", RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages, protocol.ProtocolImagesEdits}, ProtocolFallbacks: map[protocol.ProtocolID][]protocol.ProtocolID{protocol.ProtocolAnthropicMessages: {protocol.ProtocolOpenAIResponses}}, ResponsesImagePolicy: "disabled"})
	require.NoError(t, err)
	require.False(t, created.AllowsClientProtocol(protocol.ProtocolOpenAIResponses))
	require.True(t, created.AllowImageGeneration)
	repo.getByID = created
	created.ID = 1
	snapshot := apikey.KeyAuthGroupSnapshotFromGroup(created)
	restored := apikey.KeyGroupFromAuthSnapshot(snapshot)
	restored.ProtocolFallbacks[protocol.ProtocolAnthropicMessages][0] = protocol.ProtocolOpenAIChatCompletions
	require.Equal(t, []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}, snapshot.ProtocolFallbacks[protocol.ProtocolAnthropicMessages])
	require.Equal(t, "disabled", restored.ResponsesImagePolicy)
	protocols := []protocol.ProtocolID{protocol.ProtocolOpenAIChatCompletions}
	updated, err := svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{AllowedProtocols: &protocols, LegacyProtocolInput: true})
	require.NoError(t, err)
	require.Contains(t, updated.AllowedProtocols, protocol.ProtocolImagesEdits)
	require.NotContains(t, updated.AllowedProtocols, protocol.ProtocolImagesGenerations)
	updated, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{Name: "renamed"})
	require.NoError(t, err)
	require.NotContains(t, updated.AllowedProtocols, protocol.ProtocolImagesGenerations)
	empty := []protocol.ProtocolID{}
	updated, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{AllowedProtocols: &empty, ProtocolFallbacks: map[protocol.ProtocolID][]protocol.ProtocolID{}, ResponsesImagePolicy: "block"})
	require.NoError(t, err)
	require.Empty(t, updated.AllowedProtocols)
	require.Empty(t, updated.ProtocolFallbacks)
	_, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{ProtocolFallbacks: map[protocol.ProtocolID][]protocol.ProtocolID{protocol.ProtocolEmbeddings: {protocol.ProtocolOpenAIResponses}}})
	require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
}

// TestGroupProtocolLegacyPatchDoesNotHideInvalidInput 检查协议集合校验先于媒体补丁，重复或未知的协议返回输入错误。
func TestGroupProtocolLegacyPatchDoesNotHideInvalidInput(t *testing.T) {
	for _, protocols := range [][]protocol.ProtocolID{
		{"unknown"},
		{protocol.ProtocolImagesEdits, protocol.ProtocolImagesEdits},
	} {
		t.Run(string(protocols[0]), func(t *testing.T) {
			enabled := true
			repo := &groupRepoStubForAdmin{}
			svc := newGroupAdminForTest(repo, nil, nil)
			_, err := svc.CreateGroup(context.Background(), &routing.CreateGroupInput{
				Name: "invalid", RateMultiplier: 1,
				AllowedProtocols: protocols, LegacyProtocolInput: true, AllowImageGeneration: enabled,
			})
			require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", apperror.Reason(err))
			require.Nil(t, repo.created)
			repo.getByID = &routing.Group{ID: 1, RateMultiplier: 1, AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolOpenAIResponses}}
			_, err = svc.UpdateGroup(context.Background(), 1, &routing.UpdateGroupInput{
				AllowedProtocols: &protocols, LegacyProtocolInput: true, AllowImageGeneration: &enabled,
			})
			require.Equal(t, http.StatusBadRequest, httpx.ErrorCode(err))
			require.Equal(t, "INVALID_ALLOWED_CLIENT_PROTOCOLS", apperror.Reason(err))
			require.Nil(t, repo.updated)
		})
	}
}

func (s *groupRepoStub) Create(ctx context.Context, group *routing.Group) error {
	panic("unexpected Create call")
}

func (s *groupRepoStub) GetByID(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByID call")
}

func (s *groupRepoStub) GetByIDLite(ctx context.Context, id int64) (*routing.Group, error) {
	panic("unexpected GetByIDLite call")
}

func (s *groupRepoStub) Update(ctx context.Context, group *routing.Group) error {
	panic("unexpected Update call")
}

func (s *groupRepoStub) Delete(ctx context.Context, id int64) error {
	panic("unexpected Delete call")
}

func (s *groupRepoStub) DeleteCascade(ctx context.Context, id int64) ([]int64, error) {
	s.deleteCalls = append(s.deleteCalls, id)
	return s.affectedUserIDs, s.deleteErr
}

func (s *groupRepoStub) List(ctx context.Context, params pagination.PaginationParams) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected List call")
}

func (s *groupRepoStub) ListWithFilters(ctx context.Context, params pagination.PaginationParams, platform, status, search string, isExclusive *bool) ([]routing.Group, *pagination.PaginationResult, error) {
	panic("unexpected ListWithFilters call")
}

func (s *groupRepoStub) ListActive(ctx context.Context) ([]routing.Group, error) {
	panic("unexpected ListActive call")
}

func (s *groupRepoStub) ListActiveByPlatform(ctx context.Context, platform string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatform call")
}

func (s *groupRepoStub) ListActiveByPlatformLite(ctx context.Context, platform string) ([]routing.Group, error) {
	panic("unexpected ListActiveByPlatformLite call")
}

func (s *groupRepoStub) ExistsByName(ctx context.Context, name string) (bool, error) {
	panic("unexpected ExistsByName call")
}

func (s *groupRepoStub) GetProviderCount(ctx context.Context, groupID int64) (int64, int64, error) {
	panic("unexpected GetProviderCount call")
}

func (s *groupRepoStub) DeleteProviderGroupsByGroupID(ctx context.Context, groupID int64) (int64, error) {
	panic("unexpected DeleteProviderGroupsByGroupID call")
}

func (s *groupRepoStub) BindProvidersToGroup(ctx context.Context, groupID int64, providerIDs []int64) error {
	panic("unexpected BindProvidersToGroup call")
}

func (s *groupRepoStub) GetProviderIDsByGroupIDs(ctx context.Context, groupIDs []int64) ([]int64, error) {
	panic("unexpected GetProviderIDsByGroupIDs call")
}

func (s *groupRepoStub) UpdateSortOrders(ctx context.Context, updates []routing.GroupSortOrderUpdate) error {
	return nil
}

func (s *deleteGroupAPIKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	s.listGroupIDs = append(s.listGroupIDs, groupID)
	if s.listErr != nil {
		return nil, s.listErr
	}
	return s.keys, nil
}

func (s *groupModelsListProviderRepoStub) ListSchedulableByGroupID(_ context.Context, groupID int64) ([]routing.CatalogueProvider, error) {
	s.calledGroupID = groupID
	records := append([]provider.Record(nil), s.providers...)
	for i := range records {
		if records[i].Type == "" {
			records[i].Type = capability.ProviderTypeAPIKey
		}
	}
	return gatewayprovider.CatalogueProviders(records), nil
}

// groupAdvancedSchedulerOverrideTestPointer 为可选的调度参数生成指针。
func groupAdvancedSchedulerOverrideTestPointer[T any](value T) *T { return &value }

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByGroupID(_ context.Context, id int64) {
	s.groupIDs = append(s.groupIDs, id)
}

func (s *authCacheInvalidatorStub) InvalidateAuthCacheByKey(_ context.Context, key string) {
	s.keys = append(s.keys, key)
}

func ptrGroupClientProtocols(value []protocol.ProtocolID) *[]protocol.ProtocolID {
	return &value
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

func (s *groupRepoStubForFallbackCycle) GetProviderCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetProviderCount call")
}

func (s *groupRepoStubForFallbackCycle) DeleteProviderGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteProviderGroupsByGroupID call")
}

func (s *groupRepoStubForFallbackCycle) BindProvidersToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindProvidersToGroup call")
}

func (s *groupRepoStubForFallbackCycle) GetProviderIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetProviderIDsByGroupIDs call")
}

func (s *groupRepoStubForFallbackCycle) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
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

func (s *groupRepoStubForInvalidRequestFallback) GetProviderCount(_ context.Context, _ int64) (int64, int64, error) {
	panic("unexpected GetProviderCount call")
}

func (s *groupRepoStubForInvalidRequestFallback) DeleteProviderGroupsByGroupID(_ context.Context, _ int64) (int64, error) {
	panic("unexpected DeleteProviderGroupsByGroupID call")
}

func (s *groupRepoStubForInvalidRequestFallback) GetProviderIDsByGroupIDs(_ context.Context, _ []int64) ([]int64, error) {
	panic("unexpected GetProviderIDsByGroupIDs call")
}

func (s *groupRepoStubForInvalidRequestFallback) BindProvidersToGroup(_ context.Context, _ int64, _ []int64) error {
	panic("unexpected BindProvidersToGroup call")
}

func (s *groupRepoStubForInvalidRequestFallback) UpdateSortOrders(_ context.Context, _ []routing.GroupSortOrderUpdate) error {
	return nil
}

func (r *groupPlatformRepoStub) GetByID(_ context.Context, _ int64) (*routing.Group, error) {
	cloned := *r.group
	return &cloned, nil
}

func (r *groupPlatformRepoStub) Update(_ context.Context, group *routing.Group) error {
	if r.updateErr != nil {
		return r.updateErr
	}
	r.updated = group
	return nil
}

func (s *pricingConfigCacheInvalidatorSpy) InvalidateCache() { s.calls++ }
