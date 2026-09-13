package app

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"log"
	"log/slog"
	"math/rand/v2"
	"time"
)

// provideOAuthUsageCore 将旧平台句柄接入唯一读取/缓存/恢复核心，所有入口在开放 HTTP 前完成绑定。
func provideOAuthUsageCore(source *service.AccountUsageService, store *accountpostgres.AccountStore, usage service.UsageLogRepository, cache *account.OAuthUsageCache, manager *lifecycle.Manager) *account.OAuthUsageService {
	stats := account.NewLocalUsageStatistics(legacybridge.NewAccountLocalUsageStats(usage), cache, account.LocalUsageStatisticsOptions{Now: time.Now, Today: timezone.Today, Log: log.Printf})
	options := legacybridge.OAuthUsagePlatformOptions(source)
	options.Now = time.Now
	options.Jitter = rand.Int64N
	options.Log = log.Printf
	options.Warn = slog.Warn
	core := account.NewOAuthUsageService(store, cache, stats, options)
	source.BindCore(core, stats)
	manager.Register(lifecycle.Hook{Name: "AccountOAuthUsage", StopOrder: 25, Stop: core.StopContext})
	return core
}
func provideOAuthUsageStats(source *service.AccountUsageService, core *account.OAuthUsageService) *account.LocalUsageStatistics {
	return source.LocalStatistics()
}
