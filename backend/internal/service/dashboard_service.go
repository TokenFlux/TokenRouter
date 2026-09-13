// 旧仪表盘入口只负责参数/查询能力投影，缓存和算法由 usage 唯一持有。
package service

import (
	"context"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/pkg/usagestats"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type DashboardService = usage.DashboardService
type DashboardStatsCache = usage.DashboardStatsCache
type DashboardPublicStats = usage.DashboardPublicStats

func NewDashboardService(repo UsageLogRepository, agg DashboardAggregationRepository, cache DashboardStatsCache, cfg *config.Config) *DashboardService {
	readers := usage.DashboardReaders{}
	readers.Range, _ = repo.(interface {
		GetDashboardStatsWithRange(context.Context, time.Time, time.Time) (*usagestats.DashboardStats, error)
	})
	readers.Public, _ = repo.(interface {
		GetDashboardPublicStats(context.Context, time.Time, time.Time, bool) (*DashboardPublicStats, error)
	})
	options := LegacyUsageOptions(cfg)
	native := usage.NewDashboardService(legacyUsageRepository{repo}, agg, cache, options, readers)
	native.SetBackgroundRunner(RunBackgroundTask)
	return native
}
func ProvideDashboardService(repo UsageLogRepository, agg DashboardAggregationRepository, cache DashboardStatsCache, cfg *config.Config, settings *PreAggregationSettingsService) *DashboardService {
	s := NewDashboardService(repo, agg, cache, cfg)
	s.SetPreAggregationSettings(settings)
	return s
}

var ErrDashboardStatsCacheMiss = usage.ErrDashboardStatsCacheMiss
