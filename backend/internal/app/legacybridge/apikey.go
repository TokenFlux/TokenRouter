// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	sql "database/sql"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// KeyUsageTotals 保留用量聚合查询入口与排序规则，S08 改绑。
func KeyUsageTotals(db *sql.DB, settings *service.PreAggregationSettingsService) keypostgres.UsageTotalsReader {
	return func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return repository.ReadAPIKeyUsageTotals(ctx, db, settings, ids)
	}
}
