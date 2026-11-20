//go:build wireinject

package app

import (
	"github.com/google/wire"

	gatewaytransport "github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// upstream_clients_wire.go binds provider authorization and upstream transport
// interfaces to platform clients in Wire.
// Platform adapters depend on provider interfaces for Claude OAuth, Grok
// authorization, and Gemini CodeAssist requests. The set binds those interfaces
// to platform clients and binds QoderTransport to the gateway transport client,
// alongside request fingerprinting and Claude usage fetching.

// upstreamClientProviders supplies platform clients and binds authorization
// and Qoder transport interfaces to their selected implementations.
var upstreamClientProviders = wire.NewSet(
	anthropic.NewRequestFingerprint,
	provideClaudeUsageFetcher,
	provideOpenAIOAuthClient,
	provideGeminiOAuthClient,
	anthropic.NewOAuthClient,
	wire.Bind(new(provider.ClaudeOAuthClient), new(*anthropic.OAuthClient)),
	grok.NewOAuthClient,
	wire.Bind(new(provider.GrokAuthorizationClient), new(*grok.OAuthClient)),
	codeassist.NewClient,
	wire.Bind(new(provider.GeminiCodeAssistClient), new(*codeassist.Client)),
	provideHTTPUpstream,
	wire.Bind(new(provideradapter.QoderTransport), new(*gatewaytransport.Client)),
)
