package service

import (
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	"context"
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// invalidJSONAdapter 只接入现有健康命令和错误构造，不保存第二份策略。
type invalidJSONAdapter struct {
	rateLimit *accountprovider.UpstreamHealth

	account *gatewayprovider.ExecutionAccount
	headers http.Header
}

func (a invalidJSONAdapter) Log(message string) {
	logging.LegacyPrintf("service.gateway", "%s", message)
}
func (a invalidJSONAdapter) Health(ctx context.Context, status int, body []byte, models []string) forwardcore.ErrorDecision {
	d := accountcore.ErrorDecisionWithoutPersistence(gatewayprovider.ExecutionErrorPolicy(a.account), status)
	if a.rateLimit != nil && a.account != nil {
		if len(models) > 0 {
			d = gatewayprovider.ApplyExecutionHealth(ctx, a.rateLimit, a.account, gatewayprovider.HealthObservationFromContext(ctx, status, a.headers, body, []string{models[0]}))
		} else {
			d = gatewayprovider.ApplyExecutionHealth(ctx, a.rateLimit, a.account, gatewayprovider.HealthObservationFromContext(ctx, status, a.headers, body, nil))
		}
	}
	return forwardcore.ErrorDecision{Generic: d.ShouldReturnGenericError(), RetrySameAccount: d.RetryableOnSameAccount(gatewayprovider.ExecutionErrorPolicy(a.account), status)}
}
func (a invalidJSONAdapter) Failover(status int, headers map[string][]string, body []byte, retry bool) error {
	return &forwardcore.UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers, RetryableOnSameAccount: retry}
}
