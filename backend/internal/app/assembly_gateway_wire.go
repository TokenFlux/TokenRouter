//go:build wireinject

package app

import (
	gatewaysession "github.com/TokenFlux/TokenRouter/internal/gateway/session"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	gatewaypg "github.com/TokenFlux/TokenRouter/internal/gateway/postgres"

	gatewayredis "github.com/TokenFlux/TokenRouter/internal/gateway/rediscache"

	"github.com/TokenFlux/TokenRouter/internal/handler"

	"github.com/google/wire"
)

// 网关协议入口及剩余执行装配的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var gatewayAssemblyProviders = wire.NewSet(
	gatewaysession.NewDigestSessionStore,
	provideQoderRuntime,
	provideBackendMode,
	provideMediaHTTP,
	provideAuxiliaryHTTP,
	provideLiveHTTP,
	gatewaySearchProviders,
	provideMessageHTTPBindings,
	provideMessageAttemptRuntime,
	provideMessagesHTTP,
	provideGatewayRequestActivity,
	provideModelsHTTP,
	provideResponsesWSHTTP,
	provideOpenAITextHTTP,
	provideOpenAITokensHTTP,
	provideGeminiNativeHTTP,
	provideCompatibleTextHTTP,
	provideQoderCompatibleHTTP,
	provideCountTokensHTTP,
	provideGatewayPromptPolicy,
	ProvideGatewayCompletionRecorders,
	provideGatewayCompletionBindings,
	gatewayredis.NewGatewayCache,
	gatewayErrorRulesProviders,
	provideUsageRecordWorkerPool,
	provideQoderRequestActivity,
	provideQoderChat,
	provideGatewayForRouting,
	provideExecutionAccountStore,
	provideFundingAdmission,
	gatewayExecutionProviders,
	provideOpenAIHTTPResources,
	handler.ProvideOpenAIGatewayHandler,
	provideGatewayRouteMiddleware,
	provideGatewayAdminRules,
	provideGatewaySettings,
	provideGatewayRuntimeReaders,
	gatewayhttp.NewRuntimeSettingsHandler,
)
var gatewayErrorRulesProviders = wire.NewSet(provideGatewayErrorRules, gatewaypg.NewErrorPassthroughRepository, gatewayredis.NewErrorPassthroughCache, gatewayhttp.NewErrorPassthroughHandler)
