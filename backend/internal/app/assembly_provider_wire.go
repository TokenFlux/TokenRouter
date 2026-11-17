//go:build wireinject

package app

import (
	"github.com/google/wire"

	providerauth "github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	providerredis "github.com/TokenFlux/TokenRouter/internal/provider/rediscache"
)

// assembly_provider_wire.go registers provider authorization, maintenance,
// health, and administration dependencies with Wire.
// Provider maintenance and credential refresh share runtime blocking state.
// The graph binds RuntimeBlockState as both RefreshFailureObserver and
// RuntimeUnblocker, so refresh failures and recovery operations reach the same
// instance alongside platform authorization and management services.

// providerAssemblyProviders assembles provider authorization, credentials,
// health, maintenance, and administration dependencies for the application
// graph.
var providerAssemblyProviders = wire.NewSet(
	providerauth.NewOAuthUsageCache,
	provideGeminiAuthorization,
	provideProviderProbeTasks,
	provideGrokAuthorization,
	provideGrokTokens,
	provideOpenAIAuthorization,
	provideOpenAITokens,
	provideOpenAIExecutionCredentials,
	provideAgentTaskCoordinator,
	provideClaudeTokens,
	provideMessageCredentials,
	provideGeminiTokens,
	provideAntigravityTokens,
	wire.Bind(new(providerauth.GrokRefreshTokenService), new(*providerauth.GrokAuthorization)),
	provideAntigravityAuthorization,
	provideClaudeAuthorization,
	provideTokenCacheInvalidator,
	provideQoderTokens,
	provideQoderCatalog,
	provideQoderRequestRefresh,
	provideQoderAuthorization,
	providerAuthorizationHTTPProviders,
	wire.Bind(new(providerhttp.GeminiAuthorizationUseCase), new(*providerauth.GeminiAuthorization)),
	wire.Bind(new(providerhttp.AntigravityAuthorizationUseCase), new(*providerauth.AntigravityAuthorization)),
	provideOpenAIQuota,
	provideGrokQuota,
	provideCodexInvites,
	wire.Bind(new(providerhttp.CodexInviteResetCommands), new(*providerauth.CodexInviteResetService)),
	provideOpenAIProviderOAuth,
	providerredis.NewTempUnschedCache,
	providerredis.NewTimeoutCounterCache,
	providerredis.NewOpenAI403CounterCache,
	provideGeminiPrecheck,
	provideGeminiQuotaPolicy,
	provideAntigravityQuota,
	provideradapter.NewGrokQuotaView,
	provideProviderModelSync,
	provideProviderTier,
	provideProviderManagementList,
	provideProviderRuntimePresenter,
	provideProviderRuntimeState,
	provideProviderHealthRuntime,
	provideProviderRecovery,
	provideProviderManagement,
	provideManagedRefresh,
	wire.Bind(new(providerauth.RefreshFailureObserver), new(*providerauth.RuntimeBlockState)),
	wire.Bind(new(providerauth.RuntimeUnblocker), new(*providerauth.RuntimeBlockState)),
	provideRefreshPlatforms,
	provideRefreshPostActions,
	provideBackgroundRefresh,
	wire.Bind(new(providerauth.GrokOAuthReconciler), new(*providerauth.BackgroundRefreshService)),
	provideOAuthUsageCore,
	provideOAuthUsageStats,
	providerhttp.NewOAuthUsageHandler,
	provideOllamaUsage,
	providerhttp.NewOllamaUsageHandler,
	provideCodexImporter,
	providerhttp.NewCodexImportHandler,
	provideCRSSync,
	provideCRSHTTP,
	provideProviderArchive,
	providerhttp.NewArchiveHandler,
	provideProviderImportProbes,
	provideGrokOAuthWithImports,
	provideUsageMonitor,
	provideBalanceAlertMonitor,
	provideProviderTestHTTP,
	provideUpstreamUsageHTTP,
	provideUpstreamUsage,
	provideSystemUsageTransport,
	provideProviderTests,
	provideAntigravityRetry,
	provideAntigravityProbe,
	provideScheduledTestRunner,
	provideScheduledTests,
	providerpostgres.NewScheduledTestPlanRepository,
	providerpostgres.NewScheduledTestResultRepository,
	providerhttp.NewScheduledTestHandler,
	provideProviderPrivacy,
	provideProviderAdmin,
	provideProviderExpiry,
	provideProviderDeferred,
	provideProviderRefresh,
	provideProviderStore,
	provideOAuthTokenCache,
	provideQuotaSettings,
	provideProviderSettings,
	providerhttp.NewRuntimeSettingsHandler,
)
