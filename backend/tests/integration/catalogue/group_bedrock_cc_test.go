package catalogue_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/upstream/bedrock"
	"github.com/stretchr/testify/require"
)

func TestGroupPolicy_IsBedrockCCCompatEnabled_Enabled(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: true,
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_AppliesToAllPlatforms(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: true,
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.True(t, c.IsBedrockCCCompatEnabled("openai"))
	require.True(t, c.IsBedrockCCCompatEnabled(""))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_Disabled(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: false,
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_NilFeaturesConfig(t *testing.T) {
	c := &testkit.Configuration{FeaturesConfig: nil}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_NilGroupPolicy(t *testing.T) {
	var c *testkit.Configuration
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_WrongType(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: "yes",
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_OldMapFormat(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: map[string]any{"anthropic": true},
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.False(t, c.IsBedrockCCCompatEnabled("openai"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_OldBoolMapFormat(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			bedrock.FeatureKeyBedrockCCCompat: map[string]bool{"anthropic": true},
		},
	}
	require.True(t, c.IsBedrockCCCompatEnabled("anthropic"))
	require.False(t, c.IsBedrockCCCompatEnabled("openai"))
}

func TestGroupPolicy_IsBedrockCCCompatEnabled_MissingKey(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			"other_feature": true,
		},
	}
	require.False(t, c.IsBedrockCCCompatEnabled("bedrock"))
}
