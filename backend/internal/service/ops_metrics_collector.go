// 旧采样构造器只投影端口，生产算法由 Ops 唯一持有。
package service

import (
	"context"
	"database/sql"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/postgres"
	"github.com/TokenFlux/TokenRouter/internal/ops/provider"
	"github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/redis/go-redis/v9"
)

type OpsMetricsCollector = ops.OpsMetricsCollector

func NewOpsMetricsCollector(repo OpsRepository, settings SettingRepository, accounts AccountRepository, concurrency *ConcurrencyService, db *sql.DB, r *redis.Client, cfg *config.Config) *OpsMetricsCollector {
	var source ops.AccountLoadSource
	if accounts != nil {
		base := legacyOpsLoadSource{accounts}
		source = base
		if p, ok := accounts.(interface {
			ListSchedulableAccountLoads(context.Context) ([]AccountWithConcurrency, error)
		}); ok {
			source = legacyOpsLoadProjection{base, p}
		}
	}
	var c ops.ConcurrencyReader
	if concurrency != nil {
		c = concurrency
	}
	return ops.NewOpsMetricsCollector(repo, settings, source, c, postgres.NewMetricsQueries(db), rediscache.NewRuntime(r), LegacyOpsOptions(cfg), provider.NewHostObserver(db, r))
}

type legacyOpsLoadSource struct{ AccountRepository }

func (a legacyOpsLoadSource) ListSchedulable(ctx context.Context) ([]ops.AccountObservation, error) {
	v, e := a.AccountRepository.ListSchedulable(ctx)
	return legacyOpsAccountViews(v), e
}

type legacyOpsLoadProjection struct {
	legacyOpsLoadSource
	source interface {
		ListSchedulableAccountLoads(context.Context) ([]AccountWithConcurrency, error)
	}
}

func (a legacyOpsLoadProjection) ListSchedulableAccountLoads(ctx context.Context) ([]AccountWithConcurrency, error) {
	return a.source.ListSchedulableAccountLoads(ctx)
}

func truncateString(v string, n int) string { return ops.CompatTruncateString(v, n) }

func intPtr(v int) *int { return ops.CompatIntPtr(v) }
