// 旧网关搜索入口只投影账号和请求，工具规则与合成输出由 gateway/searchtools 唯一实现。
package service

import (
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
)

// 该投影仍被 Live 传输使用；不改变代理缺失时的原返回值。
func resolveAccountProxyURL(account *gatewayprovider.ExecutionAccount) string {
	if account.Record.ProxyID != nil && account.Record.Proxy != nil {
		return account.Record.Proxy.URL()
	}
	return ""
}
