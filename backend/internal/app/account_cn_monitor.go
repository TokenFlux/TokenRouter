package app

import (
	"context"
	"database/sql"
	"github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
	"log/slog"
	"time"
)

// provideCNUsageMonitor 绑定同一账号 Store 与只读查询；数据库咨询锁只在技术装配侧构造。
func provideCNUsageMonitor(store *accountpostgres.AccountStore, queries *service.UpstreamUsageService, cfg *config.Config, leader service.LeaderLockCache, db *sql.DB) *account.CNUsageMonitor {
	options := account.CNMonitorOptions{Now: time.Now, InstanceID: uuid.NewString(), BalanceThreshold: 0.5, Leader: leader, Warn: slog.Warn, Debug: slog.Debug}
	if cfg != nil {
		v := cfg.Gateway.CNProviders
		options.Enabled = v.MonitorEnabled
		options.Interval = time.Duration(v.IntervalMinutes) * time.Minute
		options.ProbeTimeout = time.Duration(v.ProbeTimeoutSeconds) * time.Second
		options.RoundTimeout = time.Duration(v.RoundTimeoutSeconds) * time.Second
		options.Concurrency = v.Concurrency
		options.BalanceThreshold = v.BalanceThreshold
		h := cfg.Security.URLAllowlist
		options.HostPolicy = egress.MonitorHostPolicy{Enabled: h.Enabled, AllowInsecureHTTP: h.AllowInsecureHTTP, AllowPrivate: h.AllowPrivateHosts, AllowedHosts: h.UpstreamHosts}
	}
	if db != nil {
		options.Advisory = func(ctx context.Context, key string) (func(), bool) {
			return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, postgresinfra.HashAdvisoryLockID(key))
		}
	}
	return account.NewCNUsageMonitor(store, queries.Core(), options)
}
