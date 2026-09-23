package testkit

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

// WindowCosts 只投影原用量测试来源，保留可选批量能力与 nil 返回，不实现窗口规则。
func WindowCosts(source usage.UsageLogRepository) billing.WindowCostSource {
	if source == nil {
		return nil
	}
	base := windowCosts{source}
	if batch, ok := source.(windowCostBatch); ok {
		return windowCostsBatch{windowCosts: base, batch: batch}
	}
	return base
}

type windowCosts struct{ source usage.UsageLogRepository }

func (s windowCosts) GetWindow(ctx context.Context, id int64, start time.Time) (*billing.WindowCostStats, error) {
	value, err := s.source.GetAccountWindowStats(ctx, id, start)
	if value == nil {
		return nil, err
	}
	return &billing.WindowCostStats{StandardCost: value.StandardCost}, err
}

type windowCostBatch interface {
	GetAccountWindowStatsBatch(context.Context, []int64, time.Time) (map[int64]*usage.AccountStats, error)
}
type windowCostsBatch struct {
	windowCosts
	batch windowCostBatch
}

func (s windowCostsBatch) GetWindows(ctx context.Context, ids []int64, start time.Time) (map[int64]*billing.WindowCostStats, error) {
	values, err := s.batch.GetAccountWindowStatsBatch(ctx, ids, start)
	if values == nil {
		return nil, err
	}
	out := make(map[int64]*billing.WindowCostStats, len(values))
	for id, value := range values {
		if value == nil {
			out[id] = nil
		} else {
			out[id] = &billing.WindowCostStats{StandardCost: value.StandardCost}
		}
	}
	return out, err
}
