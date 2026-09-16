package qoder

import "errors"

// MayRefreshAttempt 保留原认证恢复资格，额度和权益拒绝不触发刷新交换。
func MayRefreshAttempt(err error) bool {
	var failure *APIError
	if !errors.As(err, &failure) || failure.IsAgentLimit() || failure.IsEntitlementDenied() {
		return false
	}
	return failure.StatusCode == 401 || failure.StatusCode == 403
}

// MaySwitchAttempt 只投影 Qoder 既有错误分类；调用方仍决定重试窗口和次数。
func MaySwitchAttempt(err error) bool {
	var failure *APIError
	if !errors.As(err, &failure) {
		return true
	}
	return failure.IsAgentLimit() || failure.IsEntitlementDenied() || failure.StatusCode == 429 || failure.StatusCode >= 500
}
