package postgres_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/stretchr/testify/require"
)

func TestGroupEntityToService_PreservesMessagesDispatchModelConfig(t *testing.T) {
	group := &dbent.Group{
		ID:             1,
		Name:           "openai-dispatch",
		Platform:       capability.PlatformOpenAI,
		Status:         routing.StatusActive,
		RateMultiplier: 1,
		AllowedProtocols: []protocol.ProtocolID{
			protocol.ProtocolAnthropicMessages,
			protocol.ProtocolOpenAIResponses,
			protocol.ProtocolOpenAIChatCompletions,
		},
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.4",
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			OpusMappedModel:   "gpt-5.4-nano",
			SonnetMappedModel: "gpt-5.3-codex",
			HaikuMappedModel:  "gpt-5.4-mini",
			ExactModelMappings: map[string]string{
				"claude-sonnet-4.5": "gpt-5.4-nano",
			},
		},
	}

	got := routingpostgres.GroupFromEnt(group)
	require.NotNil(t, got)
	require.Equal(t, group.AllowedProtocols, got.AllowedProtocols)
	require.Equal(t, group.MessagesDispatchModelConfig, got.MessagesDispatchModelConfig)
}

func TestGroupEntityToService_PreservesImageGenerationControls(t *testing.T) {
	group := &dbent.Group{
		ID:                   1,
		Name:                 "openai-images",
		Platform:             capability.PlatformOpenAI,
		Status:               routing.StatusActive,
		RateMultiplier:       1,
		AllowImageGeneration: true,
	}

	got := routingpostgres.GroupFromEnt(group)
	require.NotNil(t, got)
	require.True(t, got.AllowImageGeneration)
}
