//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/google/wire"
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
	provider.NewRoutePlanner,
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
	provideOpenAIResponses,
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
