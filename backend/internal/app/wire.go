//go:build wireinject

package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaypg "github.com/TokenFlux/TokenRouter/internal/gateway/postgres"
	gatewayredis "github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountredis "github.com/TokenFlux/TokenRouter/internal/account/rediscache"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	keyredis "github.com/TokenFlux/TokenRouter/internal/apikey/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	egress "github.com/TokenFlux/TokenRouter/internal/egress"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	egressredis "github.com/TokenFlux/TokenRouter/internal/egress/rediscache"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	identityprovider "github.com/TokenFlux/TokenRouter/internal/identity/provider"
	identityredis "github.com/TokenFlux/TokenRouter/internal/identity/rediscache"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	teamredis "github.com/TokenFlux/TokenRouter/internal/team/rediscache"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/handler"
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
	wire.Build(providePaymentExpiry, provideNativePaymentRuntime, providePaymentHTTP, providePaymentAdminHTTP, providePaymentWebhookHTTP, providePaymentConfigCore, providePaymentService, providePaymentConfiguration, providePromotionPromoHTTP, providePromotionAffiliateHTTP, providePromotionPromoStore, providePromotionPromo, providePromotionAffiliateStore, providePromotionAffiliate, provideMediaHTTP, provideAuxiliaryHTTP, provideLiveHTTP, ProvideGatewaySearchTools, ProvideGatewaySearchHTTP, provideMessagesHTTP, provideGatewayRequestActivity, provideModelsHTTP, provideResponsesWSHTTP, provideOpenAITextHTTP, provideGeminiNativeHTTP, provideCompatibleTextHTTP, provideQoderCompatibleHTTP, provideCountTokensHTTP, provideGatewayPromptPolicy, ProvideGatewayCompletionRecorders, provideGatewayCompletionBindings, gatewayredis.NewGatewayCache, gatewayErrorRulesProviders, provideUsageRecordWorkerPool, provideSearchRegistry, provideSearchRuntime, provideSearchHTTP, provideModerationStore, provideModerationHashes, provideRiskStatus, provideRiskDelivery, provideModerationCore, provideLegacyModeration, provideModerationHTTP, provideSitePublicHTTP, provideSitePublic, provideSitePages, provideAlertDelivery, provideBalanceNotifications, provideLegacyBalanceNotifications, provideEmailCache, provideMailer, provideEmailChallenges, provideLegacyEmail, provideNotification, provideEmailQueue, provideNotificationHTTP, provideOpenAIAccountOAuth, provideQoderChat, provideOpsErrorQueue, provideUsageDashboardCache, providePublicUsage, opsProviders, provideUsageKeys, provideUsageUsers, provideUsageHTTP, provideAdminUsageHTTP, provideDashboardHTTP, provideSystemLogSink, provideUsageOptions, provideUsageStore, provideLegacyUsageRepository, provideUsageService, provideLegacyUsageService, provideUsageAggregationRepository, provideUsageCleanupRepository, provideUsageAggregation, provideLegacyUsageAggregation, provideUsageCleanup, provideUsageDashboard, provideAuditRepository, provideAuditService, provideAuditHTTP, providePreAggregationSettings, schedulerProviders, accountredis.NewTempUnschedCache, accountredis.NewTimeoutCounterCache, accountredis.NewOpenAI403CounterCache, provideGeminiPrecheck, provideGeminiQuotaPolicy, provideLegacyGeminiQuota, provideAccountDiagnostics, provideAccountModelSync, provideAccountTier, provideAdminModelCatalog, provideAccountManagementList, provideAccountRuntimePresenter, provideAccountRecovery, provideAccountManagement, provideManagedRefresh, provideBackgroundRefresh, provideOAuthUsageCore, provideOAuthUsageStats, accounthttp.NewOAuthUsageHandler, provideOllamaUsage, provideOllamaUsageCore, accounthttp.NewOllamaUsageHandler, provideCodexImporter, accounthttp.NewCodexImportHandler, provideCRSSync, provideCRSHTTP, provideProxyTransfer, provideAccountArchive, accounthttp.NewArchiveHandler, provideAccountImportProbes, provideGrokOAuthWithImports, provideCNUsageMonitor, provideAccountTestHTTP, provideUpstreamUsageHTTP, provideUpstreamUsage, provideAccountTests, provideScheduledTestRunner, provideScheduledTests, accountpostgres.NewScheduledTestPlanRepository, accountpostgres.NewScheduledTestResultRepository, accounthttp.NewScheduledTestHandler, provideGatewayForRouting, provideRoutingModelList, provideMarketplace, provideMarketplaceHTTP, provideAccountPrivacy, provideAccountAdmin, provideAccountUsage, provideAccountExpiry, provideAccountDeferred, provideAccountRefresh, provideLegacyAccountRefresh, provideAccountStore, provideLegacyAccountStore, provideLegacyAccountReader, provideGroupRateAdmin, provideGroupCapacity, provideGroupProbeRunner, provideRoutingGroupHTTP, provideRoutingGroupStore, provideLegacyGroupStore, provideLegacyGroupReader, provideRoutingGroupAdmin, provideBillingCalculatorCore, wire.Bind(new(service.ChannelCacheInvalidator), new(*service.ChannelService)), provideRoutingChannels, provideLegacyChannels, provideChannelCatalog, routingpostgres.NewChannelStore, routinghttp.NewChannelHandler, provideProxyHTTP, wire.Bind(new(service.OpenAIOAuthTokenRouterReader), new(*service.TLSFingerprintRouterService)), wire.Bind(new(service.OpenAIOAuthTokenProfileResolver), new(*service.TLSFingerprintProfileService)), provideEgressProxyStore, wire.Bind(new(egress.ProxyRepository), new(*egresspostgres.ProxyStore)), provideEgressProbe, provideEgressAdmin, egressredis.NewProxyLatencyCache, egresspostgres.NewTLSFingerprintProfileRepository, egresspostgres.NewTLSFingerprintRouterRepository, egressredis.NewTLSFingerprintProfileCache, egressredis.NewTLSFingerprintRouterCache, provideEgressProfiles, provideLegacyTLSProfiles, provideEgressRouters, provideEgressCollector, provideEgressProfileHTTP, egresshttp.NewTLSFingerprintRouterHandler, provideIdentityAdmin, provideKeyAdmin, provideLegacyAdmin, provideKeyStore, provideLegacyKeys, provideKeys, provideLegacyKeyService, provideKeyInvalidator, apikey.ProvideAuthCacheInvalidationWorker, keyredis.NewAPIKeyCache, keypostgres.NewAuthCacheInvalidationOutboxRepository, provideTotp, providePasskey, provideTurnstile, provideTencentCaptcha, provideAliyunCaptcha, identity.NewUserAttributeService, identitypostgres.NewPasskeyRepository, identitypostgres.NewUserAttributeDefinitionRepository, identitypostgres.NewUserAttributeValueRepository, identityredis.NewTotpCache, identityredis.NewRefreshTokenCache, identityredis.NewPasskeySessionStore, identityprovider.NewTurnstileVerifier, identityprovider.NewTencentCaptchaVerifier, identityprovider.NewAliyunCaptchaVerifier, provideIdentityHTTP, provideTeam, provideTeamRepository, teamredis.NewTeamInvitationLimiter, provideIdentityAuthGraph, provideLegacyAuth, provideIdentityProfiles, provideLegacyProfiles, identitypostgres.NewUserStore, provideLegacyUserRepository, provideBillingCalculator, provideBillingPriceResolver, provideSubscriptionExpiry, provideBillingPlans, wire.Bind(new(billinghttpapi.RedeemAdministrator), new(*billing.RedeemAdmin)), provideRedeemAdministration, provideBalanceAdjuster, provideBillingRedeem, providePlatformQuotas, provideQuotaHTTP, wire.Bind(new(service.DefaultSubscriptionAssigner), new(*billing.SubscriptionService)), billing.NewQuotaCoordinator, provideBillingEligibility, provideLegacyBillingEligibility, providePlatformQuotaFlusher, provideBillingSubscriptions, provideSettlementStore, provideBillingFunds, repository.NewUsageBillingAdapter, repository.ProviderSet, service.ProviderSet, paymentProviders, middleware.ProviderSet, handler.ProviderSet, server.ProviderSet,
		provideEnt, provideRedis, provideOAuthTokenCache, providePrivacyClientFactory, provideHandlerBuildInfo, provideSecretEncryptor, provideSettingsStore, provideRouterRuntime, providePricingService,
		site.NewAnnouncementService, sitepostgres.NewAnnouncementRepository, sitepostgres.NewAnnouncementReadRepository, provideAnnouncementUsers, provideAnnouncementSubscriptions, provideAnnouncementExpiry,
		provideRestartRequester, provideBootRuntime, provideAuthRuntime, provideMaintenanceRuntime, provideOpsRuntime, provideQueuesRuntime, provideJobsRuntime, provideCoreRuntime, provideRuntime, provideApplication)
	return nil, nil
}

var gatewayErrorRulesProviders = wire.NewSet(provideGatewayErrorRules, gatewaypg.NewErrorPassthroughRepository, gatewayredis.NewErrorPassthroughCache, gatewayhttp.NewErrorPassthroughHandler)

// 支付 Wire 集合只供生成器使用，运行构造函数留在普通 app 文件。
// ProviderSet is the Wire provider set for the payment package.
var paymentProviders = wire.NewSet(
	paymentpostgres.NewInstanceStore,
	providePaymentEncryptionKey,
	providePaymentRegistry,
	providePaymentLoadBalancer,
	wire.Bind(new(payment.LoadBalancer), new(*payment.DefaultLoadBalancer)),
)
