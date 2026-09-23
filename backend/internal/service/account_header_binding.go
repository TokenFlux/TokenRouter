package service

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// bindAccountHeaders 固定原方法值捕获的账号指针，调用时才投影最新字段。
func bindAccountHeaders(value *gatewayprovider.ExecutionAccount) func(http.Header) {
	return func(headers http.Header) {
		provider.ApplyAccountHeaderOverrides(gatewayprovider.ExecutionProtocolRecord(value), headers)
	}
}

// bindAccountHeaderValue 保留原请求构造时机及字段读取顺序。
func bindAccountHeaderValue(value *gatewayprovider.ExecutionAccount) func(string) (string, bool) {
	return func(name string) (string, bool) {
		return provider.HeaderOverrideValue(gatewayprovider.ExecutionProtocolRecord(value), name)
	}
}
