package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	provider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	httpclient "github.com/TokenFlux/TokenRouter/internal/infra/httpclient"

	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
)

// provideUpstreamUsage 将唯一账号查询核心接到同一存储、平台执行和有界退出。
func provideUpstreamUsage(store *accountpostgres.AccountStore, upstream httpclient.UpstreamTransport, cfg *config.Config, tls *provider.TLSProfiles, manager *lifecycle.Manager) *account.UpstreamUsageService {
	options := accountprovider.UsageHTTPOptions{Available: store != nil && upstream != nil}
	if cfg != nil {
		value := cfg.Security.URLAllowlist
		options.Policy = egress.UsageURLPolicy{Configured: true, Enabled: value.Enabled, AllowInsecureHTTP: value.AllowInsecureHTTP, AllowPrivateHosts: value.AllowPrivateHosts, UpstreamHosts: value.UpstreamHosts}
	}
	if upstream != nil {
		options.Do = upstream.DoWithTLS
	}
	if tls != nil {
		options.ResolveTLS = tls.ResolveRequestTLS
	}
	source := accountprovider.NewUpstreamUsageHTTPExecution(options)
	core := account.NewUpstreamUsageService(store, source, account.UpstreamUsageOptions{Now: time.Now})
	// 周期生产者停止后，查询完成才允许后续 SQL/HTTP 依赖释放。
	manager.Register(lifecycle.Hook{Name: "AccountUpstreamUsage", StopOrder: 25, Stop: core.StopContext})
	return core
}

// provideUpstreamUsageHTTP 直接绑定新核心，避免 HTTP 反向依赖旧服务。
func provideUpstreamUsageHTTP(source *account.UpstreamUsageService) *accounthttp.UpstreamUsageHandler {
	return accounthttp.NewUpstreamUsageHandler(source)
}
