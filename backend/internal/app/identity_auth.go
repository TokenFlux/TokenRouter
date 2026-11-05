package app

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/site"
)

// identity_auth.go constructs authentication rules and persistence with shared
// identity dependencies. AuthState receives the constructed AuthService as its
// Rules callback target, while settings adapters obtain promotion flags and
// DingTalk registration policy from their owning readers.

// identityAuthSettings combines the owning readers used by authentication
// rules.
type identityAuthSettings struct {
	// RuntimeSettings, GrantSettings, DisplaySettings, oauth, and promotion
	// retain application readers. Authentication delegates each setting to its owning reader using the operation's context.
	*identity.RuntimeSettings                            // identity runtime flags
	*identity.GrantSettings                              // signup entitlement settings
	*site.DisplaySettings                                // public site display
	oauth                     *identity.OAuthSettings    // OAuth provider settings
	promotion                 *promotion.RuntimeSettings // promotion runtime flags
}

// identityAuthGraph retains authentication rules and storage used by HTTP
// assembly.
type identityAuthGraph struct {
	// Core and State share AuthDependencies, and State.Rules is assigned Core
	// after construction. Client is the Ent owner reused when HTTP assembles pending-state persistence.
	Core   *identity.AuthService       // authentication rule service
	State  *identitypostgres.AuthState // authentication persistence state
	Client *dbent.Client               // shared Ent client
}

// provideIdentityAuthSettings groups shared identity, display, OAuth, and
// promotion readers.
func provideIdentityAuthSettings(runtime *identity.RuntimeSettings, grants *identity.GrantSettings, display *site.DisplaySettings, oauth *identity.OAuthSettings, promotion *promotion.RuntimeSettings) *identityAuthSettings {
	return &identityAuthSettings{RuntimeSettings: runtime, GrantSettings: grants, DisplaySettings: display, oauth: oauth, promotion: promotion}
}

// IsPromoCodeEnabled reads the promotion flag from shared runtime settings.
func (s identityAuthSettings) IsPromoCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsPromoCodeEnabled(ctx)
}

// IsInvitationCodeEnabled reads invitation acceptance from shared promotion
// runtime settings.
func (s identityAuthSettings) IsInvitationCodeEnabled(ctx context.Context) bool {
	return s.promotion.IsInvitationCodeEnabled(ctx)
}

// IsAffiliateAdminRechargeEnabled reads the administrator recharge rebate flag
// from promotion settings.
func (s identityAuthSettings) IsAffiliateAdminRechargeEnabled(ctx context.Context) bool {
	return s.promotion.IsAffiliateAdminRechargeEnabled(ctx)
}

// GetDingTalkConnectOAuthConfig returns DingTalk registration policy from the
// OAuth reader. It carries enabled, bypass, and corporate-restriction values
// with the original read error for authentication to interpret.
func (s identityAuthSettings) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	value, err := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{Enabled: value.Enabled, BypassRegistration: value.BypassRegistration, CorpRestrictionPolicy: value.CorpRestrictionPolicy}, err
}

// provideIdentityAuthGraph constructs authentication rules and Ent persistence
// with shared dependencies. Optional settings, email, queued delivery,
// promotions, and affiliate capabilities are attached when supplied; cfg
// captures signup and session options, and the resulting core is assigned to
// state.Rules.
func provideIdentityAuthGraph(
	entClient *dbent.Client,
	users *identitypostgres.UserStore,
	redeemRepo billing.RedeemCodeRepository,
	refreshTokenCache identity.RefreshTokenCache,
	cfg *config.Config,
	settingService *identityAuthSettings,
	emailService *identity.EmailChallenges,
	turnstileService *identity.TurnstileService,
	tencentCaptchaService *identity.TencentCaptchaService,
	aliyunCaptchaService *identity.AliyunCaptchaService,
	emailQueueService *notification.EmailQueueService,
	promoService *promotion.PromoService,
	defaultSubAssigner identity.DefaultSubscriptionAssigner,
	affiliateService *promotion.AffiliateService,
	authCacheInvalidator apikey.APIKeyAuthCacheInvalidator,
	billingCache billing.BillingCache,
) *identityAuthGraph {
	options := &identity.AuthOptions{}
	if cfg != nil {
		options.Default.UserBalance = cfg.UserDefaults.Balance
		options.Default.UserConcurrency = cfg.UserDefaults.Concurrency
		options.JWT = identity.SessionOptions{Now: time.Now, Secret: cfg.Auth.JWT.Secret, AccessTokenExpireMinutes: cfg.Auth.JWT.AccessTokenTTLMinutes, RefreshTokenExpireDays: cfg.Auth.JWT.RefreshTokenTTLDays}
		options.Server.Mode = cfg.Server.Mode
		options.Turnstile.Required = cfg.Auth.RequireCaptcha
	}
	deps := &identity.AuthDependencies{Users: users, Redeem: redeemRepo, RefreshTokens: refreshTokenCache, Options: options, Turnstile: turnstileService, Tencent: tencentCaptchaService, Aliyun: aliyunCaptchaService, DefaultSubscriptions: defaultSubAssigner, Invalidator: authCacheInvalidator, BalanceCache: billingCache, Observer: identity.Observer{Log: logging.LegacyPrintf}, DomainRegistration: users, NormalizedEmailConflict: users, EmailAliasGuard: users, AliasLookup: users, AliasOwner: users}
	if settingService != nil {
		deps.Settings = settingService
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

// provideIdentityProfiles constructs profile operations with shared
// invalidation and balance caches. Its background callback uses the application
// task tracker, allowing shutdown to wait for admitted profile work before
// closing the stores it uses.
func provideIdentityProfiles(users *identitypostgres.UserStore, settings settingscore.Repository, keys apikey.APIKeyAuthCacheInvalidator, cache billing.BillingCache, tasks *lifecycle.Tasks) *identity.UserService {
	return identity.NewUserService(users, settings, keys, cache, func(name string, fn func()) bool { return tasks.Go(name, fn) }, time.Now)
}
