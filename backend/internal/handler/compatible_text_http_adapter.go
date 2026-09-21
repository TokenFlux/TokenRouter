// 通用 Responses/Chat 的固定端口只做字段投影和单步调用，不包含旧 HTTP 处理回调。
package handler

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	media "github.com/TokenFlux/TokenRouter/internal/gateway/media"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
	"github.com/TokenFlux/TokenRouter/internal/gateway/telemetry"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	textflow "github.com/TokenFlux/TokenRouter/internal/gateway/text"
	"github.com/TokenFlux/TokenRouter/internal/identity/httpapi/authctx"
	"github.com/TokenFlux/TokenRouter/internal/moderation"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

type compatibleTextHTTPBackend struct{ messagesHTTPBackend }

// NewCompatibleTextHTTPHandler 可由 app 创建一次后直接绑定两个通用兼容路由。
func (h *GatewayHandler) NewCompatibleTextHTTPHandler() *gatewayhttp.CompatibleTextHandler {
	options := gatewayhttp.MessagesHTTPOptions{
		MaxBodyBytes:      gatewayMaxBodySize(h.cfg),
		MaxSwitches:       h.maxAccountSwitches,
		MaxGeminiSwitches: h.maxAccountSwitchesGemini,
	}
	return gatewayhttp.NewCompatibleTextHandler(options, compatibleTextHTTPBackend{messagesHTTPBackend{h}}, h.gatewayService, h.concurrencyHelper, h.NewCompatibleTextExecutor())
}
func (p compatibleTextHTTPBackend) ImageIntent(key *apikey.APIKey, model string, body []byte, mapping routing.ChannelMappingResult) ([]byte, bool) {
	projected := apikey.CopyAPIKey(key)
	forwarded, _, image := resolveOpenAIChannelMappedImageIntent("/v1/responses", model, body, routing.ChannelMappingResult(mapping), openAICompatibleRequestPlatform(projected), p.h.gatewayService.ReplaceModelInBody)
	return forwarded, image
}
func (p compatibleTextHTTPBackend) ImageContext(ctx context.Context) context.Context {
	return requeststate.WithOpenAIImageGenerationIntent(ctx)
}
func (p compatibleTextHTTPBackend) ChatImageModel(model string, mapping routing.ChannelMappingResult) bool {
	return media.IsGPTImageGenerationModel(openAIChannelMappedModel(model, routing.ChannelMappingResult(mapping)))
}
func (p compatibleTextHTTPBackend) Moderate(c *gin.Context, log *zap.Logger, key *apikey.APIKey, subject authctx.AuthSubject, protocol, model string, body []byte) *moderation.Decision {
	return p.h.checkContentModeration(c, log, apikey.CopyAPIKey(key), subject, protocol, model, body)
}
func (p compatibleTextHTTPBackend) AuthLatency(c *gin.Context, millis int64) {
	gatewayhttp.SetOpsLatencyMs(c, gatewayhttp.OpsAuthLatencyMsKey, millis)
}

// NewCompatibleTextExecutor 在 Recorder 完成绑定后构造，不增加共享运行状态。
func (h *GatewayHandler) NewCompatibleTextExecutor() *textflow.MessagesExecutor {
	runtime := &fixedMessagesRuntime{dependencies: newMessageExecutionDependencies(h)}
	return textflow.NewMessagesExecutor(runtime, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitches, StopOnCanceledContext: true, Observe: telemetry.Failover}, textflow.MessageOptions{MaxSwitches: h.maxAccountSwitchesGemini, StopOnCanceledContext: true, Observe: telemetry.Failover})
}
