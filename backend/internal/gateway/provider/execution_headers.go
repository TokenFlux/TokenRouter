package provider

import (
	"net/http"

	"github.com/TokenFlux/TokenRouter/internal/account/provider"
)

// BindExecutionHeaders 固定原方法值捕获的账号指针，调用时才投影最新字段。
func BindExecutionHeaders(value *ExecutionAccount) func(http.Header) {
	return func(headers http.Header) {
		provider.ApplyAccountHeaderOverrides(ExecutionProtocolRecord(value), headers)
	}
}

// BindExecutionHeaderValue 保留原请求构造时机及字段读取顺序。
func BindExecutionHeaderValue(value *ExecutionAccount) func(string) (string, bool) {
	return func(name string) (string, bool) {
		return provider.HeaderOverrideValue(ExecutionProtocolRecord(value), name)
	}
}
