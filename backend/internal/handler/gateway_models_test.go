package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BrandonVee/TokenRouter/internal/pkg/antigravity"
	"github.com/BrandonVee/TokenRouter/internal/pkg/xai"
	middleware2 "github.com/BrandonVee/TokenRouter/internal/server/middleware"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type gatewayModelsAccountRepoStub struct {
	service.AccountRepository

	byGroup map[int64][]service.Account
}

type gatewayModelsChannelRepoStub struct {
	service.ChannelRepository

	channels       []service.Channel
	groupPlatforms map[int64]string
}

func (s *gatewayModelsChannelRepoStub) ListAll(ctx context.Context) ([]service.Channel, error) {
	channels := make([]service.Channel, len(s.channels))
	copy(channels, s.channels)
	return channels, nil
}

func (s *gatewayModelsChannelRepoStub) GetGroupPlatforms(ctx context.Context, groupIDs []int64) (map[int64]string, error) {
	platforms := make(map[int64]string, len(groupIDs))
	for _, groupID := range groupIDs {
		if platform, ok := s.groupPlatforms[groupID]; ok {
			platforms[groupID] = platform
		}
	}
	return platforms, nil
}

type gatewayModelsResponseForTest struct {
	Object string                    `json:"object"`
	Data   []gatewayModelItemForTest `json:"data"`
}

type gatewayModelItemForTest struct {
	ID                      string                                `json:"id"`
	Object                  string                                `json:"object"`
	Created                 int64                                 `json:"created"`
	OwnedBy                 string                                `json:"owned_by"`
	Type                    string                                `json:"type"`
	DisplayName             string                                `json:"display_name"`
	CreatedAt               string                                `json:"created_at"`
	SupportsReasoningEffort bool                                  `json:"supportsReasoningEffort"`
	ReasoningEffort         string                                `json:"reasoningEffort"`
	ReasoningEfforts        []gatewayReasoningEffortOptionForTest `json:"reasoningEfforts"`
}

type gatewayReasoningEffortOptionForTest struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Default bool   `json:"default"`
}

func (s *gatewayModelsAccountRepoStub) ListSchedulableByGroupID(ctx context.Context, groupID int64) ([]service.Account, error) {
	accounts, ok := s.byGroup[groupID]
	if !ok {
		return nil, nil
	}
	out := make([]service.Account, len(accounts))
	copy(out, accounts)
	return out, nil
}

func newGatewayModelsHandlerForTest(repo service.AccountRepository) *GatewayHandler {
	return newGatewayModelsHandlerWithChannelForTest(repo, nil)
}

// newGatewayModelsHandlerWithChannelForTest 构造可选渠道配置的模型接口处理器。
func newGatewayModelsHandlerWithChannelForTest(repo service.AccountRepository, channelService *service.ChannelService) *GatewayHandler {
	return &GatewayHandler{
		gatewayService: service.NewGatewayService(
			repo,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, channelService, nil, nil, nil, nil,
		),
	}
}

// newGatewayModelsChannelServiceForTest 构造模型接口测试使用的渠道服务。
func newGatewayModelsChannelServiceForTest(groupID int64, platform string, channel service.Channel) *service.ChannelService {
	channel.GroupIDs = []int64{groupID}
	repo := &gatewayModelsChannelRepoStub{
		channels:       []service.Channel{channel},
		groupPlatforms: map[int64]string{groupID: platform},
	}
	return service.NewChannelService(repo, nil)
}

func TestGatewayModels_GeminiGroupFallsBackToGeminiModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(20)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformGemini},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group:        &service.Group{ID: groupID, Platform: service.PlatformGemini},
		ModelMapping: map[string]string{"gemini-review": "gemini-2.5-flash", "wild-*": "gemini-2.5-flash"},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "list", got.Object)
	require.Contains(t, modelIDsForTest(got.Data), "gemini-2.5-flash")
	require.Contains(t, modelIDsForTest(got.Data), "gemini-review")
	require.NotContains(t, modelIDsForTest(got.Data), "wild-*")
	require.NotContains(t, modelIDsForTest(got.Data), "claude-sonnet-4-6")
}

func TestAntigravityModelsIncludesRequestableExactAPIKeyAlias(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defaults := antigravity.DefaultModels()
	require.NotEmpty(t, defaults)
	targetModel := defaults[0].ID

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{ModelMapping: map[string]string{
		"antigravity-review": targetModel,
		"wild-*":             targetModel,
		"missing":            "not-requestable",
	}})

	(&GatewayHandler{}).AntigravityModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Contains(t, ids, targetModel)
	require.Contains(t, ids, "antigravity-review")
	require.NotContains(t, ids, "wild-*")
	require.NotContains(t, ids, "missing")
}

func TestAntigravityModelsExcludesAliasWhoseTargetIsUnavailableToBoundGroup(t *testing.T) {
	gin.SetMode(gin.TestMode)
	defaults := antigravity.DefaultModels()
	require.GreaterOrEqual(t, len(defaults), 2)
	availableModel := defaults[0].ID
	unavailableModel := defaults[1].ID
	groupID := int64(46)
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		groupID: {
			{
				ID:          9,
				Platform:    service.PlatformAntigravity,
				Status:      service.StatusActive,
				Schedulable: true,
				Credentials: map[string]any{
					"model_mapping": map[string]any{availableModel: availableModel},
				},
			},
		},
	}})

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/antigravity/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		GroupID: &groupID,
		Group:   &service.Group{ID: groupID, Platform: service.PlatformAntigravity},
		ModelMapping: map[string]string{
			"available-alias":   availableModel,
			"unavailable-alias": unavailableModel,
		},
	})

	h.AntigravityModels(c)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Contains(t, ids, availableModel)
	require.Contains(t, ids, "available-alias")
	require.NotContains(t, ids, unavailableModel)
	require.NotContains(t, ids, "unavailable-alias")
}

func TestGatewayModelsCompositeKeyAggregatesMappingsInOrder(t *testing.T) {
	gin.SetMode(gin.TestMode)
	openAIGroupID := int64(50)
	anthropicGroupID := int64(51)
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		openAIGroupID: {
			{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{
				"model_mapping": map[string]any{"gpt-5": "gpt-5"},
			}},
		},
		anthropicGroupID: {
			{ID: 2, Platform: service.PlatformAnthropic, Credentials: map[string]any{
				"model_mapping": map[string]any{"claude-sonnet-4-6": "claude-sonnet-4-6"},
			}},
		},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		IsComposite: true,
		ModelMapping: map[string]string{
			"review":        "gpt-5",
			"missing-alias": "not-requestable",
			"wild-*":        "gpt-5",
		},
		User: &service.User{Status: service.StatusActive, AllowedGroups: []int64{anthropicGroupID}},
		CompositeGroups: []service.APIKeyCompositeGroup{
			{GroupID: openAIGroupID, Prefix: "GPT", SortOrder: 0, Group: &service.Group{ID: openAIGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}},
			{GroupID: anthropicGroupID, Prefix: "Claude", SortOrder: 1, Group: &service.Group{ID: anthropicGroupID, Platform: service.PlatformAnthropic, Status: service.StatusActive}},
		},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.NotEmpty(t, ids)
	require.Contains(t, ids, "GPT/review")
	require.NotContains(t, ids, "GPT/missing-alias")
	require.NotContains(t, ids, "GPT/wild-*")
	firstClaude := -1
	for index, id := range ids {
		if len(id) >= len("Claude/") && id[:len("Claude/")] == "Claude/" {
			firstClaude = index
			break
		}
		require.Contains(t, id, "GPT/")
	}
	require.Greater(t, firstClaude, 0)
	for _, id := range ids[firstClaude:] {
		require.Contains(t, id, "Claude/")
	}
	require.Contains(t, ids, "GPT/gpt-5")
	require.Contains(t, ids, "Claude/claude-sonnet-4-6")
}

func TestGatewayModelsCompositeKeyFiltersPreferredSubscriptionMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	allowedGroupID := int64(52)
	blockedGroupID := int64(53)
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		allowedGroupID: {{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"allowed-model": "allowed-model"},
		}}},
		blockedGroupID: {{ID: 2, Platform: service.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"blocked-model": "blocked-model"},
		}}},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		IsComposite: true,
		BillingMode: service.APIKeyBillingModeSubscription,
		User: &service.User{
			Status:        service.StatusActive,
			AllowedGroups: []int64{allowedGroupID, blockedGroupID},
		},
		CompositeGroups: []service.APIKeyCompositeGroup{
			{GroupID: allowedGroupID, Prefix: "Allowed", Group: &service.Group{ID: allowedGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true}},
			{GroupID: blockedGroupID, Prefix: "Blocked", Group: &service.Group{ID: blockedGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, IsExclusive: true}},
		},
	})
	context.Set(string(middleware2.ContextKeyAPIKeyBilling), &middleware2.APIKeyBillingContext{
		Mode:      service.APIKeyBillingModeSubscription,
		Source:    "subscription",
		Available: true,
		Subscription: &service.UserSubscription{Plan: &service.SubscriptionPlan{
			GroupIDs: []int64{allowedGroupID},
		}},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	ids := modelIDsForTest(got.Data)
	require.Contains(t, ids, "Allowed/allowed-model")
	require.NotContains(t, ids, "Blocked/blocked-model")
}

func TestGatewayModelsCompositeKeyFiltersRevokedMappings(t *testing.T) {
	gin.SetMode(gin.TestMode)
	publicGroupID := int64(60)
	exclusiveGroupID := int64(61)
	h := newGatewayModelsHandlerForTest(&gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		publicGroupID: {{ID: 1, Platform: service.PlatformOpenAI, Credentials: map[string]any{
			"model_mapping": map[string]any{"public-model": "public-model"},
		}}},
		exclusiveGroupID: {{ID: 2, Platform: service.PlatformAnthropic, Credentials: map[string]any{
			"model_mapping": map[string]any{"exclusive-model": "exclusive-model"},
		}}},
	}})

	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	context.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	context.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		IsComposite: true,
		User: &service.User{
			Status:               service.StatusActive,
			DisabledPublicGroups: []int64{publicGroupID},
			AllowedGroups:        nil,
		},
		CompositeGroups: []service.APIKeyCompositeGroup{
			{GroupID: publicGroupID, Prefix: "Public", Group: &service.Group{ID: publicGroupID, Platform: service.PlatformOpenAI, Status: service.StatusActive}},
			{GroupID: exclusiveGroupID, Prefix: "Private", Group: &service.Group{ID: exclusiveGroupID, Platform: service.PlatformAnthropic, Status: service.StatusActive, IsExclusive: true}},
		},
	})

	h.Models(context)

	require.Equal(t, http.StatusOK, recorder.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &got))
	require.Empty(t, got.Data)
}

// TestGatewayModels_AntigravityGroupKeepsDefaultModelMetadata 验证默认候选仍使用 Antigravity 的展示元数据。
func TestGatewayModels_AntigravityGroupKeepsDefaultModelMetadata(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(32)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAntigravity,
						Credentials: map[string]any{
							"model_whitelist": []string{},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformAntigravity},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotEmpty(t, got.Data)
	require.Equal(t, "claude-fable-5", got.Data[0].ID)
	require.Equal(t, "model", got.Data[0].Type)
	require.Equal(t, "Claude Fable 5", got.Data[0].DisplayName)
	require.Equal(t, "2026-06-09T00:00:00Z", got.Data[0].CreatedAt)
}

func TestGatewayModels_QoderGroupFallsBackToQoderModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(28)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformQoder},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformQoder},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, "list", got.Object)
	require.Contains(t, modelIDsForTest(got.Data), "deepseek-v4-pro")
	require.NotContains(t, modelIDsForTest(got.Data), "claude-sonnet-4-6")
}

// TestGatewayModels_Grok45AdvertisesReasoningEffortForGrokBuild 验证新增能力元数据不破坏旧兼容字段。
func TestGatewayModels_Grok45AdvertisesReasoningEffortForGrokBuild(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(4409)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformGrok,
						Credentials: map[string]any{
							"model_mapping": map[string]any{"grok-4.5": "grok-4.5"},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got.Data, 1)
	model := got.Data[0]
	require.Equal(t, "grok-4.5", model.ID)
	require.Equal(t, "model", model.Object)
	require.Equal(t, "xai", model.OwnedBy)
	require.Equal(t, "model", model.Type)
	require.Equal(t, "Grok 4.5", model.DisplayName)
	require.Equal(t, "2024-01-01T00:00:00Z", model.CreatedAt)
	require.True(t, model.SupportsReasoningEffort)
	require.Equal(t, "high", model.ReasoningEffort)
	require.Equal(t, []gatewayReasoningEffortOptionForTest{
		{Value: "low", Label: "Low"},
		{Value: "medium", Label: "Medium"},
		{Value: "high", Label: "High", Default: true},
	}, model.ReasoningEfforts)
}

// TestGatewayModels_GrokDefaultsExcludeBuiltinAliases 验证无显式账号范围时只展示默认模型目录。
func TestGatewayModels_GrokDefaultsExcludeBuiltinAliases(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(4410)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformGrok, Credentials: map[string]any{}},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformGrok},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, xai.DefaultModelIDs(), modelIDsForTest(got.Data))
	require.NotContains(t, modelIDsForTest(got.Data), "grok")
	require.NotContains(t, modelIDsForTest(got.Data), "grok-latest")

	mappedGroupID := int64(4411)
	mappedHandler := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				mappedGroupID: {
					{
						ID:       2,
						Platform: service.PlatformGrok,
						Credentials: map[string]any{
							"model_mapping": map[string]any{"grok": "grok-4.3"},
						},
					},
				},
			},
		},
	)
	mappedRecorder := httptest.NewRecorder()
	mappedContext, _ := gin.CreateTestContext(mappedRecorder)
	mappedContext.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	mappedContext.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: mappedGroupID, Platform: service.PlatformGrok},
	})

	mappedHandler.Models(mappedContext)

	require.Equal(t, http.StatusOK, mappedRecorder.Code)
	var mapped gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(mappedRecorder.Body.Bytes(), &mapped))
	require.Contains(t, modelIDsForTest(mapped.Data), "grok")
}

func TestGatewayModels_GeminiGroupFiltersMappedModelsByPlatform(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(21)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAnthropic,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"claude-sonnet-4-6": "claude-sonnet-4-6",
							},
						},
					},
					{
						ID:       2,
						Platform: service.PlatformGemini,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gemini-2.5-flash": "gemini-2.5-flash",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformGemini},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gemini-2.5-flash"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_CustomModelsListDisabledKeepsOriginalModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(22)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.5": "gpt-5.5",
								"gpt-5.4": "gpt-5.4",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: false,
				Models:  []string{"gpt-5.5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.4", "gpt-5.5"}, modelIDsForTest(got.Data))
	require.Empty(t, got.Data[0].Object)
	require.Zero(t, got.Data[0].Created)
	require.Empty(t, got.Data[0].OwnedBy)
	require.Equal(t, "2024-01-01T00:00:00Z", got.Data[0].CreatedAt)
}

func TestGatewayModels_CustomModelsListFiltersAndOrdersMappedModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(23)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.4":         "gpt-5.4",
								"gpt-5.5":         "gpt-5.5",
								"legacy-gpt-2024": "legacy-gpt-2024",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "missing-model", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_CustomModelsListKeepsConcreteModelAllowedByWildcardMapping(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(26)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAnthropic,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"claude-*": "claude-sonnet-4-6",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformAnthropic,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-sonnet-4-6"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"claude-sonnet-4-6"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_AnthropicCustomModelsListIncludesOAuthClaudeAndMappedDeepSeek(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(28)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAnthropic,
						Type:     service.AccountTypeOAuth,
					},
					{
						ID:       2,
						Platform: service.PlatformAnthropic,
						Type:     service.AccountTypeAPIKey,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"deepseek-v4-pro": "deepseek-v4-pro",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformAnthropic,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-fable-5", "claude-opus-4-8", "deepseek-v4-pro"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"claude-fable-5", "claude-opus-4-8", "deepseek-v4-pro"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_AnthropicCustomModelsListDisabledIncludesUnrestrictedDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(29)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAnthropic,
						Type:     service.AccountTypeOAuth,
					},
					{
						ID:       2,
						Platform: service.PlatformAnthropic,
						Type:     service.AccountTypeAPIKey,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"deepseek-v4-pro": "deepseek-v4-pro",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformAnthropic,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: false,
				Models:  []string{"claude-fable-5", "deepseek-v4-pro"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	modelIDs := modelIDsForTest(got.Data)
	require.Contains(t, modelIDs, "deepseek-v4-pro")
	require.Contains(t, modelIDs, "claude-opus-4-6")
}

func TestGatewayModels_AnthropicCustomModelsListDoesNotAddModelsOutsideResolvedCandidates(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(30)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformAnthropic,
						Type:     service.AccountTypeOAuth,
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformAnthropic,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"claude-opus-4-6-thinking", "claude-sonnet-4-5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, modelIDsForTest(got.Data))
}

func TestGatewayModels_CustomModelsListCanReturnEmptyWhenSelectionsUnavailable(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(24)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{
						ID:       1,
						Platform: service.PlatformOpenAI,
						Credentials: map[string]any{
							"model_mapping": map[string]any{
								"gpt-5.4": "gpt-5.4",
							},
						},
					},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, modelIDsForTest(got.Data))
}

func TestGatewayModels_CustomModelsListFiltersDefaultFallbackModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(25)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformOpenAI},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "legacy-gpt-2024", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_OpenAICustomModelsListKeepsOpenAIResponseShapeForDefaultFallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(27)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformOpenAI},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"gpt-5.5", "gpt-5.4"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"gpt-5.5", "gpt-5.4"}, modelIDsForTest(got.Data))
	require.Equal(t, "model", got.Data[0].Object)
	require.NotZero(t, got.Data[0].Created)
	require.Equal(t, "openai", got.Data[0].OwnedBy)
	require.Empty(t, got.Data[0].CreatedAt)
}

func TestGatewayModels_OpenAIUnrestrictedListKeepsOpenAIResponseShape(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(31)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {{ID: 1, Platform: service.PlatformOpenAI}},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)
	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.NotEmpty(t, got.Data)
	require.Equal(t, "model", got.Data[0].Object)
	require.NotZero(t, got.Data[0].Created)
	require.Equal(t, "openai", got.Data[0].OwnedBy)
	require.Empty(t, got.Data[0].CreatedAt)
}

func TestGatewayModels_QoderCustomModelsListFiltersDefaultFallbackModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(29)
	h := newGatewayModelsHandlerForTest(
		&gatewayModelsAccountRepoStub{
			byGroup: map[int64][]service.Account{
				groupID: {
					{ID: 1, Platform: service.PlatformQoder},
				},
			},
		},
	)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformQoder,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"deepseek-v4-pro", "claude-sonnet-4-6", "lite"},
			},
		},
	})

	h.Models(c)

	require.Equal(t, http.StatusOK, rec.Code)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"deepseek-v4-pro", "lite"}, modelIDsForTest(got.Data))
}

func TestGatewayModels_ChannelRestrictionEmptyDoesNotFallBackToDefaults(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(31)
	accountRepo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		groupID: {{ID: 1, Platform: service.PlatformOpenAI}},
	}}
	channelService := newGatewayModelsChannelServiceForTest(groupID, service.PlatformOpenAI, service.Channel{
		ID:                 81,
		Status:             service.StatusActive,
		RestrictModels:     true,
		BillingModelSource: service.BillingModelSourceRequested,
	})
	h := newGatewayModelsHandlerWithChannelForTest(accountRepo, channelService)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI},
	})

	h.Models(c)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Empty(t, got.Data)
}

func TestGatewayModels_CustomListIntersectsChannelFilteredModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	groupID := int64(32)
	price := 0.01
	accountRepo := &gatewayModelsAccountRepoStub{byGroup: map[int64][]service.Account{
		groupID: {{
			ID:       1,
			Platform: service.PlatformOpenAI,
			Credentials: map[string]any{
				"model_mapping": map[string]any{
					"allowed-model": "allowed-model",
					"blocked-model": "blocked-model",
				},
			},
		}},
	}}
	channelService := newGatewayModelsChannelServiceForTest(groupID, service.PlatformOpenAI, service.Channel{
		ID:                 82,
		Status:             service.StatusActive,
		RestrictModels:     true,
		BillingModelSource: service.BillingModelSourceRequested,
		ModelPricing: []service.ChannelModelPricing{{
			Platform:   service.PlatformOpenAI,
			Models:     []string{"allowed-model"},
			InputPrice: &price,
		}},
	})
	h := newGatewayModelsHandlerWithChannelForTest(accountRepo, channelService)

	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/models", nil)
	c.Set(string(middleware2.ContextKeyAPIKey), &service.APIKey{
		Group: &service.Group{
			ID:       groupID,
			Platform: service.PlatformOpenAI,
			ModelsListConfig: service.GroupModelsListConfig{
				Enabled: true,
				Models:  []string{"blocked-model", "allowed-model"},
			},
		},
	})

	h.Models(c)

	var got gatewayModelsResponseForTest
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Equal(t, []string{"allowed-model"}, modelIDsForTest(got.Data))
}

func modelIDsForTest(models []gatewayModelItemForTest) []string {
	ids := make([]string, 0, len(models))
	for _, model := range models {
		ids = append(ids, model.ID)
	}
	return ids
}
