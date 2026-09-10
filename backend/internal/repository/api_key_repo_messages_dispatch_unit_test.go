package repository

import (
	"context"
	"testing"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityToService_PreservesMessagesDispatchModelConfig(t *testing.T) {
	group := &dbent.Group{
		ID:             1,
		Name:           "openai-dispatch",
		Platform:       service.PlatformOpenAI,
		Status:         service.StatusActive,
		RateMultiplier: 1,
		AllowedProtocols: []service.GroupClientProtocol{
			service.ProtocolAnthropicMessages,
			service.ProtocolOpenAIResponses,
			service.ProtocolOpenAIChatCompletions,
		},
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
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
	require.Equal(t, group.AllowedProtocols, got.AllowedProtocols)
	require.Equal(t, group.MessagesDispatchModelConfig, got.MessagesDispatchModelConfig)
}

func TestGroupEntityToService_PreservesImageGenerationControls(t *testing.T) {
	group := &dbent.Group{
		ID:                   1,
		Name:                 "openai-images",
		Platform:             service.PlatformOpenAI,
		Status:               service.StatusActive,
		RateMultiplier:       1,
		AllowImageGeneration: true,
	}

	got := groupEntityToService(group)
	require.NotNil(t, got)
	require.True(t, got.AllowImageGeneration)
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
		SetAllowedProtocols([]service.GroupClientProtocol{
			service.ProtocolAnthropicMessages,
			service.ProtocolOpenAIResponses,
			service.ProtocolOpenAIChatCompletions,
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
	require.Equal(t, group.AllowedProtocols, got.Group.AllowedProtocols)
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
