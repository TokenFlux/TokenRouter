// app 装配唯一用量存储、查询、聚合与旧形状投影。
package app

import (
	"context"
	"database/sql"
	"time"

	usageredis "github.com/TokenFlux/TokenRouter/internal/usage/rediscache"
	"github.com/redis/go-redis/v9"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"

	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"

	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/usage"

	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

func provideUsageOptions(c *config.Config) *usage.Options {
	if c == nil {
		return nil
	}
	return &usage.Options{Logf: logger.LegacyPrintf, DashboardAgg: usage.DashboardAggregationConfig{Enabled: c.DashboardAgg.Enabled, IntervalSeconds: c.DashboardAgg.IntervalSeconds, LookbackSeconds: c.DashboardAgg.LookbackSeconds, BackfillEnabled: c.DashboardAgg.BackfillEnabled, BackfillMaxDays: c.DashboardAgg.BackfillMaxDays, RecomputeDays: c.DashboardAgg.RecomputeDays, Retention: usage.DashboardAggregationRetentionConfig{UsageLogsDays: c.DashboardAgg.Retention.UsageLogsDays, UsageBillingDedupDays: c.DashboardAgg.Retention.UsageBillingDedupDays, HourlyDays: c.DashboardAgg.Retention.HourlyDays, DailyDays: c.DashboardAgg.Retention.DailyDays}}, UsageCleanup: usage.UsageCleanupConfig{Enabled: c.UsageCleanup.Enabled, MaxRangeDays: c.UsageCleanup.MaxRangeDays, BatchSize: c.UsageCleanup.BatchSize, WorkerIntervalSeconds: c.UsageCleanup.WorkerIntervalSeconds, TaskTimeoutSeconds: c.UsageCleanup.TaskTimeoutSeconds}, Dashboard: usage.DashboardConfig{Enabled: c.Dashboard.Enabled, StatsFreshTTLSeconds: c.Dashboard.StatsFreshTTLSeconds, StatsTTLSeconds: c.Dashboard.StatsTTLSeconds, StatsRefreshTimeoutSeconds: c.Dashboard.StatsRefreshTimeoutSeconds}}
}
func provideUsageStore(client *dbent.Client, db *sql.DB, settings *preaggregation.PreAggregationSettingsService) *usagepg.Store {
	return usagepg.NewUsageLogRepository(client, db, settings)
}
func provideLegacyUsageRepository(store *usagepg.Store) service.UsageLogRepository {
	return repository.WrapUsageStore(store)
}
func provideUsageService(store *usagepg.Store) *usage.UsageService {
	return usage.NewUsageService(store)
}
func provideLegacyUsageService(s *usage.UsageService) *service.UsageService {
	return &service.UsageService{UsageService: s}
}
func provideUsageAggregationRepository(db *sql.DB) usage.DashboardAggregationRepository {
	store := usagepg.NewDashboardAggregationRepository(db, func(ctx context.Context, t time.Time) error { return billingpg.ArchiveUsageDedup(ctx, db, t) })
	if store == nil {
		return nil
	}
	return store
}
func provideUsageCleanupRepository(client *dbent.Client, db *sql.DB) usage.UsageCleanupRepository {
	return usagepg.NewUsageCleanupRepository(client, db)
}
func provideUsageAggregation(repo usage.DashboardAggregationRepository, wheel *service.TimingWheelService, cache service.LeaderLockCache, db *sql.DB, options *usage.Options, settings *preaggregation.PreAggregationSettingsService) *usage.DashboardAggregationService {
	s := usage.NewDashboardAggregationService(repo, wheel, options)
	s.SetSingletonLocker(func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return service.AcquireSingletonLeaderLock(ctx, cache, db, key, owner, ttl)
	})
	s.SetPreAggregationSettings(settings)
	return s
}
func provideLegacyUsageAggregation(s *usage.DashboardAggregationService) *service.DashboardAggregationService {
	return &service.DashboardAggregationService{DashboardAggregationService: s}
}
func provideUsageCleanup(repo usage.UsageCleanupRepository, wheel *service.TimingWheelService, agg *usage.DashboardAggregationService, options *usage.Options) *usage.UsageCleanupService {
	return usage.NewUsageCleanupService(repo, wheel, agg, options)
}
func provideUsageDashboard(store *usagepg.Store, agg usage.DashboardAggregationRepository, cache usage.DashboardStatsCache, options *usage.Options, settings *preaggregation.PreAggregationSettingsService, tasks *lifecycle.Tasks) *usage.DashboardService {
	s := usage.NewDashboardService(store, agg, cache, options)
	s.SetBackgroundRunner(tasks.Go)
	s.SetPreAggregationSettings(settings)
	return s
}

// provideUsageDashboardCache 只投影原前缀，复用唯一 Redis 客户端。
func provideUsageDashboardCache(r *redis.Client, cfg *config.Config) usage.DashboardStatsCache {
	prefix := "sub2api:"
	if cfg != nil {
		prefix = cfg.Dashboard.KeyPrefix
	}
	return usageredis.NewDashboardCache(r, prefix)
}
