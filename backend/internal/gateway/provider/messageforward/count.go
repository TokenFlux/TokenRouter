package messageforward

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/clientmeta"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	"context"
	"errors"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/requeststate"
)

// countAttempt 复用请求准备端口，只额外保存本次计数请求句柄。
type countAttempt struct {
	*attempt
	request    *http.Request
	proxyReady bool
}

func (a *countAttempt) ResolveModel(ctx context.Context, model string) string {
	return gatewayprovider.ExecutionModelPolicy(a.account).UpstreamModel(ctx, model)
}
func (a *countAttempt) IsCountClaudeCode(ctx context.Context, metadata string) bool {
	return requeststate.IsClaudeCodeClient(ctx) || clientmeta.IsClaudeCodeClient(a.c.RequestHeaders().Get("User-Agent"), metadata)
}
func (a *countAttempt) TokenKind() string { return a.tokenType }
func (a *countAttempt) BuildCount(ctx context.Context, body []byte, model string, mimic, passthrough bool) ([]byte, error) {
	request, wire, err := a.s.buildCountRequest(ctx, a.c, a.state, a.account, body, a.token, a.tokenType, model, mimic, passthrough)
	a.request = request
	return wire, err
}
func (a *countAttempt) SendCount(ctx context.Context, passthrough bool) (*forwardcore.ExchangeResponse, error) {
	// 代理只在首发前解析；签名重试保留原快照，TLS Profile 仍逐次读取。
	if !a.proxyReady {
		a.proxyReady = true
		if a.account.Record.ProxyID != nil && a.account.Record.Proxy != nil && (passthrough || !a.account.View().IsCustomBaseURLEnabled() || a.account.View().GetCustomBaseURL() == "") {
			a.proxyURL = a.account.Record.Proxy.URL()
		}
	}
	resp, err := a.s.dependencies.Transport.DoWithTLS(a.request, a.proxyURL, a.account.Record.ID, a.account.Record.Concurrency, a.s.requestTLS(a.account))
	if err != nil {
		return nil, err
	}
	a.response = resp
	return &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")}, nil
}
func (a *countAttempt) ReadCount() ([]byte, error) {
	body, err := a.c.ReadResponseBody(a.response.Body, a.s.options.ResponseReadLimit, CountBody)
	_ = a.response.Body.Close()
	return body, err
}
func (a *countAttempt) IsTooLarge(err error) bool {
	return errors.Is(err, httpclient.ErrResponseBodyTooLarge)
}
func (a *countAttempt) RectifyCount(ctx context.Context, body []byte, model string) bool {
	return a.s.shouldRectify(ctx, a.account, body, model)
}
func (a *countAttempt) FilterCountRetry(body []byte, model string) []byte {
	return gatewayprovider.FilterThinkingBlocksForRetry(body, model)
}
func (a *countAttempt) CountError(status int, kind, message string) {
	a.c.CountError(status, kind, message)
}
func (a *countAttempt) CountSuccess(status int, headers map[string][]string, body []byte, passthrough bool) {
	a.c.CountSuccess(status, headers, body, passthrough)
}
func (a *countAttempt) SetError(status int, message, detail string) {
	a.c.SetError(status, message, detail)
}
func (a *countAttempt) UnsupportedCount(status int, body []byte) bool {
	return isCountTokensUnsupported404(status, body)
}
func (a *countAttempt) CountHealth(ctx context.Context, status int, headers map[string][]string, body []byte, model string) forwardcore.ErrorDecision {
	d := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.account), status)
	if a.s.dependencies.Health != nil {
		d = gatewayprovider.ApplyExecutionHealth(ctx, a.s.dependencies.Health, a.account, gatewayprovider.HealthObservationFromContext(ctx, status, headers, body, []string{model}))
	}
	return forwardcore.ErrorDecision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(gatewayprovider.ExecutionErrorPolicy(a.account), status, forwardcore.ShouldFailover(status)),
		RetrySameAccount: d.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status),
	}
}
func (a *countAttempt) CountFailover(status int, headers map[string][]string, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: http.Header(headers).Clone(), RetryableOnSameAccount: retry}
}
func (a *countAttempt) CountURL() string {
	return logredact.SafeUpstreamURL(a.request.URL.String())
}
