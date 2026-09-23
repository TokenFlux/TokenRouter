package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// conversionExecutionAdapter 仅保存本次账号的受控凭据和网络句柄，不持有转换状态。
type conversionExecutionAdapter struct {
	s                          *GatewayService
	c                          *gin.Context
	account                    *gatewayprovider.ExecutionAccount
	responses                  bool
	token, tokenType, proxyURL string
	request                    *http.Request
	response                   *http.Response
}

func (a *conversionExecutionAdapter) NormalizeResponses(body []byte) ([]byte, bool, error) {
	return gatewayprovider.NormalizeOpenAIResponsesLegacyIngress(body)
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
	return gatewayprovider.ApplyThinkingEnabledFallback(effort, body, model)
}
func (a *conversionExecutionAdapter) ModelNotice(message, original, mapped string, stream bool) {
	logging.L().Debug(message, zap.Int64("account_id", a.account.Record.ID), zap.String("original_model", original), zap.String("mapped_model", mapped), zap.Bool("client_stream", stream))
}
func (a *conversionExecutionAdapter) Mimic(ctx context.Context, body []byte, system json.RawMessage, model string) []byte {
	return a.s.applyClaudeCodeOAuthMimicryToBody(ctx, a.c, a.account, body, system, model)
}
func (a *conversionExecutionAdapter) CacheLimit(body []byte) []byte {
	return anthropic.EnforceCacheControlLimit(body)
}
func (a *conversionExecutionAdapter) Credential(ctx context.Context) error {
	token, kind, err := a.s.messageCredentials.Resolve(ctx, gatewayprovider.ExecutionRecord(a.account))
	if err == nil {
		a.token, a.tokenType = token, kind
	}
	return err
}
func (a *conversionExecutionAdapter) Build(ctx context.Context, body []byte, model string, stream, mimic bool) ([]byte, error) {
	if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil {
		a.proxyURL = a.account.Record.Proxy.URL()
	}
	upstreamCtx, release := detachStreamUpstreamContext(ctx, stream)
	request, wire, err := a.s.buildUpstreamRequest(upstreamCtx, a.c, a.account, body, a.token, a.tokenType, model, stream, mimic)
	release()
	a.request = request
	return wire, err
}
func (a *conversionExecutionAdapter) Send(ctx context.Context) (forwardcore.Response, error) {
	resp, err := a.s.httpUpstream.DoWithTLS(a.request, a.proxyURL, a.account.Record.ID, a.account.Record.Concurrency, a.s.tlsFPProfileService.ResolveRequestTLS(accountTLSSelection(a.account, nil)))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return forwardcore.Response{}, a.s.handleUpstreamTransportError(ctx, a.c, a.account, err, ops.OpsUpstreamErrorEvent{UpstreamURL: logredact.SafeUpstreamURL(a.request.URL.String())})
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
	return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
}
func (a *conversionExecutionAdapter) Health(ctx context.Context, status int, body []byte, model string) forwardcore.ErrorDecision {
	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.account), status)
	if a.s.rateLimitService != nil {
		decision = gatewayprovider.ApplyExecutionHealth(ctx, a.s.rateLimitService.UpstreamHealth(), a.account, gatewayprovider.HealthObservationFromContext(ctx, status, a.response.Header, body, []string{model}))
	}
	return forwardcore.ErrorDecision{
		Generic:          decision.ShouldReturnGenericError(),
		Failover:         decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.account), status, a.s.shouldFailoverUpstreamError(status)),
		RetrySameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status),
	}
}
func (a *conversionExecutionAdapter) FailoverNotice(status int, message string) {
	gatewayhttp.AppendOpsUpstreamError(a.c, ops.OpsUpstreamErrorEvent{
		Platform: a.account.Record.Platform, AccountID: a.account.Record.ID, AccountName: a.account.Record.Name, UpstreamStatusCode: status, UpstreamRequestID: a.response.Header.Get("x-request-id"), Kind: "failover", Message: message,
	})
}
func (a *conversionExecutionAdapter) FailoverError(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *conversionExecutionAdapter) Output() forwardcore.Output {
	return a.s.forwardOutput(a.c, a.responses)
}
