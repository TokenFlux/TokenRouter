package app

import accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

// provideUpstreamHealth 直接发布组合根已构造的唯一观测图，不再回绑旧服务。
func provideUpstreamHealth(runtime *accountHealthRuntime) *accountprovider.UpstreamHealth {
	return runtime.Observer
}
