package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accounthttp "github.com/TokenFlux/TokenRouter/internal/account/httpapi"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// provideUpstreamUsage 将唯一账号查询核心接到同一存储、平台执行和有界退出。
func provideUpstreamUsage(store *accountpostgres.AccountStore, repo service.AccountRepository, upstream service.HTTPUpstream, cfg *config.Config, tls *service.TLSFingerprintProfileService, manager *lifecycle.Manager) *service.UpstreamUsageService {
	source := service.NewUpstreamUsageExecution(repo, upstream, cfg, tls)
	core := account.NewUpstreamUsageService(store, legacybridge.AccountUpstreamUsage{Source: source}, account.UpstreamUsageOptions{Now: time.Now})
	source.BindCore(core)
	// 周期生产者停止后，查询完成才允许后续 SQL/HTTP 依赖释放。
	manager.Register(lifecycle.Hook{Name: "AccountUpstreamUsage", StopOrder: 25, Stop: core.StopContext})
	return source
}

// provideUpstreamUsageHTTP 直接绑定新核心，避免 HTTP 反向依赖旧服务。
func provideUpstreamUsageHTTP(source *service.UpstreamUsageService) *accounthttp.UpstreamUsageHandler {
	return accounthttp.NewUpstreamUsageHandler(source.Core())
}
