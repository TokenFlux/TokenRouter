package repository

import (
	"context"
	"testing"

	dbent "github.com/BrandonVee/TokenRouter/ent"
	"github.com/BrandonVee/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityToService_PreservesMessagesDispatchModelConfig(t *testing.T) {
	group := &dbent.Group{
		ID:             1,
		Name:           "openai-dispatch",
		Platform:       service.PlatformOpenAI,
		Status:         service.StatusActive,
		RateMultiplier: 1,
		AllowedClientProtocols: []service.GroupClientProtocol{
			service.GroupClientProtocolAnthropicMessages,
			service.GroupClientProtocolOpenAIResponses,
			service.GroupClientProtocolOpenAIChatCompletions,
		},
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
		VideoModelPrices: map[string]map[string]float64{
			service.VideoPriceFamilyGrokImagineVideo15: {service.VideoBillingResolution720P: 0.14},
		},
		MessagesDispatchModelConfig: service.OpenAIMessagesDispatchModelConfig{
			OpusMappedModel:   "gpt-5.4-nano",
			SonnetMappedModel: "gpt-5.3-codex",
			HaikuMappedModel:  "gpt-5.4-mini",
			ExactModelMappings: map[string]string{
				"claude-sonnet-4.5": "gpt-5.4-nano",
			},
		},
	}

	got := groupEntityToService(group)
	require.NotNil(t, got)
	require.Equal(t, group.AllowedClientProtocols, got.AllowedClientProtocols)
	require.Equal(t, group.MessagesDispatchModelConfig, got.MessagesDispatchModelConfig)
	require.Equal(t, group.VideoModelPrices, got.VideoModelPrices)
}

func TestGroupEntityToService_PreservesImageGenerationControls(t *testing.T) {
	group := &dbent.Group{
		ID:                   1,
		Name:                 "openai-images",
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		RateMultiplier:       1,
		AllowImageGeneration: true,
		ImageRateIndependent: true,
		ImageRateMultiplier:  0.5,
	}

	got := groupEntityToService(group)
	require.NotNil(t, got)
	require.True(t, got.AllowImageGeneration)
	require.True(t, got.ImageRateIndependent)
	require.InDelta(t, 0.5, got.ImageRateMultiplier, 1e-12)
}

func TestAPIKeyRepository_GetByKeyForAuth_PreservesMessagesDispatchModelConfig_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "getbykey-auth-dispatch-unit@test.com")
	lbTopK := 4

	group, err := client.Group.Create().
		SetName("g-auth-dispatch-unit").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1).
		SetSchedulerType(string(service.GroupSchedulerTypeAdvanced)).
		SetAdvancedSchedulerOverrides(service.GroupAdvancedSchedulerOverrides{LBTopK: &lbTopK}).
		SetAllowedClientProtocols([]service.GroupClientProtocol{
			service.GroupClientProtocolAnthropicMessages,
			service.GroupClientProtocolOpenAIResponses,
			service.GroupClientProtocolOpenAIChatCompletions,
		}).
		SetAllowMessagesDispatch(true).
		SetDefaultMappedModel("gpt-5.4").
		SetMessagesDispatchModelConfig(service.OpenAIMessagesDispatchModelConfig{
			OpusMappedModel:   "gpt-5.4-nano",
			SonnetMappedModel: "gpt-5.3-codex",
			HaikuMappedModel:  "gpt-5.4-mini",
			ExactModelMappings: map[string]string{
				"claude-sonnet-4.5": "gpt-5.4-nano",
			},
		}).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{
		UserID:  user.ID,
		Key:     "sk-getbykey-auth-dispatch-unit",
		Name:    "Dispatch Key Unit",
		GroupID: &group.ID,
		Status:  service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.Equal(t, key.Name, got.Name)
	require.NotNil(t, got.Group)
	require.Equal(t, group.AllowedClientProtocols, got.Group.AllowedClientProtocols)
	require.Equal(t, group.MessagesDispatchModelConfig, got.Group.MessagesDispatchModelConfig)
	require.Equal(t, service.GroupSchedulerTypeAdvanced, got.Group.SchedulerType)
	require.NotNil(t, got.Group.AdvancedSchedulerOverrides.LBTopK)
	require.Equal(t, 4, *got.Group.AdvancedSchedulerOverrides.LBTopK)
}

func TestAPIKeyRepository_GetByKeyForAuth_PreservesImageGenerationControls_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "getbykey-auth-images-unit@test.com")

	group, err := client.Group.Create().
		SetName("g-auth-images-unit").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1).
		SetAllowImageGeneration(true).
		SetImageRateIndependent(true).
		SetImageRateMultiplier(0.5).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{
		UserID:  user.ID,
		Key:     "sk-getbykey-auth-images-unit",
		Name:    "Images Key Unit",
		GroupID: &group.ID,
		Status:  service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.AllowImageGeneration)
	require.True(t, got.Group.ImageRateIndependent)
	require.InDelta(t, 0.5, got.Group.ImageRateMultiplier, 1e-12)
}

func TestAPIKeyRepository_GetByKeyForAuth_PreservesSessionIsolation_SQLite(t *testing.T) {
	repo, client := newAPIKeyRepoSQLite(t)
	ctx := context.Background()
	user := mustCreateAPIKeyRepoUser(t, ctx, client, "getbykey-auth-session-isolation-unit@test.com")

	group, err := client.Group.Create().
		SetName("g-auth-session-isolation-unit").
		SetPlatform(service.PlatformOpenAI).
		SetStatus(service.StatusActive).
		SetRateMultiplier(1).
		SetSessionIsolationEnabled(true).
		Save(ctx)
	require.NoError(t, err)

	key := &service.APIKey{
		UserID:  user.ID,
		Key:     "sk-getbykey-auth-session-isolation-unit",
		Name:    "Session Isolation Key Unit",
		GroupID: &group.ID,
		Status:  service.StatusActive,
	}
	require.NoError(t, repo.Create(ctx, key))

	got, err := repo.GetByKeyForAuth(ctx, key.Key)
	require.NoError(t, err)
	require.NotNil(t, got.Group)
	require.True(t, got.Group.SessionIsolationEnabled)
}
