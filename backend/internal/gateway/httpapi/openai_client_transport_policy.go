package httpapi

import (
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
)

func ResolveOpenAIWSDecisionByClientTransport(
	decision egress.OpenAIWSProtocolDecision,
	clientTransport OpenAIClientTransport,
) egress.OpenAIWSProtocolDecision {
	if clientTransport == OpenAIClientTransportHTTP {
		return egress.OpenAIWSHTTPDecision("client_protocol_http")
	}
	return decision
}
