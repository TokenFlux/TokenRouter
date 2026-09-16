package service

import (
	"context"
	"net/http"

	forwardcore "github.com/TokenFlux/TokenRouter/internal/gateway/forward"
	"github.com/TokenFlux/TokenRouter/internal/pkg/logger"
)

// invalidJSONAdapter 只接入现有健康命令和错误构造，不保存第二份策略。
type invalidJSONAdapter struct {
	rateLimit *RateLimitService
	account   *Account
	headers   http.Header
}

func (a invalidJSONAdapter) Log(message string) {
	logger.LegacyPrintf("service.gateway", "%s", message)
}
func (a invalidJSONAdapter) Health(ctx context.Context, status int, body []byte, models []string) forwardcore.ErrorDecision {
	d := upstreamErrorDecisionWithoutPersistence(a.account, status)
	if a.rateLimit != nil && a.account != nil {
		if len(models) > 0 {
			d = a.rateLimit.ApplyUpstreamError(ctx, a.account, status, a.headers, body, models[0])
		} else {
			d = a.rateLimit.ApplyUpstreamError(ctx, a.account, status, a.headers, body)
		}
	}
	return forwardcore.ErrorDecision{Generic: d.ShouldReturnGenericError(), RetrySameAccount: d.RetryableOnSameAccount(a.account, status)}
}
func (a invalidJSONAdapter) Failover(status int, headers map[string][]string, body []byte, retry bool) error {
	return &UpstreamFailoverError{StatusCode: status, ResponseBody: body, ResponseHeaders: headers, RetryableOnSameAccount: retry}
}
