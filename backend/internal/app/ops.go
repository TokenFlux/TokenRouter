package app

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"runtime"
	"runtime/debug"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keyhttp "github.com/TokenFlux/TokenRouter/internal/apikey/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	gatewayhttp "github.com/TokenFlux/TokenRouter/internal/gateway/httpapi"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/notification"
	"github.com/TokenFlux/TokenRouter/internal/ops"
	"github.com/TokenFlux/TokenRouter/internal/ops/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/ops/maintenance"
	opspostgres "github.com/TokenFlux/TokenRouter/internal/ops/postgres"
	opsadapter "github.com/TokenFlux/TokenRouter/internal/ops/provider"
	opsredis "github.com/TokenFlux/TokenRouter/internal/ops/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/server/middleware"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
)

// ops.go assembles operational queries, collectors, alerting, reporting, and
// release maintenance services. Ops shares provider, user, concurrency, quota,
// and authentication observers with the application. Error recording receives a
// separately owned queue, while the services receive SQL, Redis, settings, and
// external adapters during construction.

// provideOpsErrorQueue constructs error recording and registers its owned
// shutdown before lifecycle startup. Processor count comes from current
// GOMAXPROCS, and queue creation is distinct from its lazy worker admission on
// first enqueue.
func provideOpsErrorQueue(manager *lifecycle.Manager) *ops.ErrorLogQueue {
	q := ops.NewErrorLogQueue(ops.ErrorLogQueueOptions{Processors: func() int { return runtime.GOMAXPROCS(0) }, Logf: log.Printf, Stack: debug.Stack})
	// Error records may arrive through request and completion shutdown phases.
	// Drain their owned queue before later shared storage closes at the registered cleanup level.
	manager.Register(lifecycle.Hook{Name: "OpsErrorLogWorkers", StartOrder: 924, StopOrder: 76, Stop: q.Shutdown})
	return q
}

// provideOpsRepository binds operations persistence to the application SQL
// connection pool.
func provideOpsRepository(db *sql.DB) ops.OpsRepository { return opspostgres.NewOpsRepository(db) }

// provideOpsService constructs operations queries and installs aggregation,
// quota, and authentication observers. quota is required; it is warmed with a
// background context during assembly, and its sink receives later Ops quota
// settings.
func provideOpsService(repo ops.OpsRepository, settings *settings.Store, options *ops.Options, providers *providerpostgres.ProviderStore, users *identitypostgres.UserStore, c *scheduler.ConcurrencyService, sink *ops.OpsSystemLogSink, quota *provider.QuotaSettingsCache, worker *apikey.AuthCacheInvalidationWorker, keys *apikey.APIKeyService, pre *preaggregation.PreAggregationSettingsService) *ops.OpsService {
	s := ops.NewOpsService(repo, settings, options, opsProviderReader{providers}, opsUsers{users}, c, sink, opsadapter.LogControl{})
	s.SetPreAggregationSettings(pre)
	s.SetOpenAIQuotaAutoPauseSettingsSink(quota.SetOpenAIQuotaAutoPauseSettings)
	quota.WarmOpenAIQuotaAutoPauseSettings(context.Background())
	s.SetAuthObservers(worker, keys)
	return s
}

// provideOpsCollector combines shared runtime metrics with database, Redis, and
// host observations.
func provideOpsCollector(repo ops.OpsRepository, settings *settings.Store, providers *providerpostgres.ProviderStore, c *scheduler.ConcurrencyService, db *sql.DB, r *redis.Client, options *ops.Options) *ops.OpsMetricsCollector {
	return ops.NewOpsMetricsCollector(repo, settings, opsProviderReader{providers}, c, opspostgres.NewMetricsQueries(db), opsredis.NewRuntime(r), options, opsadapter.NewHostObserver(db, r))
}

// provideOpsAggregation binds Ops aggregation to settings, advisory
// coordination, and Redis runtime.
func provideOpsAggregation(repo ops.OpsRepository, settings *settings.Store, db *sql.DB, r *redis.Client, options *ops.Options, pre *preaggregation.PreAggregationSettingsService) *ops.OpsAggregationService {
	return ops.NewOpsAggregationService(repo, settings, opspostgres.NewAdvisory(db), opsredis.NewRuntime(r), options, pre)
}

// provideOpsEvaluator constructs alert evaluation with shared Ops data and
// notification delivery.
func provideOpsEvaluator(s *ops.OpsService, repo ops.OpsRepository, email *notification.Mailer, notifications *notification.NotificationEmailService, r *redis.Client, options *ops.Options, proxies *egresspostgres.ProxyStore) *ops.OpsAlertEvaluatorService {
	return ops.NewOpsAlertEvaluatorService(s, repo, notificationOpsDelivery(email, notifications), opsredis.NewRuntime(r), options, proxies)
}

// provideOpsCleanup constructs data cleanup and attaches its reloader to Ops
// administration.
func provideOpsCleanup(repo ops.OpsRepository, settings *settings.Store, s *ops.OpsService, db *sql.DB, r *redis.Client, options *ops.Options) *ops.OpsCleanupService {
	c := ops.NewOpsCleanupService(repo, opspostgres.NewCleanupStore(db), opsredis.NewRuntime(r), options, settings)
	s.SetCleanupReloader(c)
	return c
}

// provideOpsReports combines scheduled reports with user reads and notification
// delivery.
func provideOpsReports(s *ops.OpsService, users *identitypostgres.UserStore, email *notification.Mailer, notifications *notification.NotificationEmailService, r *redis.Client, options *ops.Options) *ops.OpsScheduledReportService {
	return ops.NewOpsScheduledReportService(s, opsUsers{users}, notificationOpsDelivery(email, notifications), opsredis.NewRuntime(r), options)
}

// provideOpsIngress attaches ingress aggregation when the repository supports
// its persistence interface. Missing capability returns nil, and a constructed
// aggregator is installed on the shared Ops service before returning.
func provideOpsIngress(repo ops.OpsRepository, s *ops.OpsService) *ops.OpsIngressRejectAggregator {
	r, ok := repo.(ops.OpsIngressRejectRepository)
	if !ok {
		return nil
	}
	a := ops.NewOpsIngressRejectAggregator(r)
	s.SetIngressRejectAggregator(a)
	return a
}

// provideReleaseClient captures release proxy policy and the configured GitHub token
// from process configuration. cfg is required; the selected
// direct-on-proxy-error flag is passed to the release adapter for its request
// behavior.
func provideReleaseClient(cfg *config.Config) opsadapter.ReleaseClient {
	return opsadapter.NewReleaseClient(opsadapter.ReleaseOptions{ProxyURL: cfg.OutboundProxy.URL, AllowDirectOnProxyError: cfg.OutboundProxy.AllowDirectOnError, GitHubToken: cfg.Update.GitHubToken})
}

// provideReleaseQuery binds cached release lookup with the running build
// metadata.
func provideReleaseQuery(cache ops.UpdateCache, client opsadapter.ReleaseClient, info BuildInfo) *ops.ReleaseQuery {
	return ops.NewReleaseQuery(cache, client, info.Version, info.BuildType)
}

// provideUpdateMaintenance combines release lookup and binary installation for
// system maintenance.
func provideUpdateMaintenance(query *ops.ReleaseQuery, client opsadapter.ReleaseClient) *maintenance.UpdateService {
	return maintenance.NewUpdateService(query, opsadapter.NewBinaryInstaller(client, nil))
}

// provideOpsOptions returns nil for missing cfg or copied runtime observation
// options. It recognizes both SQL and Ops not-found errors, configures
// completion logging, and captures monitor, aggregation, cleanup, connection,
// and process-log settings.
func provideOpsOptions(cfg *config.Config) *ops.Options {
	if cfg == nil {
		return nil
	}
	o := &ops.Options{Timezone: cfg.Timezone, IsNotFound: func(e error) bool { return errors.Is(e, sql.ErrNoRows) || errors.Is(e, ops.ErrRowNotFound) }, Logf: func(f string, a ...any) { logger.LegacyPrintf("service.ops", f, a...) }}
	o.CleanupCompleted = func(counts string) {
		logger.L().Info("[OpsCleanup] cleanup complete", zap.String("component", "service.ops_cleanup"), zap.String("deleted_counts", counts))
	}
	o.Ops.Enabled = cfg.Ops.Enabled
	o.Ops.Aggregation.Enabled = cfg.Ops.Aggregation.Enabled
	o.Ops.Cleanup = ops.CleanupOptions(cfg.Ops.Cleanup)
	o.Database.MaxOpenConns = cfg.Database.MaxOpenConns
	o.Redis.PoolSize = cfg.Redis.PoolSize
	o.Log = ops.LogOptions{Level: cfg.Log.Level, Caller: cfg.Log.Caller, StacktraceLevel: cfg.Log.StacktraceLevel, Sampling: ops.SamplingOptions(cfg.Log.Sampling)}
	return o
}

// provideOpsObservationAccess binds Key lookup and typed ingress rejection for
// diagnostic recording. Effective request Keys take precedence; absent
// effective access uses the Ops fallback Key, whose loaded data supplies
// observations rather than authentication authority.
func provideOpsObservationAccess() gatewayhttp.OpsObservationAccess {
	return gatewayhttp.OpsObservationAccess{
		APIKey: func(c *gin.Context) *apikey.APIKey {
			if key, ok := gatewayhttp.EffectiveAPIKey(c); ok && key != nil {
				return key
			}
			// Authentication can fail after loading a Key. Use that separately
			// stored observation value when effective access is absent, keeping diagnostic attribution available after rejection.
			key, _ := keyhttp.GetOpsFallbackAPIKey(c)
			return key
		},
		Rejected: func(c *gin.Context) bool {
			_, rejected := middleware.GetIngressRejectReason(c)
			return rejected
		},
	}
}

// provideOpsWSOptions copies validated process settings into the HTTP adapter.
// The handler parses and owns its proxy prefixes, so its handshake policy is
// isolated from other handlers and from the caller's configuration slice storage.
func provideOpsWSOptions(cfg *config.Config) httpapi.OpsWSOptions {
	value := cfg.Ops.WebSocket
	return httpapi.OpsWSOptions{
		TrustProxy:     value.TrustProxy,
		TrustedProxies: value.TrustedProxies,
		OriginPolicy:   value.OriginPolicy,
		MaxConns:       value.MaxConns,
		MaxConnsPerIP:  value.MaxConnsPerIP,
	}
}
