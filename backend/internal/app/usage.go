package app

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	usageprovider "github.com/TokenFlux/TokenRouter/internal/usage/provider"
	usageredis "github.com/TokenFlux/TokenRouter/internal/usage/rediscache"
)

// usage.go assembles usage queries, aggregation, cleanup, export, and dashboard
// services with their storage dependencies.
// Usage querying, dashboard aggregation, cleanup, and keyset CSV exports share
// the same SQL-backed store and calendar. Dashboard refresh tasks use
// application tracking, aggregation receives singleton coordination and billing
// deduplication archiving, export artifacts stay inside a confined local root
// closed after worker and timer shutdown, and public usage reads copied Key
// context with separately resolved billing/subscription data.

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
// context. Its Key reader uses CopyAPIKey, spending windows read directly from
// KeyUsageStore, and billing/subscription callbacks retain the gateway context
// result; date handling uses the supplied calendar.
func providePublicUsage(u *usage.UsageService, keyUsage *billingpg.KeyUsageStore, users *identity.UserService, store *settings.Store, calendar timezone.Calendar) *usagehttp.PublicUsageHandler {
	return usagehttp.NewPublicUsageHandler(u, keyUsage, usagehttp.PublicBalanceQuery(func(ctx context.Context, id int64) (*usagehttp.PublicUserBalance, error) {
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
// forwarding aggregation, retention, cleanup, export, and dashboard values for
// each service's interpretation.
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
		UsageExport: usage.UsageExportConfig{
			Enabled:               c.Usage.ExportTasks.Enabled,
			RetentionHours:        c.Usage.ExportTasks.RetentionHours,
			MaxActivePerUser:      c.Usage.ExportTasks.MaxActivePerUser,
			MaxActivePerAdmin:     c.Usage.ExportTasks.MaxActivePerAdmin,
			BatchSize:             c.Usage.ExportTasks.BatchSize,
			WorkerConcurrency:     c.Usage.ExportTasks.WorkerConcurrency,
			WorkerIntervalSeconds: c.Usage.ExportTasks.WorkerIntervalSeconds,
			TaskTimeoutSeconds:    c.Usage.ExportTasks.TaskTimeoutSeconds,
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

// provideUsageExportRepository constructs usage-export task persistence over
// shared Ent and SQL. Missing database dependencies return a nil interface so
// route-inventory and unconfigured fixtures observe an absent repository.
func provideUsageExportRepository(client *dbent.Client, db *sql.DB) usage.UsageExportRepository {
	if client == nil && db == nil {
		return nil
	}
	return usagepg.NewUsageExportRepository(client, db)
}

// provideUsageExportArtifactStore opens the confined local usage-export
// artifact directory when export tasks are enabled. Disabled or nil
// configurations return nil without touching disk, while enabled stores
// register descriptor closure after timing-wheel shutdown.
func provideUsageExportArtifactStore(cfg *config.Config, manager *lifecycle.Manager) (usage.UsageExportArtifactStore, error) {
	if cfg == nil || !cfg.Usage.ExportTasks.Enabled {
		return nil, nil
	}
	store, err := usageprovider.NewLocalExportStore(resolveUsageExportDir(cfg))
	if err != nil {
		return nil, err
	}
	if manager != nil {
		// Close the confined os.Root descriptor only after HTTP downloads, export
		// workers, and recurring timing-wheel sweeps have stopped using it.
		manager.Register(lifecycle.Hook{
			Name:       "UsageExportArtifactStore",
			StartOrder: 175,
			StopOrder:  shutdownStageTimingWheel + 5,
			Stop: func(context.Context) error {
				return store.Close()
			},
		})
	}
	return store, nil
}

// resolveUsageExportDir returns the configured usage-export directory or
// derives <DATA_DIR or storage.dir or ./data>/exports/usage when omitted.
func resolveUsageExportDir(cfg *config.Config) string {
	if cfg != nil {
		if dir := strings.TrimSpace(cfg.Usage.ExportTasks.Dir); dir != "" {
			return dir
		}
	}
	base := strings.TrimSpace(os.Getenv("DATA_DIR"))
	if base == "" && cfg != nil {
		base = strings.TrimSpace(cfg.Storage.Dir)
	}
	if base == "" {
		base = "./data"
	}
	return filepath.Join(base, "exports", "usage")
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

// provideUsageExport constructs background usage CSV export workers with shared
// task persistence, keyset log scanning, confined local storage, balance-unit
// display settings, and timing-wheel sweeps. Nil store or wheel pointers stay
// untyped nil interfaces so unconfigured fixtures do not receive typed-nil dependencies.
func provideUsageExport(repo usage.UsageExportRepository, store *usagepg.Store, artifacts usage.UsageExportArtifactStore, settingsStore *settings.Store, wheel *timingwheel.Wheel, options *usage.Options) *usage.UsageExportService {
	var rowReader usage.UsageExportRowReader
	if store != nil {
		rowReader = store
	}
	var tw usage.TimingWheel
	if wheel != nil {
		tw = wheel
	}
	return usage.NewUsageExportService(repo, rowReader, artifacts, publicBalanceUnit{settingsStore}, tw, options)
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
