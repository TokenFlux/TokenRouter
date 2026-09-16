// 透传 failover 分类只返回当前错误能否交还上层换号，不执行重试。
package openaiforward

import "net/http"

// PassthroughFailureOptions 使用明确分类查询，保持平台解释的原调用顺序。
type PassthroughFailureOptions struct {
	Cyber         func([]byte) bool
	ContextWindow func([]byte) bool
	AccessState   func(int, []byte) bool
	BodyTooLarge  func(int, []byte) bool
	PoolRetryable func(int) bool
	APIKey        bool
}

func ShouldFailoverPassthrough(status int, body []byte, o PassthroughFailureOptions) bool {
	if o.Cyber(body) {
		return false
	}
	if o.ContextWindow(body) {
		return false
	}
	if o.AccessState(status, body) {
		return true
	}
	if o.BodyTooLarge(status, body) {
		return true
	}
	if o.PoolRetryable(status) {
		return true
	}
	switch status {
	case http.StatusTooManyRequests, 529:
		return true
	}
	if !o.APIKey {
		return false
	}
	switch status {
	case http.StatusInternalServerError, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 520, 521, 522, 523, 524:
		return true
	default:
		return false
	}
}
