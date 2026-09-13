// 旧 Key 统计入口委托 usage 的批量查询，保持同连接和原窗口。
package repository

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/service"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

func ReadAPIKeyUsageTotals(ctx context.Context, sqlq sqlExecutor, settings *service.PreAggregationSettingsService, ids []int64) (map[int64]float64, error) {
	return usagepostgres.ReadAPIKeyUsageTotals(ctx, sqlq, settings, ids)
}
