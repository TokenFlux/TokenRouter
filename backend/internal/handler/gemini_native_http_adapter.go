package handler

import (
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/upstream/gemini"
	"github.com/google/uuid"
)

// NewGeminiNativeHTTPHandler 仅委托原生 HTTP 构造，生产直接由 app 绑定。
func (h *GatewayHandler) NewGeminiNativeHTTPHandler() *gatewayhttp.GeminiNativeHandler {
	return gatewayhttp.NewBoundGeminiNativeHandler(gatewayhttp.GeminiNativeOptions{MaxSwitches: h.maxAccountSwitchesGemini}, h.messagesBindings(), gatewayhttp.GeminiHTTPBindings{SafeModelSegment: gemini.IsSafeGeminiModelPathSegment, FindSession: h.gatewayService.FindGeminiSession, BindSticky: h.gatewayService.BindStickySession}, h.prompts, h.concurrencyHelper, func() string { return uuid.New().String() }, h.NewGeminiNativeExecutor())
}
