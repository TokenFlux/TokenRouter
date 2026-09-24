package messageforward

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"

	"bytes"
	"context"
	"encoding/json"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
	"github.com/TokenFlux/TokenRouter/internal/upstream"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"go.uber.org/zap"
)

// conversionAttempt 仅保存本次账号的受控凭据和网络句柄，不持有转换状态。
type conversionAttempt struct {
	s                          *Runtime
	c                          HTTPBoundary
	account                    *gatewayprovider.ExecutionAccount
	responses                  bool
	state                      *AttemptState
	token, tokenType, proxyURL string
	request                    *http.Request
	response                   *http.Response
}

func (a *conversionAttempt) NormalizeResponses(body []byte) ([]byte, bool, error) {
	return gatewayprovider.NormalizeOpenAIResponsesLegacyIngress(body)
}
func (a *conversionAttempt) ResolveModel(ctx context.Context, model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).UpstreamModel(ctx, model)
}
func (a *conversionAttempt) Effort(body []byte, chat bool, models ...string) *string {
	return forwardcore.ExtractEffort(body, chat, capability.NormalizeRecordedOpenAIEffortForModel, models...)
}
func (a *conversionAttempt) ThinkingFallback(effort *string, body []byte, model string) *string {
	return gatewayprovider.ApplyThinkingEnabledFallback(effort, body, model)
}
func (a *conversionAttempt) ModelNotice(message, original, mapped string, stream bool) {
	logging.L().Debug(message, zap.Int64("account_id", a.account.Record.ID), zap.String("original_model", original), zap.String("mapped_model", mapped), zap.Bool("client_stream", stream))
}
func (a *conversionAttempt) Mimic(ctx context.Context, body []byte, system json.RawMessage, model string) []byte {
	return a.s.mimic(ctx, a.c, a.state, a.account, body, system, model)
}
func (a *conversionAttempt) CacheLimit(body []byte) []byte {
	return anthropic.EnforceCacheControlLimit(body)
}
func (a *conversionAttempt) Credential(ctx context.Context) error {
	token, kind, err := a.s.dependencies.Credentials.Resolve(ctx, gatewayprovider.ExecutionRecord(a.account))
	if err == nil {
		a.token, a.tokenType = token, kind
	}
	return err
}
func (a *conversionAttempt) Build(ctx context.Context, body []byte, model string, stream, mimic bool) ([]byte, error) {
	if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil {
		a.proxyURL = a.account.Record.Proxy.URL()
	}
	upstreamCtx, release := detachedStreamContext(ctx, stream)
	request, wire, err := a.s.buildRequest(upstreamCtx, a.c, a.state, a.account, body, a.token, a.tokenType, model, stream, mimic)
	release()
	a.request = request
	return wire, err
}
func (a *conversionAttempt) Send(ctx context.Context) (forwardcore.Response, error) {
	resp, err := a.s.dependencies.Transport.DoWithTLS(a.request, a.proxyURL, a.account.Record.ID, a.account.Record.Concurrency, a.s.requestTLS(a.account))
	if err != nil {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return forwardcore.Response{}, a.s.transportError(ctx, a.c, a.account, err, forwardcore.Notice{UpstreamURL: logredact.SafeUpstreamURL(a.request.URL.String())})
	}
	a.response = resp
	return a.s.forwardResponse(resp), nil
}
func (a *conversionAttempt) ReadErrorBody() ([]byte, error) {
	body, err := a.s.readErrorBody(a.response)
	_ = a.response.Body.Close()
	a.response.Body = io.NopCloser(bytes.NewReader(body))
	return body, err
}
func (a *conversionAttempt) ErrorMessage(body []byte) string {
	return logredact.SanitizeUpstreamQueries(strings.TrimSpace(upstream.ExtractErrorMessage(body)))
}
func (a *conversionAttempt) Health(ctx context.Context, status int, body []byte, model string) forwardcore.ErrorDecision {
	decision := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.account), status)
	if a.s.dependencies.Health != nil {
		decision = gatewayprovider.ApplyExecutionHealth(ctx, a.s.dependencies.Health, a.account, gatewayprovider.HealthObservationFromContext(ctx, status, a.response.Header, body, []string{model}))
	}
	return forwardcore.ErrorDecision{
		Generic:          decision.ShouldReturnGenericError(),
		Failover:         decision.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.account), status, forwardcore.ShouldFailover(status)),
		RetrySameAccount: decision.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status),
	}
}
func (a *conversionAttempt) FailoverNotice(status int, message string) {
	a.c.Observe(forwardcore.Notice{
		Platform: a.account.Record.Platform, AccountID: a.account.Record.ID, AccountName: a.account.Record.Name, UpstreamStatusCode: status, UpstreamRequestID: a.response.Header.Get("x-request-id"), Kind: "failover", Message: message,
	})
}
func (a *conversionAttempt) FailoverError(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *conversionAttempt) Output() forwardcore.Output {
	return a.c.ConversionOutput(a.responses, a.state)
}
