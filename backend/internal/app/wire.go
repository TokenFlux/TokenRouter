//go:build wireinject

package app

import (
	accountauth "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	creativeprovider "github.com/TokenFlux/TokenRouter/internal/creative/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewaytransport "github.com/TokenFlux/TokenRouter/internal/gateway/provider/transport"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"context"

	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"

	identityhttp "github.com/TokenFlux/TokenRouter/internal/identity/httpapi"

	"github.com/TokenFlux/TokenRouter/internal/payment"
	serverhttp "github.com/TokenFlux/TokenRouter/internal/server/httpapi"

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

	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"

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
	"github.com/TokenFlux/TokenRouter/internal/server"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/site"
	anthropicredis "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic/rediscache"

	sitepostgres "github.com/TokenFlux/TokenRouter/internal/site/postgres"
	"github.com/google/wire"
)

// initializeApplication 只构造和登记资源；运行由 Application.Run 统一启动。
func initializeApplication(ctx context.Context, cfg *config.Config, info BuildInfo, manager *lifecycle.Manager, restarter *lifecycle.Restarter, tasks *lifecycle.Tasks) (*Application, error) {
	wire.Build(provideProxyExpiry, accountauth.NewOAuthUsageCache, gatewaysession.NewDigestSessionStore, provideGeminiAuthorization, provideAccountProbeTasks,
		provideGrokAuthorization, provideGrokTokens, provideOpenAIAuthorization, provideOpenAITokens, provideClaudeTokens, provideGeminiTokens, provideAntigravityTokens,
		wire.Bind(new(accountauth.GrokRefreshTokenService), new(*accountauth.GrokAuthorization)), provideAntigravityAuthorization, provideClaudeAuthorization, provideTokenCacheInvalidator, provideQoderTokens, provideQoderRequestRefresh, provideQoderRuntime, provideQoderAuthorization, accountAuthorizationHTTPProviders, wire.Bind(new(accounthttp.GeminiAuthorizationUseCase), new(*accountauth.GeminiAuthorization)), wire.Bind(new(accounthttp.AntigravityAuthorizationUseCase), new(*accountauth.AntigravityAuthorization)), provideOpenAIQuota, provideGrokQuota, provideCodexInvites, wire.Bind(new(accounthttp.CodexInviteResetCommands), new(*accountauth.CodexInviteResetService)), provideIdentityAdminHTTP, provideKeyAdminHTTP, provideKeyHTTP, identityhttp.NewTotpHandler, provideSiteDisplay, provideIdentityAuthSettings, provideRoutingSettings, provideBackendMode, provideCalendar, cacheProviders, teamHTTPProviders, provideBackup, provideBackupHTTP, provideDataManagementHTTP, provideSystemLock, provideSystemOperations, provideSystemHTTP, provideS13TaskActivity, provideS13CreativePublic, provideCreativeWorkerRuntime, provideCompositeReadOptions, provideBatchPricing, provideS13BatchPublic, provideS13BatchDownload, provideS13BatchCleanup, provideBatchCleanupRuntime, provideS13BatchRuntime, provideS13BatchRegistry, provideS13CreativeHTTP, provideS13BatchHTTP, providePaymentExpiry, providePaymentHTTP, providePaymentAdminHTTP, providePaymentWebhookHTTP, providePaymentConfigCore, providePaymentRuntime, providePromotionPromoHTTP, providePromotionAffiliateHTTP, providePromotionPromoStore, providePromotionPromo, providePromotionAffiliateStore, providePromotionAffiliate, providePromotionSettings, provideMediaHTTP, provideAuxiliaryHTTP, provideLiveHTTP, ProvideGatewaySearchTools, ProvideGatewaySearchHTTP, provideMessagesHTTP, provideGatewayRequestActivity, provideModelsHTTP, provideResponsesWSHTTP, provideOpenAITextHTTP, provideGeminiNativeHTTP, provideCompatibleTextHTTP, provideQoderCompatibleHTTP, provideCountTokensHTTP, provideGatewayPromptPolicy, ProvideGatewayCompletionRecorders, provideGatewayCompletionBindings, gatewayredis.NewGatewayCache, gatewayErrorRulesProviders, provideUsageRecordWorkerPool, provideSearchRegistry, provideSearchRuntime, provideSearchHTTP, provideModerationStore, provideModerationHashes, provideRiskStatus, provideRiskDelivery, provideModerationCore, provideModerationHTTP, provideSitePublicHTTP, provideSitePublic, provideSitePages, provideAlertDelivery, provideBalanceNotifications, provideEmailCache, provideMailer, provideEmailChallenges, provideNotification, provideEmailQueue, provideNotificationHTTP, provideOpenAIAccountOAuth, provideQoderChat, provideOpsErrorQueue, provideUsageDashboardCache, providePublicUsage, providePublicBalanceUnit, opsProviders, provideUsageKeys, provideUsageUsers, provideUsageHTTP, provideUsageSettings, provideAuditSettings, provideAdminUsageHTTP, provideDashboardHTTP, provideSystemLogSink, provideUsageOptions, provideUsageStore, provideUsageRepository, provideUsageService, provideUsageAggregationRepository, provideUsageCleanupRepository, provideUsageAggregation, provideUsageCleanup, provideUsageDashboard, provideAuditRepository, provideAuditService, provideAuditHTTP, providePreAggregationSettings, schedulerProviders, accountredis.NewTempUnschedCache, accountredis.NewTimeoutCounterCache, accountredis.NewOpenAI403CounterCache, provideGeminiPrecheck, provideGeminiQuotaPolicy, provideAntigravityQuota, accountprovider.NewGrokQuotaView, provideAccountDiagnostics, provideAccountModelSync, provideAccountTier, provideAdminModelCatalog, provideAccountManagementList, provideAccountRuntimePresenter, provideAccountRecovery, provideAccountManagement, provideManagedRefresh, wire.Bind(new(accountauth.RefreshFailureObserver), new(*service.OpenAIGatewayService)), wire.Bind(new(accountauth.RuntimeUnblocker), new(*service.OpenAIGatewayService)), provideRefreshPlatforms, provideRefreshPostActions, provideBackgroundRefresh, wire.Bind(new(accountauth.GrokOAuthReconciler), new(*accountauth.BackgroundRefreshService)), provideOAuthUsageCore, provideOAuthUsageStats, accounthttp.NewOAuthUsageHandler, provideOllamaUsage, accounthttp.NewOllamaUsageHandler, provideCodexImporter, accounthttp.NewCodexImportHandler, provideCRSSync, provideCRSHTTP, provideProxyTransfer, provideAccountArchive, accounthttp.NewArchiveHandler, provideAccountImportProbes, provideGrokOAuthWithImports, provideCNUsageMonitor, provideAccountTestHTTP, provideUpstreamUsageHTTP, provideUpstreamUsage, provideAccountTests, provideAntigravityRetry, provideAntigravityProbe, provideScheduledTestRunner, provideScheduledTests, accountpostgres.NewScheduledTestPlanRepository, accountpostgres.NewScheduledTestResultRepository, accounthttp.NewScheduledTestHandler, provideGatewayForRouting, provideRoutingModelList, provideRequestableCatalogue, provideMarketplace, provideMarketplaceHTTP, provideAccountPrivacy, provideAccountAdmin, provideAccountUsage, provideAccountExpiry, provideAccountDeferred, provideAccountRefresh, provideAccountStore, provideLegacyAccountStore, provideGroupRateAdmin, provideGroupCapacity, provideGroupProbeRunner, provideRoutingGroupHTTP, provideRoutingGroupStore, provideGroupReader, provideRoutingGroupAdmin, provideRoutingChannels, provideChannelCatalog, routingpostgres.NewChannelStore, routinghttp.NewChannelHandler, provideProxyHTTP, wire.Bind(new(accountprovider.OpenAITokenRouterReader), new(*egress.TLSFingerprintRouterService)), wire.Bind(new(accountprovider.OpenAITokenProfileResolver), new(*provider.TLSProfiles)), provideEgressProxyStore, wire.Bind(new(egress.ProxyRepository), new(*egresspostgres.ProxyStore)), provideEgressProbe, provideEgressAdmin, egressredis.NewProxyLatencyCache, egresspostgres.NewTLSFingerprintProfileRepository, egresspostgres.NewTLSFingerprintRouterRepository, egressredis.NewTLSFingerprintProfileCache, egressredis.NewTLSFingerprintRouterCache, provideEgressProfiles, provider.NewTLSProfiles, provideEgressRouters, provideEgressCollector, provideEgressProfileHTTP, egresshttp.NewTLSFingerprintRouterHandler, provideIdentityAdmin, provideKeyAdmin, provideKeyStore, provideKeyRepository, provideKeys, provideKeyInvalidator, apikey.ProvideAuthCacheInvalidationWorker, keyredis.NewAPIKeyCache, keypostgres.NewAuthCacheInvalidationOutboxRepository, provideTotp, providePasskey, providePasskeyHTTP, provideTurnstile, provideTencentCaptcha, provideAliyunCaptcha, identity.NewUserAttributeService, identitypostgres.NewPasskeyRepository, identitypostgres.NewUserAttributeDefinitionRepository, identitypostgres.NewUserAttributeValueRepository, identityredis.NewTotpCache, identityredis.NewRefreshTokenCache, identityredis.NewPasskeySessionStore, identityprovider.NewTurnstileVerifier, identityprovider.NewTencentCaptchaVerifier, identityprovider.NewAliyunCaptchaVerifier, provideIdentityHTTP, provideTeam, provideTeamRepository, teamredis.NewTeamInvitationLimiter, provideIdentityAuthGraph, provideIdentityProfiles, identitypostgres.NewUserStore, provideUserRepository, provideBillingCalculator, provideBillingPriceResolver, provideSubscriptionExpiry, provideBillingPlans, wire.Bind(new(billinghttpapi.RedeemAdministrator), new(*billing.RedeemAdmin)), provideRedeemAdministration, provideBalanceAdjuster, provideBillingRedeem, providePlatformQuotas, provideQuotaHTTP, wire.Bind(new(identity.DefaultSubscriptionAssigner), new(*billing.SubscriptionService)), billing.NewQuotaCoordinator, provideBillingEligibility, provideWindowCostCache, provideFundingAdmission, providePlatformQuotaFlusher, provideBillingSubscriptions, provideSettlementStore, provideBillingFunds, wire.Bind(new(completion.Store), new(*billingpostgres.SettlementStore)), upstreamClientProviders, wire.Bind(new(httpclient.UpstreamTransport), new(*gatewaytransport.Client)), wire.Bind(new(creativeprovider.ExecutionGroups), new(*routingpostgres.GroupStore)), service.ProviderSet, paymentProviders, provideAPIKeyAuth, nativeHTTPProviders, handler.NewGatewayHandler, handler.ProvideOpenAIGatewayHandler, handler.NewQoderGatewayHandler, server.ProviderSet,
		provideEnt, provideRedis, provideOAuthTokenCache, providePrivacyClientFactory, provideSecretEncryptor, storageProviders, moduleStorageProviders, provideHTTPOptions, provideHTTPRouteMount, provideAuthRouteMount, provideUserRouteMount, provideAdminRouteMount, provideGatewayRouteMount, providePaymentRouteMount, provideGatewayRouteMiddleware, provideCompositeSettingsHTTP, provideCompositeRuntime, provideOAuthSettings, provideGrantSettings, provideForwardedSettings, provideSchedulerAdminDefaults, provideGatewayAdminRules, provideQuotaSettings,  provideCreativeRuntimeSettings, provideSettingsParticipants, providePreAggregationHTTP, provideCreativeSettingsHTTP, provideGatewaySettings, provideGatewayRuntimeReaders, provideModerationSettings, gatewayhttp.NewRuntimeSettingsHandler, provideIdentitySettings, identityhttp.NewAdminKeySettingsHandler, provideAccountSettings, accounthttp.NewRuntimeSettingsHandler, providePanelSettings, serverhttp.NewPanelSettingsHandler, providePanelUserHTTP, providePromotionUserHTTP, provideRouterRuntime, provideJWTAuth, provideAdminAuth, provideStepUpAuth, provideAuditMiddleware, provideAuditRedactor, providePricingService, anthropicredis.NewFingerprintStore, billingredis.NewBillingCache, wire.Bind(new(billing.BillingCache), new(*billingredis.Cache)),
		site.NewAnnouncementService, sitepostgres.NewAnnouncementRepository, sitepostgres.NewAnnouncementReadRepository, provideAnnouncementUsers, provideAnnouncementSubscriptions, provideAnnouncementExpiry,
		provideBootRuntime, provideAuthRuntime, provideMaintenanceRuntime, provideOpsRuntime, provideQueuesRuntime, provideJobsRuntime, provideCoreRuntime, provideRuntime, provideApplication, providePlatformQuotaStore)
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
