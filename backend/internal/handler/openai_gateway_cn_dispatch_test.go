package handler

import (
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
)

import (
	"testing"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

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
		require.Equal(t, platform, openAICompatibleRequestPlatform(apiKey))
	}
	require.Equal(t, capability.PlatformOpenAI, openAICompatibleRequestPlatform(nil))
	require.Equal(t, capability.PlatformOpenAI, openAICompatibleRequestPlatform(
		&apikey.APIKey{Group: &routing.Group{Platform: capability.PlatformAnthropic}},
	))
}

func TestAllowOpenAICompatibleMessagesDispatchUsesProtocolCollectionForCN(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil))
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		disabled := &apikey.APIKey{Group: &routing.Group{Platform: platform}}
		require.False(t, allowOpenAICompatibleMessagesDispatch(disabled), platform)

		enabled := &apikey.APIKey{Group: &routing.Group{
			Platform:         platform,
			AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages},
		}}
		require.True(t, allowOpenAICompatibleMessagesDispatch(enabled), platform)
	}
}
