// 测试绑定与生产使用同一资金归档实现，不复制 SQL。
package postgres

import (
	"context"
	"time"

	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
)

func newDashboardAggregationRepositoryWithSQL(q sqlExecutor) *AggregationStore {
	return NewAggregationStoreWithSQL(q, timezone.NewCalendar(time.Local), func(ctx context.Context, t time.Time) error { return billingpg.ArchiveUsageDedup(ctx, q, t) })
}
