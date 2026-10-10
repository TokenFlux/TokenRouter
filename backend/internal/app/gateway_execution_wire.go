//go:build wireinject

package app

import (
	"github.com/google/wire"

	gatewayadapter "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// gatewayExecutionProviders 绑定网关执行器、凭据、健康反馈和会话状态的构造函数。
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
	gatewayadapter.NewRoutePlanner,
	provideRetryCooldown,
	provideGatewayRequestDebug,
	provideMessagesExecution,
	provideGatewayModelAvailability,
	provideResponseHeaderFilter,
	provideOpenAIResponseState,
	provideExecutionAgentIdentity,
	provideAnthropicPromptCache,
	provideOpenAITextExecutor,
	provideOpenAIImageBridgePolicy,
	provideOpenAIEncryptedLineage,
	provideOpenAIWebSockets,
	provideResponsesWSRuntime,
	provideOpenAIResponses,
	provideOpenAIAuxiliary,
	provideSystemOneExecutor,
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
