package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
)

// 媒体、Live 与工具入口共享已经取得的实例，只在组合根构造 HTTP 门面。
func provideMediaHTTP(legacy *handler.OpenAIGatewayHandler, cyber *gatewayhttp.CyberHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.MediaHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	legacy.BindCyberHTTPHandler(cyber)
	result := legacy.MediaHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
func provideAuxiliaryHTTP(legacy *handler.OpenAIGatewayHandler, cyber *gatewayhttp.CyberHandler, activity *gatewayRequestActivity, recorders GatewayCompletionRecorders) *gatewayhttp.AuxiliaryHandler {
	legacy.BindCompletionRecorder(recorders.OpenAI)
	legacy.BindCyberHTTPHandler(cyber)
	result := legacy.AuxiliaryHTTPHandler()
	result.BindRequestActivity(activity.Enter)
	return result
}
