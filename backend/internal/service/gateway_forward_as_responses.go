package service

import (
	"context"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	protocolanthropic "github.com/TokenFlux/TokenRouter/internal/protocol/anthropic"

	"github.com/gin-gonic/gin"
)

func (s *GatewayService) ForwardAsResponses(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	parsed *ParsedRequest,
) (*ForwardResult, error) {
	result, err := forwardcore.AsResponses(ctx, &conversionExecutionAdapter{s: s, c: c, account: account, responses: true}, forwardcore.ConversionInput{OAuth: account.IsOAuth()}, body)
	return legacyForwardExecutionResult(result), err
}

// adaptResponsesClientToolsForAnthropic 降级客户端专用工具，并保留响应还原所需映射。
func adaptResponsesClientToolsForAnthropic(body []byte) ([]byte, apicompat.ResponsesClientToolMapping, error) {
	return forwardcore.AdaptResponsesClientToolsForAnthropic(body)
}

func ExtractResponsesReasoningEffortFromBody(body []byte, modelCandidates ...string) *string {
	return forwardcore.ExtractEffort(body, false, normalizeOpenAIReasoningEffortForModel, modelCandidates...)
}

func mergeAnthropicUsage(dst *ClaudeUsage, src protocolanthropic.AnthropicUsage) {
	protocolanthropic.MergeAnthropicUsage(dst, src)
}

func writeResponsesError(c *gin.Context, statusCode int, code, message string) {
	output := gatewayhttp.ForwardConversionOutput{Context: c, Responses: true, Commit: func() { MarkResponseCommitted(c) }}
	output.Error(statusCode, code, message)
}

func mapUpstreamStatusCode(code int) int { return forwardcore.MapStatus(code) }
