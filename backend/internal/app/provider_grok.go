package app

import (
	"context"
	"slices"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider/rediscache"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// provider_grok.go constructs Grok authorization, token refresh, and quota
// probing from shared application resources. Grok authorization can store
// browser sessions in Redis when that application resource is available. Quota
// probing reuses operator URL policy, execution transport, and local usage
// statistics, while request-side token refresh shares the same coordinator and
// cached credentials.

// provideGrokAuthorization constructs password policy and authorization session
// storage. With Redis supplied it stops the constructor's existing store before
// replacing it with the Redis adapter; absent Redis retains the constructed
// store.
func provideGrokAuthorization(proxies egress.ProxyRepository, client provider.GrokAuthorizationClient, cfg *config.Config, redisClient *redis.Client) *provider.GrokAuthorization {
	options := provideradapter.GrokAuthorizationOptions(proxies, func() bool {
		return cfg != nil && cfg.Provider.Grok.PasswordAuthEnabled
	})
	authorization := provider.NewGrokAuthorization(client, options)
	if redisClient != nil {
		// Stop the constructor-owned session store before installing Redis storage,
		// ending its local resource lifetime before future authorization calls use the replacement adapter.
		authorization.Store.Stop()
		authorization.Store = rediscache.NewGrokSessionStore(redisClient)
	}
	return authorization
}

// provideGrokQuota binds quota requests to shared tokens, transport, proxies,
// and usage statistics. cfg is required and its host slice is cloned; endpoint
// mode remains a request-time reader from shared gateway settings.
func provideGrokQuota(store *providerpostgres.ProviderStore, proxies egress.ProxyRepository, token *provider.GrokTokenSource, transport httpclient.UpstreamTransport, cfg *config.Config, usageStore *usagepostgres.Store, settings *gateway.RuntimeSettings, system *provideradapter.SystemUsageTransport) *provider.GrokQuotaService {
	if system != nil {
		transport = system
	}

	policy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	requests := &provideradapter.GrokQuotaTransport{Do: transport.Do, Proxy: proxies.GetByID, DefaultBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings), OperatorValidator: policy.Validate, MapStatus: forward.MapStatus}

	return provideradapter.NewGrokQuota(store, token, requests, newProviderLocalUsageStats(usageStore))
}

// provideGrokTokens constructs cached Grok token lookup with the platform
// refresh policy. Its callback invokes the shared OAuth coordinator using the
// Grok refresher so gateway and manual queries share credential refresh
// handling.
func provideGrokTokens(store *providerpostgres.ProviderStore, cache provider.AccessTokenCache, authorization *provider.GrokAuthorization, refresh *provider.OAuthRefreshAPI) *provider.GrokTokenSource {
	executor := provider.NewGrokTokenRefresher(authorization)
	return &provider.GrokTokenSource{
		Cache:      cache,
		Repository: store,
		Policy:     provider.GrokProviderRefreshPolicy(),
		Refresh: func(ctx context.Context, record *provider.Record, window time.Duration) (*provider.OAuthRefreshResult, error) {
			return refresh.RefreshIfNeeded(ctx, record, executor, window)
		},
	}
}
