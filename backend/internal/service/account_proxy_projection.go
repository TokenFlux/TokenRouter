package service

import gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

// accountProxyURL 返回账号绑定的代理地址（无代理时为空串）。
func accountProxyURL(account *gatewayprovider.ExecutionAccount) string {
	if account == nil || account.Record.ProxyID == nil || account.Record.Proxy == nil {
		return ""
	}
	return account.Record.Proxy.URL()
}
