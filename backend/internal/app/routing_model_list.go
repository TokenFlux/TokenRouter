// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

func provideGatewayForRouting(
	models *routing.ModelList,
	accountRepo service.AccountRepository,
	groupRepo service.GroupRepository,
	usageLogRepo service.UsageLogRepository,
	usageBillingRepo service.UsageBillingRepository,
	userRepo service.UserRepository,
	userSubRepo service.UserSubscriptionRepository,
	userGroupRateRepo service.UserGroupRateRepository,
	cache service.GatewayCache,
	cfg *config.Config,
	schedulerSnapshot *service.SchedulerSnapshotService,
	concurrencyService *service.ConcurrencyService,
	billingService *service.BillingService,
	rateLimitService *service.RateLimitService,
	billingCacheService *service.BillingCacheService,
	identityService *service.IdentityService,
	httpUpstream service.HTTPUpstream,
	deferredService *service.DeferredService,
	claudeTokenProvider *service.ClaudeTokenProvider,
	sessionLimitCache service.SessionLimitCache,
	rpmCache service.RPMCache,
	digestStore *service.DigestSessionStore,
	settingService *service.SettingService,
	tlsFPProfileService *service.TLSFingerprintProfileService,
	channelService *service.ChannelService,
	resolver *service.ModelPricingResolver,
	balanceNotifyService *service.BalanceNotifyService,
	userPlatformQuotaRepo service.UserPlatformQuotaRepository,
) *service.GatewayService {
	return service.NewGatewayService(accountRepo, groupRepo, usageLogRepo, usageBillingRepo, userRepo, userSubRepo, userGroupRateRepo, cache, cfg, schedulerSnapshot, concurrencyService, billingService, rateLimitService, billingCacheService, identityService, httpUpstream, deferredService, claudeTokenProvider, sessionLimitCache, rpmCache, digestStore, settingService, tlsFPProfileService, channelService, resolver, balanceNotifyService, userPlatformQuotaRepo, models)
}

// provideRoutingModelList 由 app 投影原 15 秒默认 TTL；缓存无构造启动副作用。
func provideRoutingModelList(repo service.AccountRepository, cfg *config.Config) *routing.ModelList {
	ttl := 15 * time.Second
	if cfg != nil && cfg.Gateway.ModelsListCacheTTLSeconds > 0 {
		ttl = time.Duration(cfg.Gateway.ModelsListCacheTTLSeconds) * time.Second
	}
	return routing.NewModelList(legacybridge.ModelListReader(repo), ttl)
}
