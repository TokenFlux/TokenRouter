package service

import (
	"context"
	"errors"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

// countExecutionAdapter 复用请求准备端口，只额外保存本次计数请求句柄。
type countExecutionAdapter struct {
	*messageExecutionAdapter
	request    *http.Request
	proxyReady bool
}

func (a *countExecutionAdapter) ResolveModel(ctx context.Context, model string) string {
	return resolveAccountUpstreamModel(ctx, a.account, model)
}
func (a *countExecutionAdapter) IsCountClaudeCode(ctx context.Context, metadata string) bool {
	return IsClaudeCodeClient(ctx) || isClaudeCodeClient(a.c.GetHeader("User-Agent"), metadata)
}
func (a *countExecutionAdapter) TokenKind() string { return a.tokenType }
func (a *countExecutionAdapter) BuildCount(ctx context.Context, body []byte, model string, mimic, passthrough bool) ([]byte, error) {
	if passthrough {
		r, e := a.s.buildCountTokensRequestAnthropicAPIKeyPassthrough(ctx, a.c, a.account, body, a.token)
		a.request = r
		return nil, e
	}
	r, wire, e := a.s.buildCountTokensRequest(ctx, a.c, a.account, body, a.token, a.tokenType, model, mimic)
	a.request = r
	return wire, e
}
func (a *countExecutionAdapter) SendCount(ctx context.Context, passthrough bool) (*forwardcore.ExchangeResponse, error) {
	// 代理只在首发前解析；签名重试保留原快照，TLS Profile 仍逐次读取。
	if !a.proxyReady {
		a.proxyReady = true
		if a.account.ProxyID != nil && a.account.Proxy != nil && (passthrough || !a.account.IsCustomBaseURLEnabled() || a.account.GetCustomBaseURL() == "") {
			a.proxyURL = a.account.Proxy.URL()
		}
	}
	resp, err := a.s.httpUpstream.DoWithTLS(a.request, a.proxyURL, a.account.ID, a.account.Concurrency, a.s.tlsFPProfileService.ResolveTLSProfile(a.account))
	if err != nil {
		return nil, err
	}
	a.response = resp
	return &forwardcore.ExchangeResponse{StatusCode: resp.StatusCode, Headers: resp.Header, RequestID: resp.Header.Get("x-request-id")}, nil
}
func (a *countExecutionAdapter) ReadCount() ([]byte, error) {
	body, err := ReadUpstreamResponseBody(a.response.Body, a.s.cfg, a.c, func(c *gin.Context) {
		a.s.countTokensError(c, http.StatusBadGateway, "upstream_error", "Upstream response too large")
	})
	_ = a.response.Body.Close()
	return body, err
}
func (a *countExecutionAdapter) IsTooLarge(err error) bool {
	return errors.Is(err, ErrUpstreamResponseBodyTooLarge)
}
func (a *countExecutionAdapter) RectifyCount(ctx context.Context, body []byte, model string) bool {
	return a.s.shouldRectifySignatureError(ctx, a.account, body, model)
}
func (a *countExecutionAdapter) FilterCountRetry(body []byte, model string) []byte {
	return FilterThinkingBlocksForRetry(body, model)
}
func (a *countExecutionAdapter) CountError(status int, kind, message string) {
	a.s.countTokensError(a.c, status, kind, message)
}
func (a *countExecutionAdapter) CountSuccess(status int, headers map[string][]string, body []byte, passthrough bool) {
	if passthrough {
		writeAnthropicPassthroughResponseHeaders(a.c.Writer.Header(), headers, a.s.responseHeaderFilter)
	}
	gatewayhttp.WriteForwardCountSuccess(a.c, status, headers, body, passthrough)
}
func (a *countExecutionAdapter) SetError(status int, message, detail string) {
	setOpsUpstreamError(a.c, status, message, detail)
}
func (a *countExecutionAdapter) UnsupportedCount(status int, body []byte) bool {
	return isCountTokensUnsupported404(status, body)
}
func (a *countExecutionAdapter) CountHealth(ctx context.Context, status int, headers map[string][]string, body []byte, model string) forwardcore.ErrorDecision {
	d := upstreamErrorDecisionWithoutPersistence(a.account, status)
	if a.s.rateLimitService != nil {
		d = a.s.rateLimitService.ApplyUpstreamError(ctx, a.account, status, headers, body, model)
	}
	return forwardcore.ErrorDecision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(a.account, status, forwardcore.ShouldFailover(status)),
		RetrySameAccount: d.RetryableOnSameAccount(a.account, status),
	}
}
func (a *countExecutionAdapter) CountFailover(status int, headers map[string][]string, body []byte, retry bool) error {
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: http.Header(headers).Clone(), RetryableOnSameAccount: retry}
}
func (a *countExecutionAdapter) CountURL() string { return safeUpstreamURL(a.request.URL.String()) }
