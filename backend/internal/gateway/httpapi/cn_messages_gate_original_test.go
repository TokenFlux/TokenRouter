package httpapi

import (
	"testing"

	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"

	"github.com/TokenFlux/TokenRouter/internal/protocol"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"github.com/stretchr/testify/require"
)

func TestAllowOpenAICompatibleMessagesDispatchUsesProtocolCollectionForCN(t *testing.T) {
	require.True(t, (openAITextHTTPBackend{}).AllowsMessages(nil))
	for _, platform := range []string{capability.PlatformKimi, capability.PlatformZhipu, capability.PlatformDeepseek} {
		disabled := &apikey.APIKey{Group: &routing.Group{Platform: platform}}
		require.False(t, (openAITextHTTPBackend{}).AllowsMessages(disabled), platform)

		enabled := &apikey.APIKey{Group: &routing.Group{
			Platform:         platform,
			AllowedProtocols: []protocol.ProtocolID{protocol.ProtocolAnthropicMessages},
		}}
		require.True(t, (openAITextHTTPBackend{}).AllowsMessages(enabled), platform)
	}
}
