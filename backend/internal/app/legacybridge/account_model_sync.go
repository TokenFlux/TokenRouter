// 本文件维护 legacybridge 的所属能力；兼容入口复用唯一实现。
package legacybridge

import (
	context "context"
	account "github.com/TokenFlux/TokenRouter/internal/account"
	usagestats "github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	time "time"
)

// AccountModelSyncFetch 只转交旧供应商执行端口，S09 改绑。
func AccountModelSyncFetch(source *service.AccountTestService) func(context.Context, *account.Record) ([]string, error) {
	return service.AccountModelSyncFetch(source)
}

// AccountUsageReportQuery 只投影旧详细统计查询，S08 迁移 SQL 和报告值。
func AccountUsageReportQuery(source *service.AccountUsageService) func(context.Context, int64, time.Time, time.Time) (*usagestats.AccountUsageStatsResponse, error) {
	return source.GetAccountUsageStats
}
