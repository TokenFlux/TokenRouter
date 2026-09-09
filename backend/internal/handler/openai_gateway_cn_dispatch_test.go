package handler

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/stretchr/testify/require"
)

func TestOpenAICompatibleRequestPlatformPreservesCNPlatform(t *testing.T) {
	for _, platform := range []string{
		service.PlatformGrok,
		service.PlatformKimi,
		service.PlatformZhipu,
		service.PlatformDeepseek,
	} {
		apiKey := &service.APIKey{Group: &service.Group{Platform: platform}}
		require.Equal(t, platform, openAICompatibleRequestPlatform(apiKey))
	}
	require.Equal(t, service.PlatformOpenAI, openAICompatibleRequestPlatform(nil))
	require.Equal(t, service.PlatformOpenAI, openAICompatibleRequestPlatform(
		&service.APIKey{Group: &service.Group{Platform: service.PlatformAnthropic}},
	))
}

func TestAllowOpenAICompatibleMessagesDispatchUsesProtocolCollectionForCN(t *testing.T) {
	require.True(t, allowOpenAICompatibleMessagesDispatch(nil))
	for _, platform := range []string{service.PlatformKimi, service.PlatformZhipu, service.PlatformDeepseek} {
		disabled := &service.APIKey{Group: &service.Group{Platform: platform}}
		require.False(t, allowOpenAICompatibleMessagesDispatch(disabled), platform)

		enabled := &service.APIKey{Group: &service.Group{
			Platform:         platform,
			AllowedProtocols: []service.GroupClientProtocol{service.GroupClientProtocolAnthropicMessages},
		}}
		require.True(t, allowOpenAICompatibleMessagesDispatch(enabled), platform)
	}
}
