//go:build integration

package billing_test

import (
	"context"
	"time"

	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	infra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// directUsageExecutor 保留同步写入路径，避免批处理等待掩盖原锁顺序断言。
type directUsageExecutor struct{ infra.Executor }

// newAggregationFixture 沿用先归档资金去重记录再清理的同一回调。
func newAggregationFixture(q infra.Executor) *usagepg.AggregationStore {
	return usagepg.NewAggregationStoreWithSQL(q, timezone.NewCalendar(time.Local), func(ctx context.Context, t time.Time) error {
		return billingpg.ArchiveUsageDedup(ctx, q, t)
	})
}
