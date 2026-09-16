package service

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// conversionExecutionAdapter 仅保存本次账号的受控凭据和网络句柄，不持有转换状态。
type conversionExecutionAdapter struct {
	s                          *GatewayService
	c                          *gin.Context
	account                    *Account
	responses                  bool
	token, tokenType, proxyURL string
	request                    *http.Request
	response                   *http.Response
}

func (a *conversionExecutionAdapter) NormalizeResponses(body []byte) ([]byte, bool, error) {
	return normalizeOpenAIResponsesLegacyIngress(body)
}
func (a *conversionExecutionAdapter) ResolveModel(ctx context.Context, model string) string {
	return resolveAccountUpstreamModel(ctx, a.account, model)
}
func (a *conversionExecutionAdapter) Effort(body []byte, chat bool, models ...string) *string {
	if chat {
		return extractCCReasoningEffortFromBody(body, models...)
	}
	return ExtractResponsesReasoningEffortFromBody(body, models...)
}
func (a *conversionExecutionAdapter) ThinkingFallback(effort *string, body []byte, model string) *string {
	return ApplyThinkingEnabledFallback(effort, body, model)
}
func (a *conversionExecutionAdapter) ModelNotice(message, original, mapped string, stream bool) {
	logger.L().Debug(message, zap.Int64("account_id", a.account.ID), zap.String("original_model", original), zap.String("mapped_model", mapped), zap.Bool("client_stream", stream))
}
func (a *conversionExecutionAdapter) Mimic(ctx context.Context, body []byte, system json.RawMessage, model string) []byte {
	return a.s.applyClaudeCodeOAuthMimicryToBody(ctx, a.c, a.account, body, system, model)
}
func (a *conversionExecutionAdapter) CacheLimit(body []byte) []byte {
	return enforceCacheControlLimit(body)
}
func (a *conversionExecutionAdapter) Credential(ctx context.Context) error {
	token, kind, err := a.s.GetAccessToken(ctx, a.account)
	if err == nil {
		a.token, a.tokenType = token, kind
	}
	return err
}
func (a *conversionExecutionAdapter) Build(ctx context.Context, body []byte, model string, stream, mimic bool) ([]byte, error) {
	if a.account.ProxyID != nil && a.account.Proxy != nil {
		a.proxyURL = a.account.Proxy.URL()
	}
	upstreamCtx, release := detachStreamUpstreamContext(ctx, stream)
	request, wire, err := a.s.buildUpstreamRequest(upstreamCtx, a.c, a.account, body, a.token, a.tokenType, model, stream, mimic)
	release()
	a.request = request
	return wire, err
}
func (a *conversionExecutionAdapter) Send(ctx context.Context) (forwardcore.Response, error) {
	resp, err := a.s.httpUpstream.DoWithTLS(a.request, a.proxyURL, a.account.ID, a.account.Concurrency, a.s.tlsFPProfileService.ResolveTLSProfile(a.account))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return forwardcore.Response{}, a.s.handleUpstreamTransportError(ctx, a.c, a.account, err, OpsUpstreamErrorEvent{UpstreamURL: safeUpstreamURL(a.request.URL.String())})
	}
	a.response = resp
	return a.s.forwardResponse(resp), nil
}
func (a *conversionExecutionAdapter) ReadErrorBody() ([]byte, error) {
	body, err := a.s.readUpstreamErrorBody(a.response)
	_ = a.response.Body.Close()
	a.response.Body = io.NopCloser(bytes.NewReader(body))
	return body, err
}
func (a *conversionExecutionAdapter) ErrorMessage(body []byte) string {
	return sanitizeUpstreamErrorMessage(strings.TrimSpace(extractUpstreamErrorMessage(body)))
}
func (a *conversionExecutionAdapter) Health(ctx context.Context, status int, body []byte, model string) forwardcore.ErrorDecision {
	decision := upstreamErrorDecisionWithoutPersistence(a.account, status)
	if a.s.rateLimitService != nil {
		decision = a.s.rateLimitService.ApplyUpstreamError(ctx, a.account, status, a.response.Header, body, model)
	}
	return forwardcore.ErrorDecision{
		Generic:          decision.ShouldReturnGenericError(),
		Failover:         decision.ShouldFailover(a.account, status, a.s.shouldFailoverUpstreamError(status)),
		RetrySameAccount: decision.RetryableOnSameAccount(a.account, status),
	}
}
func (a *conversionExecutionAdapter) FailoverNotice(status int, message string) {
	appendOpsUpstreamError(a.c, OpsUpstreamErrorEvent{
		Platform: a.account.Platform, AccountID: a.account.ID, AccountName: a.account.Name, UpstreamStatusCode: status, UpstreamRequestID: a.response.Header.Get("x-request-id"), Kind: "failover", Message: message,
	})
}
func (a *conversionExecutionAdapter) FailoverError(status int, body []byte, retry bool) error {
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *conversionExecutionAdapter) Output() forwardcore.Output {
	return a.s.forwardOutput(a.c, a.responses)
}
