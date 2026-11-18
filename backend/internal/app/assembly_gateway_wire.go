//go:build wireinject

package app

import (
	"github.com/google/wire"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewaypg "github.com/TokenFlux/TokenRouter/internal/gateway/postgres"
	gatewayredis "github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"
)

// assembly_gateway_wire.go registers gateway handlers, execution adapters,
// completion resources, and error-rule dependencies with Wire.
// Text, media, and live requests reach platform execution through handlers
// assembled around shared request activity and completion records. Persisted
// error rules and their Redis cache join the same graph, connecting gateway
// failure handling to administrator rule editing.

var (
	// gatewayAssemblyProviders supplies gateway HTTP handlers, platform execution
	// adapters, request activity, and completion dependencies to
	// initializeApplication.
	gatewayAssemblyProviders = wire.NewSet(
		gatewaysession.NewDigestSessionStore,
		provideQoderRuntime,
		provideBackendMode,
		provideMediaHTTP,
		provideMediaRuntime,
		provideAuxiliaryHTTP,
		provideLiveHTTP,
		provideLiveExecution,
		gatewaySearchProviders,
		provideMessageHTTPBindings,
		provideMessageAttemptRuntime,
		provideMessagesHTTP,
		provideGatewayRequestActivity,
		provideModelsHTTP,
		provideResponsesWSHTTP,
		provideOpenAITextHTTP,
		provideOpenAITextAttemptRuntime,
		provideOpenAIAttemptBindings,
		provideUnifiedTextExecutor,
		provideOpenAITokensHTTP,
		provideGeminiNativeHTTP,
		provideCountTokensHTTP,
		provideGatewayPromptPolicy,
		ProvideGatewayCompletionRecorders,
		gatewayredis.NewGatewayCache,
		gatewayErrorRulesProviders,
		provideUsageRecordWorkerPool,
		provideQoderRequestActivity,
		provideExecutionProviderStore,
		provideFundingAdmission,
		gatewayExecutionProviders,
		provideOpenAIHTTPResources,
		provideCyberBlocks,
		provideCyberHTTP,
		provideGatewayRouteMiddleware,
		provideGatewayAdminRules,
		provideGatewaySettings,
		provideGatewayRuntimeReaders,
		gatewayhttp.NewRuntimeSettingsHandler,
	)

	// gatewayErrorRulesProviders joins persisted error rules, their Redis cache,
	// and administrator HTTP access in the gateway graph.
	gatewayErrorRulesProviders = wire.NewSet(provideGatewayErrorRules, gatewaypg.NewErrorPassthroughRepository, gatewayredis.NewErrorPassthroughCache, gatewayhttp.NewErrorPassthroughHandler)
)
