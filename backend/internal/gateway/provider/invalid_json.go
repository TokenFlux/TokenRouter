package provider

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
)

// invalidJSONObservation 只接入现有健康命令和错误构造，不保存第二份策略。
type invalidJSONObservation struct {
	rateLimit *accountprovider.UpstreamHealth

	account *ExecutionAccount
	headers http.Header
}

func (a invalidJSONObservation) Log(message string) {
	logging.LegacyPrintf("service.gateway", "%s", message)
}
func (a invalidJSONObservation) Health(ctx context.Context, status int, body []byte, models []string) forwardcore.ErrorDecision {
	d := accountcore.ErrorDecisionWithoutPersistence(ExecutionErrorPolicy(a.account), status)
	if a.rateLimit != nil && a.account != nil {
		if len(models) > 0 {
			d = ApplyExecutionHealth(ctx, a.rateLimit, a.account, HealthObservationFromContext(ctx, status, a.headers, body, []string{models[0]}))
		} else {
			d = ApplyExecutionHealth(ctx, a.rateLimit, a.account, HealthObservationFromContext(ctx, status, a.headers, body, nil))
		}
	}
	return forwardcore.ErrorDecision{Generic: d.ShouldReturnGenericError(), RetrySameAccount: d.RetryableOnSameAccount(ExecutionErrorPolicy(a.account), status)}
}
func (a invalidJSONObservation) Failover(status int, headers map[string][]string, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers, RetryableOnSameAccount: retry}
}

// NonJSONUpstreamFailure 让 Anthropic 直连与兼容输出共用健康反馈和切号错误。
func NonJSONUpstreamFailure(
	ctx context.Context,
	healthObserver *accountprovider.UpstreamHealth,
	resp *http.Response,
	account *ExecutionAccount,
	body []byte,
	parseErr error,
	requestedModel ...string,
) error {
	input := forwardcore.InvalidJSONInput{UpstreamStatus: resp.StatusCode, RequestID: resp.Header.Get("x-request-id"), Headers: resp.Header, Body: body, ParseError: parseErr, RequestedModels: requestedModel}
	if account != nil {
		input.AccountID = account.Record.ID
		input.AccountName = account.Record.Name
	}
	return forwardcore.InvalidJSON(ctx, invalidJSONObservation{rateLimit: healthObserver, account: account, headers: resp.Header}, input)
}
