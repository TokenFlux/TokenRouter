package legacybridge

import (
	"context"
	"time"

	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai/liveattestation"
)

// GroupUsageReports 只投影原聚合结果；用量计算和查询顺序在 S08 迁移。
func GroupUsageReports(dashboard *service.DashboardService) func(context.Context, time.Time) ([]routinghttp.GroupUsageSummary, error) {
	return func(ctx context.Context, today time.Time) ([]routinghttp.GroupUsageSummary, error) {
		values, err := dashboard.GetGroupUsageSummary(ctx, today)
		if values == nil {
			return nil, err
		}
		out := make([]routinghttp.GroupUsageSummary, len(values))
		for i, v := range values {
			out[i] = routinghttp.GroupUsageSummary{GroupID: v.GroupID, TodayCost: v.TodayCost, YesterdayCost: v.YesterdayCost, TotalCost: v.TotalCost}
		}
		return out, err
	}
}

// GroupLiveCapability 维持每次请求创建平台检查器的行为；S09 改绑平台实现。
func GroupLiveCapability(ctx context.Context) error { return liveattestation.NewProvider().Check(ctx) }
