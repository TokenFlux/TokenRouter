package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	nativeopenai "github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	"github.com/TokenFlux/TokenRouter/internal/pkg/apicompat"
	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"

	"github.com/gin-gonic/gin"
)

// ForwardAsAnthropic 只保留旧签名，Messages 请求和恢复编排由目标执行器唯一拥有。
func (s *OpenAIGatewayService) ForwardAsAnthropic(ctx context.Context, c *gin.Context, account *Account, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...TLSFingerprintRouterMatchResult) (*OpenAIForwardResult, error) {
	result, err := forward.RunMessages(ctx, body, promptCacheKey, defaultMappedModel, &openAIMessagesExecutionAdapter{s: s, c: c, account: account, tls: tlsRouterMatch})
	return openAIForwardResultFromHTTP(result), err
}

func ensureCodexOAuthInstructionsField(reqBody map[string]any) {
	if reqBody == nil {
		return
	}
	if value, ok := reqBody["instructions"]; !ok || value == nil {
		reqBody["instructions"] = ""
		return
	}
	if _, ok := reqBody["instructions"].(string); !ok {
		reqBody["instructions"] = ""
	}
}

// handleAnthropicErrorResponse reads an upstream error and returns it in
// Anthropic error format.
func (s *OpenAIGatewayService) handleAnthropicErrorResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	requestedModel ...string,
) (*OpenAIForwardResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, writeAnthropicError, writeAnthropicErrorBody, requestedModel...)
}

func (s *OpenAIGatewayService) handleAnthropicBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *Account,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*OpenAIForwardResult, error) {
	result, err := nativeopenai.ReadMessagesBuffered(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeMessagesResponseOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime)
	return chatForwardResult(result, billingModel), err
}

func (s *OpenAIGatewayService) recordOpenAIMessagesStreamUpstreamError(c *gin.Context, account *Account, upstreamRequestID, kind, message string) {
	if c == nil {
		return
	}
	message = sanitizeUpstreamErrorMessage(message)
	setOpsUpstreamError(c, http.StatusBadGateway, message, "")
	event := OpsUpstreamErrorEvent{
		Platform:           PlatformOpenAI,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	}
	if account != nil {
		event.Platform = account.Platform
		event.AccountID = account.ID
		event.AccountName = account.Name
	}
	appendOpsUpstreamError(c, event)
}

type openAICompatBufferedReadError = nativeopenai.CompatBufferedReadError

func (s *OpenAIGatewayService) readOpenAICompatBufferedTerminal(
	resp *http.Response,
	c *gin.Context,
	logPrefix string,
	requestID string,
) (*protocolopenai.ResponsesResponse, OpenAIUsage, *apicompat.BufferedResponseAccumulator, error) {
	return nativeopenai.ReadCompatBufferedTerminal(resp, s.nativeCompatBufferedOptions(c, logPrefix, requestID))
}

// writeAnthropicError 委托 HTTP Adapter，保留旧调用入口。
func writeAnthropicError(c *gin.Context, statusCode int, errType, message string) {
	gatewayhttp.WriteForwardAnthropicError(c, statusCode, errType, message)
}

// writeAnthropicErrorBody 委托 HTTP Adapter，保留旧调用入口。
func writeAnthropicErrorBody(c *gin.Context, statusCode int, body []byte) {
	gatewayhttp.WriteForwardAnthropicErrorBody(c, statusCode, body)
}

// buildAnthropicStreamErrorSSE 委托 HTTP Adapter，保留旧调用入口。
func buildAnthropicStreamErrorSSE(errType, message string) string {
	return gatewayhttp.BuildForwardAnthropicStreamError(errType, message)
}
