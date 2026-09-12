//go:build wireinject

package app

import (
	"context"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	keyredis "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	identityredis "github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	teamredis "github.com/TokenFlux/TokenRouter/internal/team/rediscache"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
	"github.com/TokenFlux/TokenRouter/internal/payment"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/server"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	sitepostgres "github.com/TokenFlux/TokenRouter/internal/site/postgres"

	"github.com/google/wire"
)

// initializeApplication 只构造和登记资源；运行由 Application.Run 统一启动。
func initializeApplication(ctx context.Context, cfg *config.Config, info BuildInfo, manager *lifecycle.Manager, restarter *lifecycle.Restarter, tasks *lifecycle.Tasks) (*Application, error) {
	wire.Build(provideIdentityAdmin, provideKeyAdmin, provideLegacyAdmin, provideKeyStore, provideLegacyKeys, provideKeys, provideLegacyKeyService, provideKeyInvalidator, apikey.ProvideAuthCacheInvalidationWorker, keyredis.NewAPIKeyCache, keypostgres.NewAuthCacheInvalidationOutboxRepository, provideTotp, providePasskey, provideTurnstile, provideTencentCaptcha, provideAliyunCaptcha, identity.NewUserAttributeService, identitypostgres.NewPasskeyRepository, identitypostgres.NewUserAttributeDefinitionRepository, identitypostgres.NewUserAttributeValueRepository, identityredis.NewTotpCache, identityredis.NewRefreshTokenCache, identityredis.NewPasskeySessionStore, identityprovider.NewTurnstileVerifier, identityprovider.NewTencentCaptchaVerifier, identityprovider.NewAliyunCaptchaVerifier, provideIdentityHTTP, provideTeam, provideTeamRepository, teamredis.NewTeamInvitationLimiter, provideIdentityAuthGraph, provideLegacyAuth, provideIdentityProfiles, provideLegacyProfiles, identitypostgres.NewUserStore, provideLegacyUserRepository, provideBillingCalculator, provideBillingPriceResolver, provideSubscriptionExpiry, provideBillingPlans, wire.Bind(new(billinghttpapi.RedeemAdministrator), new(*billing.RedeemAdmin)), provideRedeemAdministration, provideBalanceAdjuster, provideBillingRedeem, providePlatformQuotas, provideQuotaHTTP, wire.Bind(new(service.DefaultSubscriptionAssigner), new(*billing.SubscriptionService)), billing.NewQuotaCoordinator, provideBillingEligibility, provideLegacyBillingEligibility, providePlatformQuotaFlusher, provideBillingSubscriptions, provideSettlementStore, provideBillingFunds, repository.NewUsageBillingAdapter, repository.ProviderSet, service.ProviderSet, payment.ProviderSet, middleware.ProviderSet, handler.ProviderSet, server.ProviderSet,
		provideEnt, provideRedis, providePrivacyClientFactory, provideServiceBuildInfo, provideHandlerBuildInfo, provideSecretEncryptor, provideSettingsStore, provideRouterRuntime, providePricingService,
		site.NewAnnouncementService, sitepostgres.NewAnnouncementRepository, sitepostgres.NewAnnouncementReadRepository, provideAnnouncementUsers, provideAnnouncementSubscriptions, provideAnnouncementExpiry,
		provideRestartRequester, provideBootRuntime, provideAuthRuntime, provideMaintenanceRuntime, provideOpsRuntime, provideQueuesRuntime, provideJobsRuntime, provideCoreRuntime, provideRuntime, provideApplication)
	return nil, nil
}
