package httpapi

import (
	"context"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	openaiprotocol "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/TokenFlux/TokenRouter/internal/gateway/media"
	gatewayws "github.com/TokenFlux/TokenRouter/internal/gateway/ws"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// openAIWSImageIntentForRoutingModel 用渠道模型 C 还原判定请求体，避免账号模型 U 改写生图语义。
// 宽泛意图供图片状态和计费使用，显式意图只负责权限门禁。
func openAIWSImageIntentForRoutingModel(routingModel, upstreamModel string, body []byte, platform string) ([]byte, bool, bool) {
	imageIntentBody := body
	if routingModel != upstreamModel {
		imageIntentBody = openaiprotocol.ReplaceModelInBody(body, routingModel)
	}
	imageIntent := gatewayprovider.ImageIntentForPlatform(media.OpenAIResponsesEndpoint, routingModel, imageIntentBody, platform)
	explicitImageIntent := gatewayprovider.ImageIntent().IsExplicitImageGenerationIntent(media.OpenAIResponsesEndpoint, routingModel, imageIntentBody)
	return imageIntentBody, imageIntent, explicitImageIntent
}

// openAIWSIngressInterTurnIdleTimeout 返回已完成轮次之间允许的客户端空闲时间。
func (s *OpenAIWebSocketExecutor) openAIWSIngressInterTurnIdleTimeout() time.Duration {
	if s == nil || s.Options == nil || s.Options.IngressInterTurnIdleTimeoutSeconds <= 0 {
		return 0
	}
	return time.Duration(s.Options.IngressInterTurnIdleTimeoutSeconds) * time.Second
}

// newOpenAIWSDownstreamWriteContext 让下行写直接绑定客户端生命周期，
// 排除独立的 ingress 租约取消信号，使租约丢失时当前帧可先完成，再发送重试关闭帧。
func newOpenAIWSDownstreamWriteContext(controlCtx context.Context, hooks *gatewayws.OpenAIIngressHooks, timeout time.Duration) (context.Context, context.CancelFunc) {
	writeParent := controlCtx
	if hooks != nil && hooks.ClientLifecycleContext != nil {
		writeParent = hooks.ClientLifecycleContext
	}
	if writeParent == nil {
		writeParent = context.Background()
	}
	return context.WithTimeout(writeParent, timeout)
}

// ProxyResponsesWebSocketFromClient 保留现有平台适配入口，逐轮编排由 gateway/ws 持有。
func (s *OpenAIWebSocketExecutor) ProxyResponsesWebSocketFromClient(ctx context.Context, c *gin.Context, clientConn *coderws.Conn, account *gatewayprovider.ExecutionAccount, token string, firstClientMessage []byte, hooks *gatewayws.OpenAIIngressHooks) error {
	return s.executeWSIngressAdapter(ctx, c, clientConn, account, token, firstClientMessage, hooks)
}
