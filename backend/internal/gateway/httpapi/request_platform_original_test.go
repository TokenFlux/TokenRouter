package httpapi

import (
	"testing"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestOpenAICompatibleRequestPlatformPreservesCNPlatform(t *testing.T) {
	for _, platform := range []string{
		capability.PlatformGrok,
		capability.PlatformKimi,
		capability.PlatformZhipu,
		capability.PlatformDeepseek,
	} {
		apiKey := &apikey.APIKey{Group: &routing.Group{Platform: platform}}
		require.Equal(t, platform, OpenAICompatibleRequestPlatform(apiKey))
	}
	require.Equal(t, capability.PlatformOpenAI, OpenAICompatibleRequestPlatform(nil))
	require.Equal(t, capability.PlatformOpenAI, OpenAICompatibleRequestPlatform(
		&apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformAnthropic}},
	))
}
