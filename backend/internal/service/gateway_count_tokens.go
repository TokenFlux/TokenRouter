package service

import (
	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"

	claude "github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	"github.com/gin-gonic/gin"
)

// ForwardCountTokens 转发 count_tokens 请求到上游 API
// 特点：不记录使用量、仅支持非流式响应
func (s *GatewayService) ForwardCountTokens(ctx context.Context, c *gin.Context, account *Account, parsed *ParsedRequest) error {
	adapter := &countExecutionAdapter{messageExecutionAdapter: newMessageExecutionAdapter(s, c, account)}
	return forwardcore.CountTokens(ctx, adapter, adapter.input(), parsed)
}

func (s *GatewayService) buildCountTokensRequestAnthropicAPIKeyPassthrough(
	ctx context.Context,
	c *gin.Context,
	account *Account,
	body []byte,
	token string,
) (*http.Request, error) {
	o := s.countTokensRequestOptions(ctx, c, account, "", "apikey", false, true)
	return claude.BuildCountTokensRequestPassthrough(ctx, body, token, o)
}

func (s *GatewayService) buildCountTokensRequest(ctx context.Context, c *gin.Context, account *Account, body []byte, token, tokenType, modelID string, mimicClaudeCode bool) (*http.Request, []byte, error) {
	o := s.countTokensRequestOptions(ctx, c, account, modelID, tokenType, mimicClaudeCode, false)
	return claude.BuildCountTokensRequest(ctx, body, token, tokenType, modelID, mimicClaudeCode, o)
}

// countTokensError 返回 count_tokens 错误响应
func (s *GatewayService) countTokensError(c *gin.Context, status int, errType, message string) {
	gatewayhttp.WriteForwardCountError(c, status, errType, message)
}
