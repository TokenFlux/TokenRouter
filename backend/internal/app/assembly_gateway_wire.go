//go:build wireinject

package app

import (
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewaypg "github.com/TokenFlux/TokenRouter/internal/gateway/postgres"

	gatewayredis "github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"

	"github.com/google/wire"
)

// 网关协议入口及剩余执行装配的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var gatewayAssemblyProviders = wire.NewSet(
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
	provideOpenAITokensHTTP,
	provideGeminiNativeHTTP,
	provideCompatibleTextHTTP,
	provideQoderCompatibleHTTP,
	provideCountTokensHTTP,
	provideGatewayPromptPolicy,
	ProvideGatewayCompletionRecorders,
	gatewayredis.NewGatewayCache,
	gatewayErrorRulesProviders,
	provideUsageRecordWorkerPool,
	provideQoderRequestActivity,
	provideQoderChat,
	provideExecutionAccountStore,
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
var gatewayErrorRulesProviders = wire.NewSet(provideGatewayErrorRules, gatewaypg.NewErrorPassthroughRepository, gatewayredis.NewErrorPassthroughCache, gatewayhttp.NewErrorPassthroughHandler)
