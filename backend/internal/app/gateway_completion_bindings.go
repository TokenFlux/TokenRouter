package app

import "github.com/TokenFlux/TokenRouter/internal/handler"

// gatewayCompletionReady 保证所有 HTTP 完成入口在启动前共享装配结果。
type gatewayCompletionReady struct{}

func provideGatewayCompletionBindings(recorders GatewayCompletionRecorders, h *handler.Handlers) *gatewayCompletionReady {
	h.Gateway.BindCompletionRecorder(recorders.Forward)
	h.OpenAIGateway.BindCompletionRecorder(recorders.OpenAI)
	h.OpenAIGateway.BindCyberHTTPHandler(h.OpenAIGateway.NewCyberHTTPHandler())
	h.QoderGateway.BindCompletionRecorder(recorders.Forward)
	return &gatewayCompletionReady{}
}
