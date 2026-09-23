package app

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/idempotency"
)

// idempotencyOptions 保留正数覆盖默认值和 ObserveOnly 的原配置语义。
func idempotencyOptions(cfg *config.Config) idempotency.IdempotencyConfig {
	opts := idempotency.DefaultIdempotencyConfig()
	if cfg == nil {
		return opts
	}
	if cfg.Idempotency.DefaultTTLSeconds > 0 {
		opts.DefaultTTL = time.Duration(cfg.Idempotency.DefaultTTLSeconds) * time.Second
	}
	if cfg.Idempotency.SystemOperationTTLSeconds > 0 {
		opts.SystemOperationTTL = time.Duration(cfg.Idempotency.SystemOperationTTLSeconds) * time.Second
	}
	if cfg.Idempotency.ProcessingTimeoutSeconds > 0 {
		opts.ProcessingTimeout = time.Duration(cfg.Idempotency.ProcessingTimeoutSeconds) * time.Second
	}
	if cfg.Idempotency.FailedRetryBackoffSeconds > 0 {
		opts.FailedRetryBackoff = time.Duration(cfg.Idempotency.FailedRetryBackoffSeconds) * time.Second
	}
	if cfg.Idempotency.MaxStoredResponseLen > 0 {
		opts.MaxStoredResponseLen = cfg.Idempotency.MaxStoredResponseLen
	}
	opts.ObserveOnly = cfg.Idempotency.ObserveOnly
	return opts
}

// provideIdempotencyCoordinator 构造唯一协调器，由 HTTP 装配显式共享。
func provideIdempotencyCoordinator(repo idempotency.IdempotencyRepository, cfg *config.Config) *idempotency.IdempotencyCoordinator {
	coordinator := idempotency.NewIdempotencyCoordinator(repo, idempotencyOptions(cfg), idempotencyObserver())
	return coordinator
}

// provideIdempotencyCleanupService 只构造任务，启动和停止由应用生命周期持有。
func provideIdempotencyCleanupService(repo idempotency.IdempotencyRepository, cfg *config.Config) *idempotency.IdempotencyCleanupService {
	opts := idempotency.CleanupOptions{Observer: idempotencyObserver()}
	if cfg != nil {
		opts.Interval = time.Duration(cfg.Idempotency.CleanupIntervalSeconds) * time.Second
		opts.Batch = cfg.Idempotency.CleanupBatchSize
	}
	return idempotency.NewIdempotencyCleanupService(repo, opts)
}

// idempotencyObserver 由组合根提供日志出口，不在幂等核心安装全局后端。
func idempotencyObserver() idempotency.Observer {
	return idempotency.ObserverFunc(func(component, message string) { logging.LegacyPrintf(component, "%s", message) })
}
