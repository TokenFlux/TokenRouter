package service

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/egress"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/gin-gonic/gin"
)

// forwardResponsesViaRawChatCompletions 将 `/v1/responses` 入站请求桥接到
// 只支持 `/v1/chat/completions` 的上游。
func (s *OpenAIGatewayService) forwardResponsesViaRawChatCompletions(
	ctx context.Context,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	body []byte,
	tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult,
) (*forwardcore.OpenAIResult, error) {
	adapter := &openAIRawFallbackAdapter{openAIMessagesExecutionAdapter: &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch}, kind: forward.NativeResponses}
	result, err := forward.ResponsesViaRawChat(ctx, body, adapter)
	return openAIForwardResultFromHTTP(result), err
}
