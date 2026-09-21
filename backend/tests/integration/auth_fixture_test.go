//go:build unit

package integration

import (
	"context"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

type promotionAuthSettings = promotion.RuntimeSettings

// authSettingsFixture 组合真实设置读取器；站点名是此认证集合固定的外部展示投影。
type authSettingsFixture struct {
	*identity.RuntimeSettings
	*identity.GrantSettings
	*promotionAuthSettings
	oauth *identity.OAuthSettings
}

func (s authSettingsFixture) GetSiteName(context.Context) string { return "Sub2API" }

func (s authSettingsFixture) GetDingTalkConnectOAuthConfig(ctx context.Context) (identity.DingTalkRegistrationPolicy, error) {
	value, err := s.oauth.GetDingTalkConnectOAuthConfig(ctx)
	return identity.DingTalkRegistrationPolicy{
		Enabled: value.Enabled, BypassRegistration: value.BypassRegistration,
		CorpRestrictionPolicy: value.CorpRestrictionPolicy,
	}, err
}

// newIdentityAuthForTest 直接装配原生规则及存储，沿用调用者的连接与设置实例。
func newIdentityAuthForTest(
	client *dbent.Client,
	users identity.UserRepository,
	options *identity.AuthOptions,
	store settings.Repository,
	email identity.EmailCache,
	refresh identity.RefreshTokenCache,
	subscriptions identity.DefaultSubscriptionAssigner,
) *identity.AuthService {
	deps := &identity.AuthDependencies{
		Users: users, Options: options, RefreshTokens: refresh,
		DefaultSubscriptions: subscriptions,
	}
	if store != nil {
		deps.Settings = authSettingsFixture{
			RuntimeSettings: identity.NewRuntimeSettings(store, settings.ErrSettingNotFound),
			GrantSettings: identity.NewGrantSettings(store, identity.GrantSettingsOptions{
				DefaultBalance: options.Default.UserBalance, DefaultConcurrency: options.Default.UserConcurrency,
			}),
			promotionAuthSettings: promotion.NewRuntimeSettings(store),
			oauth:                 identity.NewOAuthSettings(store, nil, nil),
		}
	}
	if email != nil {
		deps.Email = identity.NewEmailChallenges(email, nil)
	}
	deps.DomainRegistration, _ = users.(identity.RegistrationEmailDomainRepository)
	deps.NormalizedEmailConflict, _ = users.(identity.AuthNormalizedEmailBindingConflictChecker)
	deps.EmailAliasGuard, _ = users.(identity.AuthEmailIdentityAliasGuardRepository)
	deps.AliasLookup, _ = users.(identity.EmailAliasLookupRepository)
	deps.AliasOwner, _ = users.(identity.EmailAliasOwnerLookupRepository)
	state := identitypostgres.NewAuthState(client, deps)
	core := identity.NewAuthService(deps, &identitypostgres.AuthRepository{State: state})
	state.Rules = core
	return core
}
