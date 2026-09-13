// 旧清理构造只投影参数和错误，业务与运行状态由 usage 拥有。
package service

import (
	"context"
	"database/sql"
	"errors"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type UsageCleanupService = usage.UsageCleanupService

func NewUsageCleanupService(repo UsageCleanupRepository, wheel *TimingWheelService, agg *DashboardAggregationService, cfg *config.Config) *UsageCleanupService {
	var native *usage.DashboardAggregationService
	if agg != nil {
		native = agg.DashboardAggregationService
	}
	if repo != nil {
		repo = legacyCleanupStatus{repo}
	}
	return usage.NewUsageCleanupService(repo, wheel, native, LegacyUsageOptions(cfg))
}

// legacyCleanupStatus 保留旧仓储边界，在 Adapter 转换数据库缺失错误。
type legacyCleanupStatus struct{ UsageCleanupRepository }

func (r legacyCleanupStatus) GetTaskStatus(ctx context.Context, id int64) (string, error) {
	v, e := r.UsageCleanupRepository.GetTaskStatus(ctx, id)
	if errors.Is(e, sql.ErrNoRows) {
		e = usage.ErrCleanupTaskNotFound
	}
	return v, e
}
