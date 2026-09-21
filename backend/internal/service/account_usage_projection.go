// 仅保留尚未清零的账号测试、Grok 查询与调度统计消费者的字段投影。
package service

import (
	"context"
	"time"

	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

type accountWindowStatsBatchReader interface {
	GetAccountWindowStatsBatch(ctx context.Context, accountIDs []int64, startTime time.Time) (map[int64]*usagecore.AccountStats, error)
}
