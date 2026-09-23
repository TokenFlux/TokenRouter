// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	anthropic "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	service "github.com/TokenFlux/TokenRouter/internal/service"

	time "time"
)

func provideGatewayForRouting(
	accountRepo gatewayprovider.ExecutionAccountStore,

	cache session.GatewayCache,
	cfg *config.Config,

	healthObserver *accountprovider.UpstreamHealth,
	identityService *anthropic.RequestFingerprint,
	httpUpstream httpclient.UpstreamTransport, deferredService *account.DeferredService,
	messageCredentials *account.MessageCredentialSource,

	digestStore *session.DigestSessionStore,
	settingService *gatewayprovider.RuntimeReaders,
	tlsFPProfileService *provider.TLSProfiles,
	channelService *routing.ChannelService,
	resolver *billing.PriceResolver,

	headerFilter *egress.CompiledHeaderFilter, recorders GatewayCompletionRecorders,
) *service.GatewayService {
	gateway := service.NewGatewayService(accountRepo, cache, cfg, healthObserver, identityService, httpUpstream, deferredService, messageCredentials, digestStore, settingService, tlsFPProfileService, channelService, resolver, headerFilter)
	gateway.BindCompletionRecorder(recorders.Forward)
	return gateway
}

// provideRoutingModelList 由 app 投影原 15 秒默认 TTL；缓存无构造启动副作用。
func provideRoutingModelList(repo *accountpostgres.AccountStore, cfg *config.Config) *routing.ModelList {
	return routing.NewModelList(catalogueReader(repo), resolveModelsListCacheTTL(cfg))
}

// resolveModelsListCacheTTL 保留默认十五秒及显式正数配置，投影仅属于组合根。
func resolveModelsListCacheTTL(cfg *config.Config) time.Duration {
	if cfg == nil || cfg.Gateway.ModelsListCacheTTLSeconds <= 0 {
		return 15 * time.Second
	}
	return time.Duration(cfg.Gateway.ModelsListCacheTTLSeconds) * time.Second
}
