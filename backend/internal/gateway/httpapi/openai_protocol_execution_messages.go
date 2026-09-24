package httpapi

import (
	"context"
	"net/http"

	openaiexecution "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/gin-gonic/gin"
)

// Messages 只保留旧签名，Messages 请求和恢复编排由目标执行器唯一拥有。
func (s *OpenAITextExecutor) Messages(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
	result, err := openaiexecution.RunMessages(ctx, body, promptCacheKey, defaultMappedModel, &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch})
	return openaiexecution.ToForwardResult(result), err
}

// messagesError reads an upstream error and returns it in
// Anthropic error format.
func (s *OpenAITextExecutor) messagesError(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	return s.Output.CompatError(resp, c, account, WriteForwardAnthropicError, WriteForwardAnthropicErrorBody, requestedModel...)
}
