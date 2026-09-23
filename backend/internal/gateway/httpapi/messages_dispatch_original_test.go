package httpapi

import (
	"testing"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"

	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIMessagesDispatchMappedModel(t *testing.T) {
	t.Run("exact_claude_model_override_wins", func(t *testing.T) {
		apiKey := &apikey.APIKey{
			Group: &routing.Group{
				MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
					SonnetMappedModel: "gpt-5.2",
					ExactModelMappings: map[string]string{
						"claude-sonnet-4-5-20250929": "gpt-5.4-mini-high",
						"claude-fable-5":             "gpt-5.6-sol",
					},
				},
			},
		}
		require.Equal(t, "gpt-5.4-mini", ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-sonnet-4-5-20250929"))
		require.Equal(t, "gpt-5.6-sol", ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-fable-5"))
	})

	t.Run("requires_explicit_family_mapping", func(t *testing.T) {
		apiKey := &apikey.APIKey{Group: &routing.Group{
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
				SonnetMappedModel: "gpt-5.2",
			},
		}}
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-opus-4-6"))
		require.Equal(t, "gpt-5.2", ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-sonnet-4-5-20250929"))
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-haiku-4-5-20251001"))
	})

	t.Run("returns_empty_for_non_claude_or_missing_group", func(t *testing.T) {
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(nil, "claude-sonnet-4-5-20250929"))
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(&apikey.APIKey{}, "claude-sonnet-4-5-20250929"))
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(&apikey.APIKey{Group: &routing.Group{}}, "gpt-5.4"))
	})

	t.Run("grok_group_maps_claude_cli_model_to_grok_default", func(t *testing.T) {
		original := xai.RuntimeModelMappingOptions()
		t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
		xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{EnableCrossClientMap: true})
		apiKey := &apikey.APIKey{
			Group: &routing.Group{
				Platform: capability.PlatformGrok,
			},
		}
		require.Equal(t, "grok-4.6", ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-sonnet-4-5"))
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(apiKey, "grok"))
	})

	t.Run("does_not_fall_back_to_group_default_mapped_model", func(t *testing.T) {
		apiKey := &apikey.APIKey{
			Group: &routing.Group{
				DefaultMappedModel: "gpt-5.4",
			},
		}
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(apiKey, "gpt-5.4"))
		require.Empty(t, ResolveOpenAIMessagesDispatchMappedModel(apiKey, "claude-sonnet-4-5-20250929"))
	})
}

func TestResolveOpenAIMessagesAccountLayerModel_ChannelMappingPrecedesGroupDispatch(t *testing.T) {
	apiKey := &apikey.APIKey{
		Group: &routing.Group{
			MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
				ExactModelMappings: map[string]string{
					"channel-model": "dispatch-model",
				},
			},
		},
	}

	require.Equal(t, "dispatch-model", ResolveOpenAIMessagesAccountLayerModel(apiKey, "channel-model"))
	require.Equal(t, "client-alias", ResolveOpenAIMessagesAccountLayerModel(apiKey, "client-alias"))
	require.Equal(t, "gpt-5.4", ResolveOpenAIMessagesAccountLayerModel(apiKey, "gpt-5.4-high"))
}
