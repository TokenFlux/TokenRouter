// 查询源只投影标准费用，不拥有资金窗口策略或缓存。
package app

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

type usageWindowStats struct{ store *usagepostgres.Store }

func (s usageWindowStats) GetWindow(ctx context.Context, id int64, start time.Time) (*billing.WindowCostStats, error) {
	v, e := s.store.GetAccountWindowStats(ctx, id, start)
	if v == nil {
		return nil, e
	}
	return &billing.WindowCostStats{StandardCost: v.StandardCost}, e
}
func (s usageWindowStats) GetWindows(ctx context.Context, ids []int64, start time.Time) (map[int64]*billing.WindowCostStats, error) {
	v, e := s.store.GetAccountWindowStatsBatch(ctx, ids, start)
	if v == nil {
		return nil, e
	}
	out := make(map[int64]*billing.WindowCostStats, len(v))
	for id, row := range v {
		if row == nil {
			out[id] = nil
		} else {
			out[id] = &billing.WindowCostStats{StandardCost: row.StandardCost}
		}
	}
	return out, e
}
