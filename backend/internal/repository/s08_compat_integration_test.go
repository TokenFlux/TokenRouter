//go:build integration

package repository

import (
	"context"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

type dashboardAggregationRepository = usagepg.AggregationStore
type sqlQueryer = infra.Queryer

func newDashboardAggregationRepositoryWithSQL(q sqlExecutor) *dashboardAggregationRepository {
	return usagepg.NewAggregationStoreWithSQL(q, func(ctx context.Context, t time.Time) error { return billingpg.ArchiveUsageDedup(ctx, q, t) })
}
func scanSingleRow(ctx context.Context, q sqlQueryer, query string, args []any, dest ...any) error {
	return infra.ScanSingleRow(ctx, q, query, args, dest...)
}
func newUsageLogRepositoryWithSQL(client *dbent.Client, q sqlExecutor) *usageLogRepository {
	return &usageLogRepository{usagepg.NewUsageLogRepositoryWithSQL(client, q)}
}

// directUsageExecutor 强制使用现有同步执行器分支，锁断言不会被批处理等待伪造。
type directUsageExecutor struct{ sqlExecutor }
