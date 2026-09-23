//go:build unit

package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestResolveMessagesDispatchModelCNProvidersSkipOpenAIMapping(t *testing.T) {
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		group := &routing.Group{
			Platform: platform,
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
				SonnetMappedModel: "gpt-5.4",
			},
		}
		require.Empty(t, ResolveMessagesDispatchModel(group, "claude-sonnet-4-5"), platform)
	}

	openAIGroup := &routing.Group{
		Platform: capability.PlatformOpenAI,
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.4",
		},
	}
	require.Equal(t, "gpt-5.4", ResolveMessagesDispatchModel(openAIGroup, "claude-sonnet-4-5"))
}
