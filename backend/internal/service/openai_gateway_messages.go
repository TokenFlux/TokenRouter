package service

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/protocol/bridge"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	forward "github.com/TokenFlux/TokenRouter/internal/gateway/provider/openaiforward"
	"github.com/TokenFlux/TokenRouter/internal/upstream"

	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"

	protocolopenai "github.com/TokenFlux/TokenRouter/internal/protocol/openai"
	"github.com/gin-gonic/gin"
)

// ForwardAsAnthropic 只保留旧签名，Messages 请求和恢复编排由目标执行器唯一拥有。
func (s *OpenAIGatewayService) ForwardAsAnthropic(ctx context.Context, c *gin.Context, account *gatewayprovider.ExecutionAccount, body []byte, promptCacheKey, defaultMappedModel string, tlsRouterMatch ...egress.TLSFingerprintRouterMatchResult) (*forwardcore.OpenAIResult, error) {
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
	account *gatewayprovider.ExecutionAccount,
	requestedModel ...string,
) (*forwardcore.OpenAIResult, error) {
	return s.handleCompatErrorResponse(resp, c, account, gatewayhttp.WriteForwardAnthropicError, gatewayhttp.WriteForwardAnthropicErrorBody, requestedModel...)
}

func (s *OpenAIGatewayService) handleAnthropicBufferedStreamingResponse(
	resp *http.Response,
	c *gin.Context,
	account *gatewayprovider.ExecutionAccount,
	originalModel string,
	billingModel string,
	upstreamModel string,
	startTime time.Time,
) (*forwardcore.OpenAIResult, error) {
	result, err := openai.ReadMessagesBuffered(resp, upstream.NewDeferredOutputContext(gatewayhttp.ResponseSink{Writer: c.Writer}), s.nativeMessagesResponseOptions(c, account, resp, originalModel, billingModel, upstreamModel), originalModel, upstreamModel, startTime)
	return chatForwardResult(result, billingModel), err
}

func (s *OpenAIGatewayService) recordOpenAIMessagesStreamUpstreamError(c *gin.Context, account *gatewayprovider.ExecutionAccount, upstreamRequestID, kind, message string) {
	if c == nil {
		return
	}
	message = logredact.SanitizeUpstreamQueries(message)
	gatewayhttp.SetOpsUpstreamError(c, http.StatusBadGateway, message, "")
	event := ops.OpsUpstreamErrorEvent{
		Platform:           capability.PlatformOpenAI,
		UpstreamStatusCode: http.StatusBadGateway,
		UpstreamRequestID:  strings.TrimSpace(upstreamRequestID),
		Kind:               kind,
		Message:            message,
	}
	if account != nil {
		event.Platform = account.Record.Platform
		event.AccountID = account.Record.ID
		event.AccountName = account.Record.Name
	}
	gatewayhttp.AppendOpsUpstreamError(c, event)
}

func (s *OpenAIGatewayService) readOpenAICompatBufferedTerminal(
	resp *http.Response,
	c *gin.Context,
	logPrefix string,
	requestID string,
) (*protocolopenai.ResponsesResponse, protocolopenai.ForwardUsage, *bridge.BufferedResponseAccumulator, error) {
	return openai.ReadCompatBufferedTerminal(resp, s.nativeCompatBufferedOptions(c, logPrefix, requestID))
}
