package httpapi

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/protocol"
)

func openAIForwardProfile(account *gatewayprovider.ExecutionAccount) forward.Profile {
	return forward.Profile{Platform: account.Record.Platform, Name: account.Record.Name, Type: account.Record.Type, UsesCodex: account.View().UsesOpenAICodexProtocol(), OpenAI: account.View().IsOpenAI(), OAuth: account.View().IsOAuth(), OAuthLike: account.View().IsOpenAIOAuthLike(), APIKey: account.Record.Type == capability.AccountTypeAPIKey, Grok: account.Record.Platform == capability.PlatformGrok, DeepSeek: account.Record.Platform == capability.PlatformDeepseek, NativeCN: gatewayprovider.ExecutionProtocolTarget(account).UsesNativeCNResponses(), Anthropic: gatewayprovider.ExecutionProtocolTarget(account).IsAnthropicProtocol(), RawChat: gatewayprovider.ExecutionModelPolicy(account).RawChat(), ResolvedChat: account.Route.Protocol() == protocol.ProtocolOpenAIChatCompletions, Passthrough: account.View().IsOpenAIPassthroughEnabled()}
}
