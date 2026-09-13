package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type accountLocalStats struct{ source usage.UsageLogRepository }

func localWindowStats(v *usagestats.AccountStats) *account.WindowStats {
	if v == nil {
		return nil
	}
	return &account.WindowStats{Requests: v.Requests, Tokens: v.Tokens, Cost: v.Cost, StandardCost: v.StandardCost, UserCost: v.UserCost}
}
func (r accountLocalStats) GetAccountWindowStats(ctx context.Context, id int64, start time.Time) (*account.WindowStats, error) {
	v, err := r.source.GetAccountWindowStats(ctx, id, start)
	return localWindowStats(v), err
}
func (r accountLocalStats) GetAccountTodayStats(ctx context.Context, id int64) (*account.WindowStats, error) {
	v, err := r.source.GetAccountTodayStats(ctx, id)
	return localWindowStats(v), err
}

type accountLocalStatsBatchSource interface {
	GetAccountWindowStatsBatch(context.Context, []int64, time.Time) (map[int64]*usagestats.AccountStats, error)
}
type accountLocalStatsBatch struct {
	accountLocalStats
	batch accountLocalStatsBatchSource
}

func (r accountLocalStatsBatch) GetAccountWindowStatsBatch(ctx context.Context, ids []int64, start time.Time) (map[int64]*account.WindowStats, error) {
	values, err := r.batch.GetAccountWindowStatsBatch(ctx, ids, start)
	if values == nil {
		return nil, err
	}
	out := make(map[int64]*account.WindowStats, len(values))
	for id, v := range values {
		out[id] = localWindowStats(v)
	}
	return out, err
}

// newAccountLocalUsageStats 只投影旧查询；批量失败回退由 account 拥有，S08 改绑来源。
func newAccountLocalUsageStats(source usage.UsageLogRepository) account.LocalUsageStats {
	reader := accountLocalStats{source}
	if batch, ok := source.(accountLocalStatsBatchSource); ok {
		return accountLocalStatsBatch{reader, batch}
	}
	return reader
}
