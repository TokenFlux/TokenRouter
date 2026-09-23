package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
)

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

// provideResponsesWSHTTP 固定绑定入口，逐请求仅建立执行状态。
func provideResponsesWSHTTP(legacy *handler.OpenAIGatewayHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.ResponsesWSHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	result := legacy.NewResponsesWSHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
