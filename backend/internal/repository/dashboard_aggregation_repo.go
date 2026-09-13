// 旧聚合仓储构造只绑定同连接的资金归档，查询与状态实现由 usage/postgres 持有。
package repository

import (
	"context"
	"database/sql"
	"time"

	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

func NewDashboardAggregationRepository(db *sql.DB) service.DashboardAggregationRepository {
	return usagepg.NewDashboardAggregationRepository(db, func(ctx context.Context, t time.Time) error { return billingpg.ArchiveUsageDedup(ctx, db, t) })
}
