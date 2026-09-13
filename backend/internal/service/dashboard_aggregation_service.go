// 兼容构造入口只保留旧参数投影，运行状态由 usage 唯一持有。
package service

import (
	"context"
	"database/sql"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/usage"
)

type DashboardAggregationRepository = usage.DashboardAggregationRepository
type DashboardAggregationService struct {
	*usage.DashboardAggregationService
}

var ErrDashboardBackfillDisabled = usage.ErrDashboardBackfillDisabled
var ErrDashboardBackfillTooLarge = usage.ErrDashboardBackfillTooLarge

func NewDashboardAggregationService(repo DashboardAggregationRepository, wheel *TimingWheelService, cfg *config.Config) *DashboardAggregationService {
	return &DashboardAggregationService{usage.NewDashboardAggregationService(repo, wheel, LegacyUsageOptions(cfg))}
}
func (s *DashboardAggregationService) SetLeaderLock(cache LeaderLockCache, db *sql.DB) {
	s.SetSingletonLocker(func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return tryAcquireSingletonLeaderLock(ctx, cache, db, key, owner, ttl)
	})
}
