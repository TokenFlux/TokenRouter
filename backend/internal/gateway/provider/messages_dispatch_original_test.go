package provider

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	xai "github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAIMessagesDispatchModelConfig(t *testing.T) {
	t.Parallel()

	cfg := routing.NormalizeMessagesDispatchConfig(routing.OpenAIMessagesDispatchModelConfig{
		OpusMappedModel:   " gpt-5.4-high ",
		SonnetMappedModel: "gpt-5.3-codex",
		HaikuMappedModel:  " gpt-5.4-mini-medium ",
		ExactModelMappings: map[string]string{
			" claude-sonnet-4-5-20250929 ": " gpt-5.2-high ",
			"":                             "gpt-5.4",
			"claude-opus-4-6":              " ",
		},
	}, NormalizeMessagesDispatchModel)

	require.Equal(t, "gpt-5.4", cfg.OpusMappedModel)
	require.Equal(t, "gpt-5.3-codex", cfg.SonnetMappedModel)
	require.Equal(t, "gpt-5.4-mini", cfg.HaikuMappedModel)
	require.Equal(t, map[string]string{
		"claude-sonnet-4-5-20250929": "gpt-5.2",
	}, cfg.ExactModelMappings)
}

func TestGroupResolveMessagesDispatchModel_RequiresExplicitFamilyMapping(t *testing.T) {
	t.Parallel()

	group := &routing.Group{Platform: capability.PlatformOpenAI}
	// 空配置不能再把 Claude 系列请求隐式改写为内置 GPT 模型。
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-opus-4-6"))
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-sonnet-4-5-20250929"))
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-haiku-4-5-20251001"))

	group.MessagesDispatchModelConfig = routing.OpenAIMessagesDispatchModelConfig{
		SonnetMappedModel: " gpt-5.4-high ",
	}
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-opus-4-6"))
	require.Equal(t, "gpt-5.4", ResolveMessagesDispatchModel(group, "claude-sonnet-4-5-20250929"))
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-haiku-4-5-20251001"))
}

func TestGroupResolveMessagesDispatchModel_GrokRequiresCrossClientMapping(t *testing.T) {
	original := xai.RuntimeModelMappingOptions()
	t.Cleanup(func() { xai.SetRuntimeModelMappingOptions(original) })
	group := &routing.Group{Platform: capability.PlatformGrok}

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{})
	require.Empty(t, ResolveMessagesDispatchModel(group, "claude-sonnet-4-5"))

	xai.SetRuntimeModelMappingOptions(xai.ModelMappingOptions{
		DefaultText:          "grok-build-0.1",
		EnableCrossClientMap: true,
	})
	require.Equal(t, "grok-build-0.1", ResolveMessagesDispatchModel(group, "claude-sonnet-4-5"))
	require.Equal(t, "grok-build-0.1", ResolveMessagesDispatchModel(group, "claude-opus-4-6"))
	require.Equal(t, "grok-build-0.1", ResolveMessagesDispatchModel(group, "claude-haiku-4-5"))
	require.Empty(t, ResolveMessagesDispatchModel(group, "grok"))
	require.Empty(t, ResolveMessagesDispatchModel(group, "gpt-5.3-codex"))
}

func TestSanitizeGroupMessagesDispatchFields_ClearsNonOpenAIPlatform(t *testing.T) {
	t.Parallel()

	group := &routing.Group{
		Platform:              capability.PlatformAnthropic,
		AllowMessagesDispatch: true,
		DefaultMappedModel:    "gpt-5.6-sol",
		MessagesDispatchModelConfig: routing.OpenAIMessagesDispatchModelConfig{
			SonnetMappedModel: "gpt-5.3-codex",
			ExactModelMappings: map[string]string{
				"claude-fable-5": "gpt-5.6-sol",
			},
		},
	}

	routing.SanitizeGroupMessagesDispatchFields(group)

	require.False(t, group.AllowMessagesDispatch)
	require.Empty(t, group.DefaultMappedModel)
	require.Equal(t, routing.OpenAIMessagesDispatchModelConfig{}, group.MessagesDispatchModelConfig)
}
