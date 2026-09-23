//go:build wireinject

package app

import (
	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	identity "github.com/TokenFlux/TokenRouter/internal/identity"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"

	identityredis "github.com/TokenFlux/TokenRouter/internal/identity/rediscache"

	"github.com/google/wire"
)

// identity 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var identityAssemblyProviders = wire.NewSet(
	provideIdentityAdminHTTP,
	identityhttp.NewTotpHandler,
	provideIdentityAuthSettings,
	provideRiskStatus,
	provideEmailCache,
	provideEmailChallenges,
	provideIdentityAdmin,
	provideTotp,
	providePasskey,
	providePasskeyHTTP,
	provideTurnstile,
	provideTencentCaptcha,
	provideAliyunCaptcha,
	identity.NewUserAttributeService,
	identitypostgres.NewPasskeyRepository,
	identitypostgres.NewUserAttributeDefinitionRepository,
	identitypostgres.NewUserAttributeValueRepository,
	identityredis.NewTotpCache,
	identityredis.NewRefreshTokenCache,
	identityredis.NewPasskeySessionStore,
	identityprovider.NewTurnstileVerifier,
	identityprovider.NewTencentCaptchaVerifier,
	identityprovider.NewAliyunCaptchaVerifier,
	provideIdentityHTTP,
	provideIdentityAuthGraph,
	provideIdentityProfiles,
	identitypostgres.NewUserStore,
	provideSecretEncryptor,
	provideOAuthSettings,
	provideGrantSettings,
	provideIdentitySettings,
	identityhttp.NewAdminKeySettingsHandler,
	providePanelUserHTTP,
	provideJWTAuth,
	provideAdminAuth,
	provideStepUpAuth,
)
