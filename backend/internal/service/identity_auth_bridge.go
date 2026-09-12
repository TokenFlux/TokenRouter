// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// IdentityRepository 在实际仓储已经迁移时直接交付原对象，避免热路径反复投影。
func IdentityRepository(users UserRepository) identity.UserRepository {
	if direct, ok := users.(interface {
		IdentityRepository() identity.UserRepository
	}); ok {
		return direct.IdentityRepository()
	}
	if users == nil {
		return nil
	}
	return legacyIdentityUsers{Repository: users}
}

// legacyAuthSettings 只转换钉钉启动策略形状，其余动态值按调用时读取。
type legacyAuthSettings struct{ *SettingService }

func (s legacyAuthSettings) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	cfg, err := s.SettingService.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: cfg.Enabled, BypassRegistration: cfg.BypassRegistration, CorpRestrictionPolicy: cfg.CorpRestrictionPolicy}, err
}

type legacyAuthAffiliate struct{ Service *AffiliateService }

func (a legacyAuthAffiliate) EnsureUserAffiliate(ctx context.Context, id int64) error {
	_, err := a.Service.EnsureUserAffiliate(ctx, id)
	return err
}
func (a legacyAuthAffiliate) BindInviterByCode(ctx context.Context, id int64, code string) error {
	return a.Service.BindInviterByCode(ctx, id, code)
}

type legacyRegistrationDomains struct {
	Source RegistrationEmailDomainRepository
}

func (d legacyRegistrationDomains) CountUsersByEmailDomain(ctx context.Context, domain string) (int, error) {
	return d.Source.CountUsersByEmailDomain(ctx, domain)
}
func (d legacyRegistrationDomains) CreateWithRegistrationEmailGuards(ctx context.Context, u *identity.User, email, domain string) error {
	legacy := UserFromIdentity(u)
	err := d.Source.CreateWithRegistrationEmailGuards(ctx, legacy, email, domain)
	if u != nil && legacy != nil {
		*u = *IdentityUser(legacy)
	}
	return err
}

// BuildIdentityAuth 为兼容构造器投影依赖；不执行规则、启动任务或建立连接。
func (s *AuthService) BuildIdentityAuth() (*identity.AuthService, *identitypostgres.AuthState) {
	deps := &identity.AuthDependencies{}
	var client *dbent.Client
	if s != nil {
		client = s.entClient
		deps.Users = IdentityRepository(s.userRepo)
		deps.Redeem = s.redeemRepo
		deps.RefreshTokens = s.refreshTokenCache
		deps.Turnstile = s.turnstileService
		deps.Tencent = s.tencentCaptchaService
		deps.Aliyun = s.aliyunCaptchaService
		deps.DefaultSubscriptions = s.defaultSubAssigner
		deps.Invalidator = s.authCacheInvalidator
		deps.BalanceCache = s.billingCache
		deps.Quotas = s.userPlatformQuotaRepo
		deps.Observer = identity.Observer{Log: logger.LegacyPrintf}
		if s.cfg != nil {
			o := &identity.AuthOptions{}
			o.Default.UserBalance = s.cfg.Default.UserBalance
			o.Default.UserConcurrency = s.cfg.Default.UserConcurrency
			o.JWT = identity.SessionOptions{Secret: s.cfg.JWT.Secret, ExpireHour: s.cfg.JWT.ExpireHour, AccessTokenExpireMinutes: s.cfg.JWT.AccessTokenExpireMinutes, RefreshTokenExpireDays: s.cfg.JWT.RefreshTokenExpireDays}
			o.Server.Mode = s.cfg.Server.Mode
			o.Turnstile.Required = s.cfg.Turnstile.Required
			deps.Options = o
		}
		if s.settingService != nil {
			deps.Settings = legacyAuthSettings{s.settingService}
		}
		if s.emailService != nil {
			deps.Email = s.emailService
		}
		if s.emailQueueService != nil {
			deps.EmailQueue = s.emailQueueService
		}
		if s.promoService != nil {
			deps.Promo = s.promoService
		}
		if s.affiliateService != nil {
			deps.Affiliate = legacyAuthAffiliate{s.affiliateService}
		}
		if p, ok := deps.Users.(identity.RegistrationEmailDomainRepository); ok {
			deps.DomainRegistration = p
		} else if p, ok := s.userRepo.(RegistrationEmailDomainRepository); ok {
			deps.DomainRegistration = legacyRegistrationDomains{p}
		}
		if p, ok := s.userRepo.(identity.AuthNormalizedEmailBindingConflictChecker); ok {
			deps.NormalizedEmailConflict = p
		}
		if p, ok := s.userRepo.(identity.AuthEmailIdentityAliasGuardRepository); ok {
			deps.EmailAliasGuard = p
		}
		if p, ok := s.userRepo.(identity.EmailAliasLookupRepository); ok {
			deps.AliasLookup = p
		}
		if p, ok := s.userRepo.(identity.EmailAliasOwnerLookupRepository); ok {
			deps.AliasOwner = p
		}
	}
	state := identitypostgres.NewAuthState(client, deps)
	core := identity.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return core, state
}
func (s *AuthService) identityCore() *identity.AuthService {
	if s != nil && s.core != nil {
		return s.core
	}
	core, _ := s.BuildIdentityAuth()
	return core
}

// PinIdentityCore 仅在 app 装配时调用，让新旧生产入口共享同一实例。
func (s *AuthService) PinIdentityCore() *identity.AuthService {
	if s.core == nil {
		s.core, s.state = s.BuildIdentityAuth()
	}
	return s.core
}

// IdentityCore 返回已有身份用例，兼容 HTTP 构造器不复制规则。
func (s *AuthService) IdentityCore() *identity.AuthService {
	if s == nil {
		return nil
	}
	return s.identityCore()
}
