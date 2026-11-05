package app

import (
	"context"
	"database/sql"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	keycore "github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpg "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/usage"
	usagehttp "github.com/TokenFlux/TokenRouter/internal/usage/httpapi"
	usagepg "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
	usageredis "github.com/TokenFlux/TokenRouter/internal/usage/rediscache"
)

// usage.go assembles usage queries, aggregation, cleanup, and dashboard
// services with their storage dependencies.
// Usage querying, dashboard aggregation, and cleanup share the same SQL-backed
// store and calendar. Dashboard refresh tasks use application tracking,
// aggregation receives singleton coordination and billing deduplication
// archiving, while public usage reads copied Key context and separately
// resolved billing/subscription data.

// publicBalanceUnit adapts billing display-unit reads for the public usage
// handler.
type publicBalanceUnit struct {
	store *settings.Store // balance display settings
}

// GetBalanceUnitName delegates the public balance display unit to billing
// settings.
func (r publicBalanceUnit) GetBalanceUnitName(ctx context.Context) string {
	return billing.ReadBalanceUnitName(ctx, r.store)
}

// providePublicUsage binds public usage, live balance, and request funding
// context. Its Key reader uses CopyAPIKey, while billing and subscription
// callbacks retain the gateway context result; date handling uses the supplied
// calendar.
func providePublicUsage(u *usage.UsageService, k *keycore.APIKeyService, users *identity.UserService, store *settings.Store, calendar timezone.Calendar) *usagehttp.PublicUsageHandler {
	return usagehttp.NewPublicUsageHandler(u, k, usagehttp.PublicBalanceQuery(func(ctx context.Context, id int64) (*usagehttp.PublicUserBalance, error) {
		v, e := users.GetByID(ctx, id)
		if e != nil {
			return nil, e
		}
		return &usagehttp.PublicUserBalance{Balance: v.Balance}, nil
	}), publicBalanceUnit{store}, usagehttp.PublicUsageContext{
		Key: func(c *gin.Context) (*keycore.APIKey, bool) {
			value, ok := keyhttp.GetAPIKeyFromContext(c)
			return keycore.CopyAPIKey(value), ok
		},
		Billing: func(c *gin.Context) (*billing.APIKeyBillingContext, bool) {
			return gatewayhttp.GetAPIKeyBillingContext(c)
		},
		Subscription: func(c *gin.Context) (*billing.UserSubscription, bool) {
			return gatewayhttp.SubscriptionFromContext(c)
		},
	}, calendar)
}

// provideUsageOptions returns nil for missing configuration or copies usage
// runtime inputs. It includes the supplied calendar and process logging,
// forwarding aggregation, retention, cleanup, and dashboard values for each
// service's interpretation.
func provideUsageOptions(c *config.Config, calendar timezone.Calendar) *usage.Options {
	if c == nil {
		return nil
	}
	return &usage.Options{
		Calendar: calendar,
		Logf:     logger.LegacyPrintf,
		DashboardAgg: usage.DashboardAggregationConfig{
			Enabled:         c.Usage.Aggregation.Enabled,
			IntervalSeconds: c.Usage.Aggregation.IntervalSeconds,
			LookbackSeconds: c.Usage.Aggregation.LookbackSeconds,
			BackfillEnabled: c.Usage.Aggregation.BackfillEnabled,
			BackfillMaxDays: c.Usage.Aggregation.BackfillMaxDays,
			RecomputeDays:   c.Usage.Aggregation.RecomputeDays,
			Retention: usage.DashboardAggregationRetentionConfig{
				UsageLogsDays:         c.Usage.Retention.LogsDays,
				UsageBillingDedupDays: c.Usage.Retention.BillingDedupDays,
				HourlyDays:            c.Usage.Aggregation.HourlyRetentionDays,
				DailyDays:             c.Usage.Aggregation.DailyRetentionDays,
			},
		},
		UsageCleanup: usage.UsageCleanupConfig{
			Enabled:               c.Usage.CleanupTasks.Enabled,
			MaxRangeDays:          c.Usage.CleanupTasks.MaxRangeDays,
			BatchSize:             c.Usage.CleanupTasks.BatchSize,
			WorkerIntervalSeconds: c.Usage.CleanupTasks.WorkerIntervalSeconds,
			TaskTimeoutSeconds:    c.Usage.CleanupTasks.TaskTimeoutSeconds,
		},
		Dashboard: usage.DashboardConfig{
			Enabled:                    c.Usage.DashboardCache.Enabled,
			StatsFreshTTLSeconds:       c.Usage.DashboardCache.StatsFreshTTLSeconds,
			StatsTTLSeconds:            c.Usage.DashboardCache.StatsTTLSeconds,
			StatsRefreshTimeoutSeconds: c.Usage.DashboardCache.StatsRefreshTimeoutSeconds,
		},
	}
}

// provideUsageStore binds shared SQL, Ent, aggregation settings, and calendar
// to usage persistence.
func provideUsageStore(client *dbent.Client, db *sql.DB, settings *preaggregation.PreAggregationSettingsService, calendar timezone.Calendar) *usagepg.Store {
	return usagepg.NewUsageLogRepository(client, db, settings, calendar)
}

// provideUsageRepository exposes the shared usage store through its repository
// interface.
func provideUsageRepository(store *usagepg.Store) usage.UsageLogRepository {
	return store
}

// provideUsageService constructs user usage queries using the shared
// persistence store.
func provideUsageService(store *usagepg.Store) *usage.UsageService {
	return usage.NewUsageService(store)
}

// provideUsageAggregationRepository binds calendar aggregation and billing
// deduplication archiving to SQL. If construction returns no repository, the
// interface result remains nil; otherwise its archive callback uses that same
// database pool.
func provideUsageAggregationRepository(db *sql.DB, calendar timezone.Calendar) usage.DashboardAggregationRepository {
	store := usagepg.NewDashboardAggregationRepository(db, calendar, func(ctx context.Context, t time.Time) error { return billingpg.ArchiveUsageDedup(ctx, db, t) })
	if store == nil {
		return nil
	}
	return store
}

// provideUsageCleanupRepository constructs usage retention persistence with
// shared Ent and SQL.
func provideUsageCleanupRepository(client *dbent.Client, db *sql.DB) usage.UsageCleanupRepository {
	return usagepg.NewUsageCleanupRepository(client, db)
}

// provideUsageAggregation constructs aggregation with shared time-wheel and
// settings dependencies. It installs singleton acquisition using Redis and
// optional database advisory locks; the aggregation service owns recurring
// execution and the returned release callbacks.
func provideUsageAggregation(repo usage.DashboardAggregationRepository, wheel *timingwheel.Wheel, cache provider.LeaderLocker, db *sql.DB, options *usage.Options, settings *preaggregation.PreAggregationSettingsService) *usage.DashboardAggregationService {
	s := usage.NewDashboardAggregationService(repo, wheel, options)
	s.SetSingletonLocker(func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return provider.AcquireSingletonLease(ctx, cache, databaseAdvisoryLease(db), key, owner, ttl)
	})
	s.SetPreAggregationSettings(settings)
	return s
}

// provideUsageCleanup constructs usage retention work with time-wheel and
// aggregation coordination.
func provideUsageCleanup(repo usage.UsageCleanupRepository, wheel *timingwheel.Wheel, agg *usage.DashboardAggregationService, options *usage.Options) *usage.UsageCleanupService {
	return usage.NewUsageCleanupService(repo, wheel, agg, options)
}

// provideUsageDashboard binds dashboard reads and cached refresh work to shared
// stores. tasks supplies background ownership, and pre-aggregation settings are
// installed on the same dashboard before it is returned.
func provideUsageDashboard(store *usagepg.Store, agg usage.DashboardAggregationRepository, cache usage.DashboardStatsCache, options *usage.Options, settings *preaggregation.PreAggregationSettingsService, tasks *lifecycle.Tasks) *usage.DashboardService {
	s := usage.NewDashboardService(store, agg, cache, options)
	s.SetBackgroundRunner(tasks.Go)
	s.SetPreAggregationSettings(settings)
	return s
}

// provideUsageDashboardCache binds dashboard Redis storage with a startup
// key-prefix input. Nil cfg selects "tokenrouter:"; supplied cfg forwards
// KeyPrefix, including empty values, for the cache constructor to interpret.
func provideUsageDashboardCache(r *redis.Client, cfg *config.Config) usage.DashboardStatsCache {
	prefix := "tokenrouter:"
	if cfg != nil {
		prefix = cfg.Usage.DashboardCache.KeyPrefix
	}
	return usageredis.NewDashboardCache(r, prefix)
}
