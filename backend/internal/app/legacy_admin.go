// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	dbent "github.com/TokenFlux/TokenRouter/ent"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

func provideLegacyAdmin(
	userRepo service.UserRepository,
	groupRepo service.AdminGroupRepository,
	accountRepo service.AdminAccountRepository,
	proxyRepo service.ProxyRepository,
	apiKeyRepo service.APIKeyRepository,
	redeemCodeRepo service.RedeemCodeRepository,
	userGroupRateRepo service.UserGroupRateRepository,
	userRPMCache service.UserRPMCache,
	billingCacheService *service.BillingCacheService,
	proxyProber service.ProxyExitInfoProber,
	proxyLatencyCache service.ProxyLatencyCache,
	authCacheInvalidator service.APIKeyAuthCacheInvalidator,
	entClient *dbent.Client,
	settingService *service.SettingService,
	defaultSubAssigner service.DefaultSubscriptionAssigner,
	userSubRepo service.UserSubscriptionRepository,
	privacyClientFactory service.PrivacyClientFactory,
	runtimeBlocker service.AccountRuntimeBlocker,
	httpUpstream service.HTTPUpstream,
	tlsFPProfileService *service.TLSFingerprintProfileService,
	affiliateService *service.AffiliateService,
	channelCacheInvalidator service.ChannelCacheInvalidator,
	billingRedeem *billing.RedeemAdmin,
	billingBalance billing.BalanceAdjuster,
	accounts *account.Admin, rates *billing.GroupRateAdmin, groups *routing.GroupAdmin, users *identity.UserAdmin, keys *apikey.Admin, proxies *egress.ProxyAdmin,
) service.AdminService {
	return service.NewAdminService(userRepo, groupRepo, accountRepo, proxyRepo, apiKeyRepo, redeemCodeRepo, userGroupRateRepo, userRPMCache, billingCacheService, proxyProber, proxyLatencyCache, authCacheInvalidator, entClient, settingService, defaultSubAssigner, userSubRepo, privacyClientFactory, runtimeBlocker, httpUpstream, tlsFPProfileService, affiliateService, channelCacheInvalidator, billingRedeem, billingBalance, service.Administration{Accounts: accounts, Rates: rates, Groups: groups, Users: users, Keys: keys, Proxies: proxies})
}
