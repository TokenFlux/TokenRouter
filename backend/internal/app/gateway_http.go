package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
)

// provideMessagesHTTP 在开放路由前构造一次固定依赖的 Messages HTTP 适配器。
func provideMessagesHTTP(legacy *handler.GatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.MessagesHandler {
	legacy.BindCompletionRecorder(recorders.Forward)
	result := legacy.NewMessagesHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// 媒体、Live 与工具入口共享已经取得的实例，只在组合根构造 HTTP 门面。
func provideMediaHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.MediaHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.MediaHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
func provideAuxiliaryHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.AuxiliaryHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.AuxiliaryHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
func provideLiveHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.LiveHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.NewLiveHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideCountTokensHTTP 仅装配无槽计数，不创建后台任务。
func provideCountTokensHTTP(legacy *handler.GatewayHandler, activity *gatewayRequestActivity) *gatewayhttp.CountTokensHandler {
	result := legacy.NewCountTokensHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideQoderCompatibleHTTP 在路由开放前绑定唯一的固定依赖入口。
func provideQoderCompatibleHTTP(legacy *handler.QoderGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.QoderCompatibleHandler {
	legacy.BindCompletionRecorder(recorders.Forward)
	result := legacy.NewCompatibleHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideCompatibleTextHTTP 在路由开放前绑定唯一的固定依赖入口。
func provideCompatibleTextHTTP(legacy *handler.GatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.CompatibleTextHandler {
	legacy.BindCompletionRecorder(recorders.Forward)
	result := legacy.NewCompatibleTextHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideGeminiNativeHTTP 在路由开放前绑定唯一的固定依赖入口。
func provideGeminiNativeHTTP(legacy *handler.GatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.GeminiNativeHandler {
	legacy.BindCompletionRecorder(recorders.Forward)
	result := legacy.NewGeminiNativeHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideOpenAITextHTTP 固定绑定入口，逐请求仅建立执行状态。
func provideOpenAITextHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.OpenAITextHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.NewOpenAITextHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideResponsesWSHTTP 固定绑定入口，逐请求仅建立执行状态。
func provideResponsesWSHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.ResponsesWSHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.NewResponsesWSHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}

// provideModelsHTTP 只绑定读取投影与展示，不取得请求槽或资金能力。
func provideModelsHTTP(legacy *handler.GatewayHandler, activity *gatewayRequestActivity) *gatewayhttp.ModelsHandler {
	result := legacy.NewModelsHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
