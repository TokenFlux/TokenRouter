//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewaytransport "github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini/codeassist"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
	"github.com/google/wire"
)

// upstreamClientProviders 绑定平台客户端与唯一传输适配器。
var upstreamClientProviders = wire.NewSet(
	anthropic.NewRequestFingerprint,
	providePricingRemoteClient,
	provideClaudeUsageFetcher,
	provideOpenAIOAuthClient,
	provideGeminiOAuthClient,
	anthropic.NewOAuthClient,
	wire.Bind(new(account.ClaudeOAuthClient), new(*anthropic.OAuthClient)),
	grok.NewOAuthClient,
	wire.Bind(new(account.GrokAuthorizationClient), new(*grok.OAuthClient)),
	codeassist.NewClient,
	wire.Bind(new(account.GeminiCodeAssistClient), new(*codeassist.Client)),
	codeassist.NewDriveClient,
	provideHTTPUpstream,
	wire.Bind(new(accountprovider.QoderTransport), new(*gatewaytransport.Client)),
)
