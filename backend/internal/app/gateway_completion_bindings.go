package app

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/handler"
)

// gatewayCompletionReady 保证所有 HTTP 完成入口在启动前共享装配结果。
type gatewayCompletionReady struct{}

func provideGatewayCompletionBindings(recorders GatewayCompletionRecorders, openai *handler.OpenAIGatewayHandler, cyber *gatewayhttp.CyberHandler) *gatewayCompletionReady {
	openai.BindCompletionRecorder(recorders.OpenAI)
	openai.BindCyberHTTPHandler(cyber)
	return &gatewayCompletionReady{}
}
