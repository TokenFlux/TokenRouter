package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// provideGrokExecutor 复用凭据、HTTP 池、健康状态和应用活动屏障。
func provideGrokExecutor(cfg *config.Config, credentials *provider.RequestCredentials, transport httpclient.UpstreamTransport, output *gatewayhttp.OpenAIResponseOutput, health *accountprovider.GrokHealth, tls *egressprovider.TLSProfiles, readers *provider.RuntimeReaders, blocks *account.RuntimeBlockState, deferred *account.DeferredService, store provider.ExecutionAccountStore, activity *gatewayRequestActivity, prices *billing.PriceResolver, connections *gatewayhttp.OpenAIWSConnections) *gatewayhttp.GrokExecutor {
	routes := provideGrokRoutes(cfg, readers)
	return &gatewayhttp.GrokExecutor{FastPolicy: &provider.ExecutionFastPolicy{Readers: readers, Prices: prices}, Credentials: credentials, Transport: transport, Output: output, Health: health, Routes: routes, TLS: tls, Dialer: connections.Dialer(), Enter: activity.Enter, Failure: &gatewayhttp.UpstreamTransportFailure{Health: &accountprovider.TransportHealth{Runtime: blocks, Deferred: deferred, Store: store}}}
}

// provideGrokRoutes 只投影静态目标策略，动态默认模式仍在原查询位置读取。
func provideGrokRoutes(cfg *config.Config, readers *provider.RuntimeReaders) provider.GrokRoutes {
	routes := provider.GrokRoutes{Validate: grok.ValidateBaseURL}
	if cfg != nil {
		routes.Validate = (egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: cfg.Security.URLAllowlist.UpstreamHosts}).Validate
	}
	if readers != nil {
		routes.DefaultMode = readers.Gateway.GetGrokDefaultBaseURLMode
	}
	return routes
}
