package catalogue_test

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/routing/testkit"

	"github.com/TokenFlux/TokenRouter/internal/gateway/searchtools"

	"github.com/stretchr/testify/require"
)

func TestGroupPolicy_IsWebSearchEmulationEnabled_Enabled(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{"anthropic": true},
		},
	}
	require.True(t, c.IsWebSearchEmulationEnabled("anthropic"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_DifferentPlatform(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{"anthropic": true},
		},
	}
	require.False(t, c.IsWebSearchEmulationEnabled("openai"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_Disabled(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{"anthropic": false},
		},
	}
	require.False(t, c.IsWebSearchEmulationEnabled("anthropic"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_NilFeaturesConfig(t *testing.T) {
	c := &testkit.Configuration{FeaturesConfig: nil}
	require.False(t, c.IsWebSearchEmulationEnabled("anthropic"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_NilGroupPolicy(t *testing.T) {
	var c *testkit.Configuration
	require.False(t, c.IsWebSearchEmulationEnabled("anthropic"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_WrongStructure(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: true, // not a map
		},
	}
	require.False(t, c.IsWebSearchEmulationEnabled("anthropic"))
}

func TestGroupPolicy_IsWebSearchEmulationEnabled_PlatformValueNotBool(t *testing.T) {
	c := &testkit.Configuration{
		FeaturesConfig: map[string]any{
			searchtools.FeatureKey: map[string]any{"anthropic": "yes"},
		},
	}
	require.False(t, c.IsWebSearchEmulationEnabled("anthropic"))
}
