package service

import (
	"context"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/gin-gonic/gin"
)

// forwardAnthropicViaRawChatCompletions 将 `/v1/messages` 客户端请求桥接到
// 仅支持 `/v1/chat/completions` 的 OpenAI 兼容上游。
//
// 转换链直接跳过 Responses 中间表示：
//
//	请求：Anthropic Messages → Chat Completions
//	响应：Chat Completions chunk/response → Anthropic events/response
//
// 该函数与服务 `/v1/responses` 的 forwardResponsesViaRawChatCompletions 对应，
// 但每个流式 token 只经过一个状态机，不再往返 Responses 表示。
func (s *OpenAIGatewayService) forwardAnthropicViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	defaultMappedModel string,
	tlsRouterMatch ...TLSFingerprintRouterMatchResult,
) (*OpenAIForwardResult, error) {
	adapter := &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: forward.NativeMessages}
	result, err := forward.MessagesViaRawChat(ctx, body, defaultMappedModel, adapter)
	return openAIForwardResultFromHTTP(result), err
}
