// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// identityAuthGraph 固定身份核心和事务适配，所有新旧入口共享同一对象。
type identityAuthGraph struct {
	Core   *identity.AuthService
	State  *identitypostgres.AuthState
	Client *dbent.Client
}

func provideLegacyAuth(g *identityAuthGraph) *service.AuthService {
	return service.WrapIdentityAuth(g.Client, g.Core, g.State)
}
func provideIdentityAuthGraph(
	entClient *dbent.Client,
	users *identitypostgres.UserStore,
	redeemRepo service.RedeemCodeRepository,
	refreshTokenCache service.RefreshTokenCache,
	cfg *config.Config,
	settingService *service.SettingService,
	emailService *service.EmailService,
	turnstileService *service.TurnstileService,
	tencentCaptchaService *service.TencentCaptchaService,
	aliyunCaptchaService *service.AliyunCaptchaService,
	emailQueueService *service.EmailQueueService,
	promoService *service.PromoService,
	defaultSubAssigner service.DefaultSubscriptionAssigner,
	affiliateService *service.AffiliateService,
	userPlatformQuotaRepo service.UserPlatformQuotaRepository,
	authCacheInvalidator service.APIKeyAuthCacheInvalidator,
	billingCache service.BillingCache,
) *identityAuthGraph {
	options := &identity.AuthOptions{}
	if cfg != nil {
		options.Default.UserBalance = cfg.Default.UserBalance
		options.Default.UserConcurrency = cfg.Default.UserConcurrency
		options.JWT = identity.SessionOptions{Now: time.Now, Secret: cfg.JWT.Secret, ExpireHour: cfg.JWT.ExpireHour, AccessTokenExpireMinutes: cfg.JWT.AccessTokenExpireMinutes, RefreshTokenExpireDays: cfg.JWT.RefreshTokenExpireDays}
		options.Server.Mode = cfg.Server.Mode
		options.Turnstile.Required = cfg.Turnstile.Required
	}
	deps := &identity.AuthDependencies{Users: users, Redeem: redeemRepo, RefreshTokens: refreshTokenCache, Options: options, Turnstile: turnstileService, Tencent: tencentCaptchaService, Aliyun: aliyunCaptchaService, DefaultSubscriptions: defaultSubAssigner, Invalidator: authCacheInvalidator, BalanceCache: billingCache, Quotas: userPlatformQuotaRepo, Observer: identity.Observer{Log: logging.LegacyPrintf}, DomainRegistration: users, NormalizedEmailConflict: users, EmailAliasGuard: users, AliasLookup: users, AliasOwner: users}
	if settingService != nil {
		deps.Settings = legacybridge.IdentityAuthSettings{SettingService: settingService}
	}
	if emailService != nil {
		deps.Email = emailService
	}
	if emailQueueService != nil {
		deps.EmailQueue = emailQueueService
	}
	if promoService != nil {
		deps.Promo = promoService
	}
	if affiliateService != nil {
		deps.Affiliate = identityPromotion{Service: affiliateService}
	}
	state := identitypostgres.NewAuthState(entClient, deps)
	core := identity.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return &identityAuthGraph{Core: core, State: state, Client: entClient}
}

// provideIdentityProfiles 使用 app 的任务拥有者，保持关闭前可等待的后台操作。
func provideIdentityProfiles(users *identitypostgres.UserStore, settings service.SettingRepository, keys service.APIKeyAuthCacheInvalidator, cache service.BillingCache, tasks *lifecycle.Tasks) *identity.UserService {
	return identity.NewUserService(users, settings, keys, cache, func(name string, fn func()) bool { return tasks.Go(name, fn) }, time.Now)
}
func provideLegacyProfiles(users *identity.UserService) *service.UserService {
	return &service.UserService{UserService: users}
}
