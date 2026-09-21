// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	anthropic "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/session"
	usage "github.com/TokenFlux/TokenRouter/internal/usage"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"

	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"

	config "github.com/TokenFlux/TokenRouter/internal/config"

	routing "github.com/TokenFlux/TokenRouter/internal/routing"

	service "github.com/TokenFlux/TokenRouter/internal/service"

	time "time"
)

func provideGatewayForRouting(nativeUsageStore *usagepostgres.Store,
	models *routing.ModelList,
	catalogue *routing.RequestableCatalogue,
	accountRepo service.AccountRepository,
	groupRepo routing.GroupRepository,
	usageLogRepo usage.UsageLogRepository,
	usageBillingRepo completion.Store,
	userRepo identity.UserRepository,
	userSubRepo billing.UserSubscriptionRepository,
	userGroupRateRepo billing.UserGroupRateRepository,
	cache session.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *service.SchedulerSnapshotService,
	concurrencyService *scheduler.ConcurrencyService,
	billingService *billing.Calculator,
	rateLimitService *service.RateLimitService,
	billingCacheService *billing.Eligibility,
	identityService *anthropic.RequestFingerprint,
	httpUpstream httpclient.UpstreamTransport, deferredService *account.DeferredService,
	claudeTokenProvider *account.ClaudeTokenSource,
	sessionLimitCache scheduler.SessionLimitCache,
	windowCostCache billing.WindowCostCache,
	rpmCache scheduler.RPMCache,
	digestStore *session.DigestSessionStore,
	settingService *gatewayprovider.RuntimeReaders,
	tlsFPProfileService *provider.TLSProfiles,
	channelService *routing.ChannelService,
	resolver *billing.PriceResolver,
	balanceNotifyService *billing.BalanceNotifyService,
	userPlatformQuotaRepo billing.UserPlatformQuotaRepository,
) *service.GatewayService {
	gateway := service.NewGatewayService(accountRepo, groupRepo, usageLogRepo, usageBillingRepo, userRepo, userSubRepo, userGroupRateRepo, cache, cfg, schedulerSnapshot, concurrencyService, billingService, rateLimitService, billingCacheService, identityService, httpUpstream, deferredService, claudeTokenProvider, sessionLimitCache, windowCostCache, rpmCache, digestStore, settingService, tlsFPProfileService, channelService, resolver, balanceNotifyService, userPlatformQuotaRepo, models)
	gateway.BindUsageWindowSource(usageWindowStats{nativeUsageStore})
	gateway.BindModelCatalogue(catalogue)
	return gateway
}

// provideRoutingModelList 由 app 投影原 15 秒默认 TTL；缓存无构造启动副作用。
func provideRoutingModelList(repo *accountpostgres.AccountStore, cfg *config.Config) *routing.ModelList {
	ttl := 15 * time.Second
	if cfg != nil && cfg.Gateway.ModelsListCacheTTLSeconds > 0 {
		ttl = time.Duration(cfg.Gateway.ModelsListCacheTTLSeconds) * time.Second
	}
	return routing.NewModelList(catalogueReader(repo), ttl)
}
