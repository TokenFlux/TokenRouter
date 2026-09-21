// 旧用量来源只投影标准费用，读取口径及 SQL 属于待迁 S08。
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type usageLogWindowStatsBatchProvider interface {
	GetAccountWindowStatsBatch(context.Context, []int64, time.Time) (map[int64]*usage.AccountStats, error)
}
type legacyWindowCostSource struct{ usage.UsageLogRepository }

func (s legacyWindowCostSource) GetWindow(ctx context.Context, id int64, start time.Time) (*billing.WindowCostStats, error) {
	v, err := s.GetAccountWindowStats(ctx, id, start)
	if v == nil {
		return nil, err
	}
	return &billing.WindowCostStats{StandardCost: v.StandardCost}, err
}

type legacyWindowCostBatchSource struct {
	legacyWindowCostSource
	batch usageLogWindowStatsBatchProvider
}

func (s legacyWindowCostBatchSource) GetWindows(ctx context.Context, ids []int64, start time.Time) (map[int64]*billing.WindowCostStats, error) {
	v, err := s.batch.GetAccountWindowStatsBatch(ctx, ids, start)
	if v == nil {
		return nil, err
	}
	out := make(map[int64]*billing.WindowCostStats, len(v))
	for id, row := range v {
		if row != nil {
			out[id] = &billing.WindowCostStats{StandardCost: row.StandardCost}
		} else {
			out[id] = nil
		}
	}
	return out, err
}
func (s *GatewayService) windowCostGuard() *billing.WindowCostGuard {
	var source billing.WindowCostSource
	if s.usageWindowSource != nil {
		source = s.usageWindowSource
	} else if s.usageLogRepo != nil {
		base := legacyWindowCostSource{s.usageLogRepo}
		source = base
		if batch, ok := s.usageLogRepo.(usageLogWindowStatsBatchProvider); ok {
			source = legacyWindowCostBatchSource{base, batch}
		}
	}
	return billing.NewWindowCostGuard(s.windowCostCache, source, billing.WindowCostGuardOptions{Now: time.Now, Stats: billing.SharedWindowCostMetrics(), Log: func(format string, args ...any) { logging.LegacyPrintf("service.gateway", format, args...) }, Debug: slog.Debug})
}
func costWindowInput(a *Account) billing.CostWindowInput {
	if a == nil {
		return billing.CostWindowInput{}
	}
	return billing.CostWindowInput{ID: a.ID, Enabled: a.IsAnthropicOAuthOrSetupToken(), Limit: a.GetWindowCostLimit(), Reserve: a.GetWindowCostStickyReserve(), Start: a.SessionWindowStart, End: a.SessionWindowEnd}
}

// BindUsageWindowSource 由 app 绑定新用量查询；窗口规则仍由 billing 拥有。
func (s *GatewayService) BindUsageWindowSource(source billing.WindowCostSource) {
	s.usageWindowSource = source
}
