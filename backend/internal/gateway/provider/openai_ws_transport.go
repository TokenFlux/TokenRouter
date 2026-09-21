package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// ResolveOpenAIWSTransport 投影当前账号资格，传输优先级由 egress 唯一裁决。
func ResolveOpenAIWSTransport(value *account.Record, options *egress.OpenAIWSOptions, defaultMode string) egress.OpenAIWSProtocolDecision {
	input := egress.OpenAIWSAccount{}
	if value != nil {
		input = egress.OpenAIWSAccount{
			Present:     true,
			OpenAI:      value.IsOpenAI(),
			ForceHTTP:   value.IsOpenAIWSForceHTTPEnabled(),
			OAuthLike:   value.IsOpenAIOAuthLike(),
			APIKey:      value.IsOpenAIApiKey(),
			WSEnabled:   value.IsOpenAIResponsesWebSocketV2Enabled(),
			Concurrency: value.Concurrency,
		}
		if options != nil && options.ModeRouterV2Enabled {
			input.Mode = value.ResolveOpenAIResponsesWebSocketV2Mode(defaultMode)
		}
	}
	return egress.ResolveOpenAIWSTransport(input, options)
}
