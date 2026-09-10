// 本文件为阶段迁移兼容入口；剩余消费者和退出阶段见 refactor/S01-foundation.md。
package urlvalidator

import (
	foundation "github.com/TokenFlux/TokenRouter/internal/egress"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
)

// ValidationOptions 保留旧调用方的类型身份；实现归目标包。
type ValidationOptions = foundation.ValidationOptions

// ValidateHTTPURL 兼容旧入口；仅转发到目标实现。
func ValidateHTTPURL(raw string, allowInsecureHTTP bool, opts ValidationOptions) (string, error) {
	return foundation.ValidateHTTPURL(raw, allowInsecureHTTP, opts)
}

// ValidateURLFormat 兼容旧入口；仅转发到目标实现。
func ValidateURLFormat(raw string, allowInsecureHTTP bool) (string, error) {
	return foundation.ValidateURLFormat(raw, allowInsecureHTTP)
}

// ValidateHTTPSURL 兼容旧入口；仅转发到目标实现。
func ValidateHTTPSURL(raw string, opts ValidationOptions) (string, error) {
	return foundation.ValidateHTTPSURL(raw, opts)
}

// ValidateResolvedIP 兼容旧入口；仅转发到目标实现。
func ValidateResolvedIP(host string) error {
	return httpclient.ValidateResolvedIP(host)
}

// IsBlockedHost 兼容旧入口；仅转发到目标实现。
func IsBlockedHost(host string) bool {
	return foundation.IsBlockedHost(host)
}
