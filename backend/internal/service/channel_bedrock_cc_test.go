package service

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/stretchr/testify/require"
)

func TestChannel_IsBedrockCCCompatEnabled_Enabled(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: true,
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestChannel_IsBedrockCCCompatEnabled_AppliesToAllPlatforms(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: true,
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.True(t, c.IsBedrockCCCompatEnabled("openai"))
	require.True(t, c.IsBedrockCCCompatEnabled(""))
}

func TestChannel_IsBedrockCCCompatEnabled_Disabled(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: false,
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestChannel_IsBedrockCCCompatEnabled_NilFeaturesConfig(t *testing.T) {
	c := &routing.Channel{FeaturesConfig: nil}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestChannel_IsBedrockCCCompatEnabled_NilChannel(t *testing.T) {
	var c *routing.Channel
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestChannel_IsBedrockCCCompatEnabled_WrongType(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: "yes",
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestChannel_IsBedrockCCCompatEnabled_OldMapFormat(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: map[string]any{"anthropic": true},
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.False(t, c.IsBedrockCCCompatEnabled("openai"))
}

func TestChannel_IsBedrockCCCompatEnabled_OldBoolMapFormat(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: map[string]bool{"anthropic": true},
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.False(t, c.IsBedrockCCCompatEnabled("openai"))
}

func TestChannel_IsBedrockCCCompatEnabled_MissingKey(t *testing.T) {
	c := &routing.Channel{
		FeaturesConfig: map[string]any{
			"other_feature": true,
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}
