package handler

import gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

// NewCompatibleTextHTTPHandler 仅委托原生 HTTP 构造，生产直接由 app 绑定。
func (h *GatewayHandler) NewCompatibleTextHTTPHandler() *gatewayhttp.CompatibleTextHandler {
	options := gatewayhttp.MessagesHTTPOptions{MaxBodyBytes: gatewayMaxBodySize(h.cfg), MaxSwitches: h.maxAccountSwitches, MaxGeminiSwitches: h.maxAccountSwitchesGemini}
	return gatewayhttp.NewBoundCompatibleTextHandler(options, h.messagesBindings(), h.gatewayService.ReplaceModelInBody, h.prompts, h.concurrencyHelper, h.NewCompatibleTextExecutor())
}
