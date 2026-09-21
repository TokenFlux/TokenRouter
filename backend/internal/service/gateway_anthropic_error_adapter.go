package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"

	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/gin-gonic/gin"
)

// anthropicErrorAdapter 只持有本次响应和单步能力，不执行第二套错误编排。
type anthropicErrorAdapter struct {
	s       *GatewayService
	c       *gin.Context
	account *Account
	resp    *http.Response
	body    []byte
}

func (a *anthropicErrorAdapter) input(models []string) forwardcore.ErrorInput {
	in := forwardcore.ErrorInput{
		AccountID:       a.account.ID,
		AccountName:     a.account.Name,
		AccountType:     a.account.Type,
		Platform:        a.account.Platform,
		RequestID:       a.resp.Header.Get("x-request-id"),
		Status:          a.resp.StatusCode,
		RequestedModels: models,
	}
	if a.s.cfg != nil {
		in.LogBody = a.s.cfg.Gateway.LogUpstreamErrorBody
		in.LogBodyMaxBytes = a.s.cfg.Gateway.LogUpstreamErrorBodyMaxBytes
	}
	return in
}
func (a *anthropicErrorAdapter) ScheduleActivity() {
	scheduleOllamaCloudUsageActivity(a.s.deferredService, a.account)
}
func (a *anthropicErrorAdapter) ReadBody() ([]byte, error) {
	body, err := a.s.readUpstreamErrorBody(a.resp)
	a.body = body
	return body, err
}
func (a *anthropicErrorAdapter) ResetBody(body []byte) {
	_ = a.resp.Body.Close()
	a.resp.Body = io.NopCloser(bytes.NewReader(body))
}
func (a *anthropicErrorAdapter) Health(ctx context.Context, status int, models []string) forwardcore.ErrorDecision {
	d := upstreamErrorDecisionWithoutPersistence(a.account, status)
	if a.s.rateLimitService != nil {
		if len(models) > 0 {
			d = a.s.rateLimitService.ApplyUpstreamError(ctx, a.account, status, a.resp.Header, a.body, models[0])
		} else {
			d = a.s.rateLimitService.ApplyUpstreamError(ctx, a.account, status, a.resp.Header, a.body)
		}
	}
	return a.decision(d, status)
}
func (a *anthropicErrorAdapter) RetryHealth(ctx context.Context, models []string) forwardcore.ErrorDecision {
	return a.decision(a.s.handleRetryExhaustedSideEffects(ctx, a.resp, a.account, models...), a.resp.StatusCode)
}
func (a *anthropicErrorAdapter) decision(d UpstreamErrorDecision, status int) forwardcore.ErrorDecision {
	return forwardcore.ErrorDecision{
		Generic:          d.ShouldReturnGenericError(),
		Failover:         d.ShouldFailover(a.account, status, false),
		RetrySameAccount: d.RetryableOnSameAccount(a.account, status),
	}
}
func (a *anthropicErrorAdapter) Failover(status int, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (a *anthropicErrorAdapter) Commit() { gatewayhttp.MarkResponseCommitted(a.c) }
func (a *anthropicErrorAdapter) Message(status int, kind, message string) {
	(gatewayhttp.AnthropicForwardErrorOutput{Context: a.c}).Message(status, kind, message)
}
func (a *anthropicErrorAdapter) Raw(status int, body []byte) {
	(gatewayhttp.AnthropicForwardErrorOutput{Context: a.c}).Raw(status, body)
}
func (a *anthropicErrorAdapter) MatchRule(platform string, status int, body []byte) *errorpolicy.ErrorPassthroughRule {
	rules := gatewayhttp.BoundErrorPassthroughService(a.c)
	if rules == nil {
		return nil
	}
	return rules.MatchRule(platform, status, body)
}
func (a *anthropicErrorAdapter) SkipMonitoring() { a.c.Set(gatewayhttp.OpsSkipPassthroughKey, true) }
func (a *anthropicErrorAdapter) ScopeDiagnostic(message string, status int, requestID string) {
	if isClaudeCodeCredentialScopeError(message) && a.c != nil {
		if v, ok := a.c.Get(claudeMimicDebugInfoKey); ok {
			if line, ok := v.(string); ok && strings.TrimSpace(line) != "" {
				logging.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s", status, requestID, line)
			}
		}
	}
}
func (a *anthropicErrorAdapter) SetError(status int, message, detail string) {
	gatewayhttp.SetOpsUpstreamError(a.c, status, message, detail)
}
func (a *anthropicErrorAdapter) Observe(n forwardcore.Notice) {
	gatewayhttp.AppendOpsUpstreamError(a.c, ops.OpsUpstreamErrorEvent{
		Platform:           n.Platform,
		AccountID:          n.AccountID,
		UpstreamStatusCode: n.UpstreamStatusCode,
		UpstreamRequestID:  n.UpstreamRequestID,
		Kind:               n.Kind,
		Message:            n.Message,
		Detail:             n.Detail,
	})
}
func (a *anthropicErrorAdapter) Log(message string) {
	logging.LegacyPrintf("service.gateway", "%s", message)
}
func (a *anthropicErrorAdapter) Truncate(value string, n int) string {
	return logredact.TruncateUTF8(value, n)
}
func (a *anthropicErrorAdapter) TruncateBytes(body []byte, n int) string {
	return truncateForLog(body, n)
}
func (a *anthropicErrorAdapter) Sanitize(value string) string {
	return logredact.SanitizeUpstreamQueries(value)
}
