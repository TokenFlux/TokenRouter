package provider

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
)

// ApplyAccountHeaderOverrides 在原调用位置应用账号策略，保留 wire 大小写和后续会话头覆盖顺序。
func ApplyAccountHeaderOverrides(value *account.Record, headers http.Header) {
	if headers == nil {
		return
	}
	policy := egress.RequestPolicy(egress.RequestPolicyInput{Headers: value.HeaderOverrides()})
	egressprovider.ApplyRequestHeaders(headers, policy, anthropic.ResolveWireCasing)
}

// HeaderOverrideValue 让协议适配在原 body 净化时点读取一项覆写值。
func HeaderOverrideValue(value *account.Record, lowerName string) (string, bool) {
	result, ok := value.HeaderOverrides()[lowerName]
	return result, ok
}
