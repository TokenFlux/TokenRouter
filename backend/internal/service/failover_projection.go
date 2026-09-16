// 平台错误只投影既有重试字段，HTTP 响应体和凭据不进入重试核心。
package service

import "github.com/TokenFlux/TokenRouter/internal/gateway/failover"

func (e *UpstreamFailoverError) RetryFailure() *failover.FailureInfo {
	if e == nil {
		return nil
	}
	return &failover.FailureInfo{StatusCode: e.StatusCode, ForceCacheBilling: e.ForceCacheBilling, RetryableOnSameAccount: e.RetryableOnSameAccount, RequestScopedTransient: e.RequestScopedTransient, SameAccountRetryDelay: e.SameAccountRetryDelay, SameAccountRetryDeadline: e.SameAccountRetryDeadline, SameAccountRetryMax: e.SameAccountRetryMax, RetryNext: e.ShouldRetryNextAccount()}
}
