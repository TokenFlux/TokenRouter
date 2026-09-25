//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/google/wire"
)

// gatewayExecutionProviders 只装配剩余执行入口；业务实现按已登记迁移账本继续清零。
var gatewayExecutionProviders = wire.NewSet(
	provideCodexTurnStateHeaders,
	provideGrokCredentialRecovery,
	provideRequestCredentials,
	provideGrokHealth,
	provideGrokExecutor,
	provideWSConnections,
	provideGrokVideoTasks,
	provideCompactExecutor,
	provideOpenAIResponseHealth,
	provideOpenAIResponseOutput,
	provideReasoningHistory,
	provideRequestCredentialExecutor,
	provider.NewRoutePlanner,
	provideRetryCooldown,
	provideGatewayRequestDebug,
	provideMessagesExecution,
	provideOpenAITLSRouters,
	provideGatewayModelAvailability,
	provideResponseHeaderFilter,
	provideOpenAIResponseState,
	provideOpenAIGatewayExecution,
	provideOpenAIAuxiliary,
	provideOpenAIImages,
	provideGatewayBillingRates,
	provideCreativeExecutor,
	provideCreativeTargets,
	provideGeminiForward,
	provideGeminiExecutor,
	provideAntigravityForward,
	provideAntigravityExecutor,
	provideAntigravityErrorObserver,
)
