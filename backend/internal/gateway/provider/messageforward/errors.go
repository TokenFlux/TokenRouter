package messageforward

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/gateway/errorpolicy"
	"github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logredact"
)

// errorExchange 只连接本次响应、健康命令与 HTTP 输出，错误流程由 forward 执行。
type errorExchange struct {
	runtime  *Runtime
	output   HTTPBoundary
	state    *AttemptState
	target   *provider.ExecutionAccount
	response *http.Response
	body     []byte
}

func (r *Runtime) handleError(ctx context.Context, output HTTPBoundary, state *AttemptState, target *provider.ExecutionAccount, response *http.Response, retry bool, models ...string) (*forward.Result, error) {
	ports := &errorExchange{runtime: r, output: output, state: state, target: target, response: response}
	input := forward.ErrorInput{
		AccountID: target.Record.ID, AccountName: target.Record.Name, AccountType: target.Record.Type,
		Platform: target.Record.Platform, RequestID: response.Header.Get("x-request-id"), Status: response.StatusCode,
		RequestedModels: models, LogBody: r.options.LogErrorBody, LogBodyMaxBytes: r.options.LogErrorBodyMaxBytes,
	}
	if retry {
		return forward.AnthropicRetryError(ctx, ports, input)
	}
	return forward.AnthropicError(ctx, ports, input)
}

func (e *errorExchange) ScheduleActivity() { e.runtime.scheduleActivity(e.target) }
func (e *errorExchange) ReadBody() ([]byte, error) {
	body, err := e.runtime.readErrorBody(e.response)
	e.body = body
	return body, err
}
func (e *errorExchange) ResetBody(body []byte) {
	_ = e.response.Body.Close()
	e.response.Body = io.NopCloser(bytes.NewReader(body))
}
func (e *errorExchange) Health(ctx context.Context, status int, models []string) forward.ErrorDecision {
	decision := account.ErrorDecisionWithoutPersistence(provider.ExecutionErrorPolicy(e.target), status)
	if e.runtime.dependencies.Health != nil {
		if len(models) > 0 {
			models = models[:1]
		} else {
			models = nil
		}
		decision = provider.ApplyExecutionHealth(ctx, e.runtime.dependencies.Health, e.target, provider.HealthObservationFromContext(ctx, status, e.response.Header, e.body, models))
	}
	return e.decision(decision, status)
}
func (e *errorExchange) RetryHealth(ctx context.Context, models []string) forward.ErrorDecision {
	return e.decision(e.runtime.retryHealth(ctx, e.response, e.target, models...), e.response.StatusCode)
}
func (e *errorExchange) decision(decision account.UpstreamErrorDecision, status int) forward.ErrorDecision {
	policy := provider.ExecutionErrorPolicy(e.target)
	return forward.ErrorDecision{Generic: decision.ShouldReturnGenericError(), Failover: decision.ShouldFailover(policy, status, false), RetrySameAccount: decision.RetryableOnSameAccount(policy, status)}
}
func (*errorExchange) Failover(status int, body []byte, retry bool) error {
	return &forward.UpstreamFailoverError{StatusCode: status, ResponseBody: body, RetryableOnSameAccount: retry}
}
func (e *errorExchange) Commit() { e.output.Commit() }
func (e *errorExchange) Message(status int, kind, message string) {
	e.output.MessageError(status, kind, message)
}
func (e *errorExchange) Raw(status int, body []byte) { e.output.RawError(status, body) }
func (e *errorExchange) MatchRule(platform string, status int, body []byte) *errorpolicy.ErrorPassthroughRule {
	return e.output.MatchRule(platform, status, body)
}
func (e *errorExchange) SkipMonitoring() { e.output.SkipMonitoring() }
func (e *errorExchange) ScopeDiagnostic(message string, status int, requestID string) {
	if claudeScopeError(message) && e.output.Present() && strings.TrimSpace(e.state.MimicDebug) != "" {
		logging.LegacyPrintf("service.gateway", "[ClaudeMimicDebugOnError] status=%d request_id=%s %s", status, requestID, e.state.MimicDebug)
	}
}
func (e *errorExchange) SetError(status int, message, detail string) {
	e.output.SetError(status, message, detail)
}
func (e *errorExchange) Observe(notice forward.Notice) {
	// 此错误分支沿用原精简事件，不顺便补充其他分支才有的字段。
	e.output.Observe(forward.Notice{Platform: notice.Platform, AccountID: notice.AccountID,
		UpstreamStatusCode: notice.UpstreamStatusCode, UpstreamRequestID: notice.UpstreamRequestID,
		Kind: notice.Kind, Message: notice.Message, Detail: notice.Detail})
}
func (*errorExchange) Log(message string) { logging.LegacyPrintf("service.gateway", "%s", message) }
func (*errorExchange) Truncate(value string, limit int) string {
	return logredact.TruncateUTF8(value, limit)
}
func (*errorExchange) TruncateBytes(body []byte, limit int) string {
	return logredact.TruncateLine(body, limit)
}
func (*errorExchange) Sanitize(value string) string { return logredact.SanitizeUpstreamQueries(value) }

func (r *Runtime) retryHealth(ctx context.Context, response *http.Response, target *provider.ExecutionAccount, models ...string) account.UpstreamErrorDecision {
	body, _ := r.readErrorBody(response)
	status := response.StatusCode
	if r.dependencies.Health == nil {
		return account.ErrorDecisionWithoutPersistence(provider.ExecutionErrorPolicy(target), status)
	}
	policy := r.dependencies.Health.ApplyExplicitErrorPolicy(ctx, provider.ExecutionRecord(target), provider.HealthObservationFromContext(ctx, status, nil, body, models))
	decision := account.UpstreamErrorDecision{Policy: policy}
	switch policy {
	case account.ErrorPolicyCustomMatched, account.ErrorPolicyTempUnscheduled:
		decision.StopScheduling = true
		return decision
	case account.ErrorPolicyCustomSkipped, account.ErrorPolicyPoolBypassed:
		return decision
	}
	if target.View().IsOAuth() && status == 403 {
		decision = provider.ApplyExecutionHealth(ctx, r.dependencies.Health, target, provider.HealthObservationFromContext(ctx, status, response.Header, body, models))
		logging.LegacyPrintf("service.gateway", "Account %d: applied upstream error policy after %d retries for status %d", target.Record.ID, maxRetryAttempts, status)
	} else {
		logging.LegacyPrintf("service.gateway", "Account %d: upstream error %d after %d retries (not marking account)", target.Record.ID, status, maxRetryAttempts)
	}
	return decision
}

func (r *Runtime) failoverHealth(ctx context.Context, response *http.Response, target *provider.ExecutionAccount, models ...string) account.UpstreamErrorDecision {
	body, _ := r.readErrorBody(response)
	if r.dependencies.Health == nil {
		return account.ErrorDecisionWithoutPersistence(provider.ExecutionErrorPolicy(target), response.StatusCode)
	}
	if len(models) > 0 {
		models = models[:1]
	} else {
		models = nil
	}
	return provider.ApplyExecutionHealth(ctx, r.dependencies.Health, target, provider.HealthObservationFromContext(ctx, response.StatusCode, response.Header, body, models))
}

// claudeScopeError 保留原凭据范围诊断的双短语匹配，不扩大错误分类。
func claudeScopeError(message string) bool {
	message = strings.ToLower(strings.TrimSpace(message))
	return message != "" && strings.Contains(message, "only authorized for use with claude code") &&
		strings.Contains(message, "cannot be used for other api requests")
}
