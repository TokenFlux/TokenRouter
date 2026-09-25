package httpapi

import (
	"testing"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/stretchr/testify/require"
)

func TestResolveOpenAIWSDecisionByClientTransport(t *testing.T) {
	base := egress.OpenAIWSProtocolDecision{
		Transport: egress.OpenAIUpstreamTransportResponsesWebsocketV2,
		Reason:    "ws_v2_enabled",
	}

	httpDecision := ResolveOpenAIWSDecisionByClientTransport(base, OpenAIClientTransportHTTP)
	require.Equal(t, egress.OpenAIUpstreamTransportHTTPSSE, httpDecision.Transport)
	require.Equal(t, "client_protocol_http", httpDecision.Reason)

	wsDecision := ResolveOpenAIWSDecisionByClientTransport(base, OpenAIClientTransportWS)
	require.Equal(t, base, wsDecision)

	unknownDecision := ResolveOpenAIWSDecisionByClientTransport(base, OpenAIClientTransportUnknown)
	require.Equal(t, base, unknownDecision)
}
