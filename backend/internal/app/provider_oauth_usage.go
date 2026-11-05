package app

import (
	"context"
	"database/sql"
	"log"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/upstream/anthropic"
	openaiws "github.com/TokenFlux/TokenRouter/internal/upstream/openai/ws"
	"github.com/TokenFlux/TokenRouter/internal/upstream/qoder"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// provider_oauth_usage.go assembles platform usage queries, local usage
// statistics, and periodic quota monitors. Provider usage presentation combines
// platform queries with local statistics and shared display caching. OAuth
// queries register service activity shutdown, API-key and Ollama monitors receive
// singleton coordination, and platform adapters keep credential preparation,
// TLS, and destination policy attached to application resources.

// provideUsageMonitor binds stored providers and upstream queries with
// instance-specific singleton coordination. Nil cfg starts from its half-unit
// balance threshold input; configured intervals and budgets are converted to
// durations, and SQL advisory acquisition is attached only when db is present.
func provideUsageMonitor(store *providerpostgres.ProviderStore, queries *provider.UpstreamUsageService, cfg *config.Config, leader provider.LeaderLocker, db *sql.DB) *provider.UsageMonitor {
	// The default reported-balance cutoff gates usage monitor temporary provider pauses.
	options := provider.UsageMonitorOptions{Now: time.Now, InstanceID: uuid.NewString(), BalanceThreshold: 0.5, Leader: leader, Warn: slog.Warn, Debug: slog.Debug}
	if cfg != nil {
		v := cfg.Provider.UsageMonitor
		options.Enabled = v.Enabled
		// Monitor intervals use configured minutes; probe and round budgets use
		// seconds. Convert them together before the monitor normalizes its runtime options.
		options.Interval = time.Duration(v.IntervalMinutes) * time.Minute
		options.ProbeTimeout = time.Duration(v.ProbeTimeoutSeconds) * time.Second
		options.RoundTimeout = time.Duration(v.RoundTimeoutSeconds) * time.Second
		options.Concurrency = v.Concurrency
		options.BalanceThreshold = v.PauseBelowBalance
		h := cfg.Security.URLAllowlist
		options.HostPolicy = egress.MonitorHostPolicy{Enabled: h.Enabled, AllowInsecureHTTP: h.AllowInsecureHTTP, AllowPrivate: h.AllowPrivateHosts, AllowedHosts: h.UpstreamHosts}
	}
	if db != nil {
		options.Advisory = func(ctx context.Context, key string) (func(), bool) {
			return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, postgresinfra.HashAdvisoryLockID(key))
		}
	}
	return provider.NewUsageMonitor(store, queries, options)
}

// provideOAuthUsageStats binds local provider statistics and display cache to
// the calendar.
func provideOAuthUsageStats(store *usagepostgres.Store, cache *provider.OAuthUsageCache, calendar timezone.Calendar) *provider.LocalUsageStatistics {
	return provider.NewLocalUsageStatistics(newProviderLocalUsageStats(store), cache, provider.LocalUsageStatisticsOptions{Now: time.Now, Today: calendar.Today, Log: log.Printf})
}

// provideOAuthUsageCore assembles OAuth platform readers and registers the
// service activity stop hook. It creates a separate Qoder token/session
// provider for quota queries, binds conditional agent credential preparation
// and connection invalidation, and adapts local Gemini and Grok observations.
// The registered hook calls OAuthUsageService.StopContext for its accepted
// query activity. Qoder shared session builds can outlive a canceled query
// waiter and remain owned by the separate token provider. This assembly leaves
// that provider's StopContext unbound.
func provideOAuthUsageCore(store *providerpostgres.ProviderStore, usageStore *usagepostgres.Store, cache *provider.OAuthUsageCache, stats *provider.LocalUsageStatistics, gemini *provider.GeminiQuotaService, antigravity *provider.AntigravityQuota, grokView *provider.GrokQuotaView, grok *provider.GrokQuotaService, openAI *provider.OpenAIQuotaService, fetcher provideradapter.ClaudeUsageClient, fingerprints anthropic.FingerprintCache, profiles *egressprovider.TLSProfiles, transport httpclient.UpstreamTransport, settings *provider.QuotaSettingsCache, connections *openaiws.OpenAIWSConnections, manager *lifecycle.Manager, coordinator *provider.OpenAITaskCoordinator, system *provideradapter.SystemUsageTransport) *provider.OAuthUsageService {
	if system != nil {
		transport = system
	}

	taskOptions := provider.OpenAITaskOptions{
		Read: store.GetByID,
		Register: func(ctx context.Context, value *provider.Record) (string, error) {
			return provideradapter.RegisterAgentIdentityTask(ctx, value, "https://auth.openai.com/api/accounts")
		},
		Persist: func(ctx context.Context, value *provider.Record, credentials map[string]any) error {
			_, err := provider.PersistCredentials(ctx, store, value, credentials, slog.Warn)
			return err
		},
		Invalidate: connections.InvalidateProvider,
	}
	requests := &provideradapter.OAuthUsageTransport{
		Transport: transport, Profiles: profiles, Fingerprints: fingerprints,
		Tasks: coordinator, TaskOptions: taskOptions,
	}
	// Quota queries use their own Qoder token/session provider. Its Fetch
	// callback is passed to OAuthUsageService; service activity shutdown is registered below.
	sessions := provideradapter.NewQoderTokenProvider(qoder.SessionBuilder{})
	sessions.SetHTTPUpstream(transport, profiles)
	qoderQuery := &provideradapter.QoderUsage{Sessions: sessions, Transport: transport, Profiles: profiles}
	qoderOptions := qoderQuery.Options()
	qoderOptions.Enrich = provideradapter.EnrichUsageWithProviderError
	options := provider.OAuthUsageOptions{
		Now: time.Now, Jitter: rand.Int64N, Log: log.Printf, Warn: slog.Warn,
		Qoder: qoderOptions,
		Anthropic: func(ctx context.Context, value *provider.Record) (*provider.ClaudeUsageResponse, error) {
			return requests.FetchAnthropic(ctx, value, fetcher)
		},
		OpenAI: provider.OpenAIUsageOptions{
			Probe: requests.ProbeOpenAI,
			Shadow: func(ctx context.Context, id int64, now time.Time) (map[string]any, error) {
				value, err := openAI.QueryUsage(ctx, id)
				if err != nil {
					return nil, err
				}
				return provider.BuildCodexSparkWindowExtraUpdates(value, now), nil
			},
		},
		OpenAIQuotaPause: func(ctx context.Context, value *provider.Record, info *provider.UsageInfo) {
			if value != nil && info != nil && value.IsOpenAI() {
				info.QuotaAutoPaused, _ = provider.EvaluateQuotaAutoPause(value.Platform, value.Extra, settings.GetOpenAIQuotaAutoPauseSettings(ctx), time.Now())
			}
		},
		Antigravity: provider.AntigravityUsageOptions{
			CanFetch: antigravity.CanFetch,
			Fetch: func(ctx context.Context, value *provider.Record) (*provider.UsageInfo, error) {
				result, err := antigravity.FetchQuota(ctx, value, antigravity.GetProxyURL(ctx, value))
				if result == nil {
					return nil, err
				}
				return result.UsageInfo, err
			},
			Degrade: provideradapter.AntigravityDegradedUsage, Enrich: provideradapter.EnrichUsageWithProviderError,
		},
		Gemini: provider.GeminiUsageOptions{
			Location: geminiQuotaLocation, Quota: gemini.QuotaForProvider,
			Totals: func(ctx context.Context, id int64, start, end time.Time) (provider.GeminiUsageTotals, error) {
				rows, err := usageStore.GetModelStatsWithFilters(ctx, start, end, 0, 0, id, 0, nil, nil, nil)
				if err != nil {
					return provider.GeminiUsageTotals{}, err
				}
				values := projectGeminiModelUsage(rows)
				return provider.AggregateGeminiUsage(values), nil
			},
		},
		Grok: provider.GrokUsageOptions{
			Available: func() bool { return true }, StatsAvailable: func() bool { return true },
			Build: grokView.BuildUsageInfo, Enrich: provideradapter.EnrichUsageWithProviderError,
			Probe: func(ctx context.Context, id int64) (*provider.GrokUsageProbe, error) {
				value, err := grok.ProbeBilling(ctx, id)
				if value == nil {
					return nil, err
				}
				return &provider.GrokUsageProbe{Billing: value.Billing, LocalUsage24h: value.LocalUsage24h, LocalUsage7d: value.LocalUsage7d, LocalUsageMonthly: value.LocalUsageMonthly}, err
			},
		},
	}
	core := provider.NewOAuthUsageService(store, cache, stats, options)
	manager.Register(lifecycle.Hook{Name: "ProviderOAuthUsage", StopOrder: shutdownStageProbesAndRefresh, Stop: core.StopContext})
	return core
}

// geminiQuotaLocation returns America/Los_Angeles when the location database
// can load it. Otherwise it returns fixed PST at UTC minus eight, retaining
// defined quota dates without daylight-saving transitions.
func geminiQuotaLocation() *time.Location {
	location, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		// Keep quota dates available when zone loading fails. The fixed UTC-minus-eight
		// fallback has no daylight-saving adjustment, unlike the normal Los Angeles location.
		return time.FixedZone("PST", -8*3600)
	}
	return location
}

// provideOllamaUsage combines shared provider storage, encryption, settings,
// and optional upstream execution. It supplies one instance identity and
// singleton lease callback, adding SQL advisory capability when available; the
// service owns its query and periodic lifetime.
func provideOllamaUsage(store *providerpostgres.ProviderStore, upstream httpclient.UpstreamTransport, settings *provider.RuntimeSettings, cipher identity.SecretEncryptor, cfg *config.Config, leader provider.LeaderLocker, db *sql.DB) *provider.OllamaCloudUsageService {
	var do func(*http.Request, string, int64, int) (*http.Response, error)
	if upstream != nil {
		do = upstream.Do
	}
	var advisory func(context.Context, string) (func(), bool)
	if db != nil {
		advisory = func(ctx context.Context, key string) (func(), bool) {
			return postgresinfra.TryAcquireDBAdvisoryLock(ctx, db, postgresinfra.HashAdvisoryLockID(key))
		}
	}
	options := provider.OllamaUsageOptions{EncryptionKeyConfigured: cfg != nil && cfg.Security.SecretEncryptionKeyConfigured, Now: time.Now, Jitter: rand.Int64N, InstanceID: uuid.NewString(), Fetch: provideradapter.OllamaUsageFetcher(do), Log: func(format string, args ...any) {
		logging.LegacyPrintf("service.ollama_cloud_usage", format, args...)
	}, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return provider.AcquireSingletonLease(ctx, leader, advisory, key, owner, ttl)
	}}
	return provider.NewOllamaCloudUsageService(store, settings, cipher, options)
}

// provideUpstreamUsage binds optional transport and TLS with configured
// operator destination policy. Availability requires both store and transport,
// and its registered stop waits for query activity before the later HTTP and
// database cleanup levels.
func provideUpstreamUsage(store *providerpostgres.ProviderStore, upstream httpclient.UpstreamTransport, cfg *config.Config, tls *egressprovider.TLSProfiles, manager *lifecycle.Manager) *provider.UpstreamUsageService {
	options := provideradapter.UsageHTTPOptions{Available: store != nil && upstream != nil}
	if cfg != nil {
		value := cfg.Security.URLAllowlist
		options.Policy = egress.UsageURLPolicy{Configured: true, Enabled: value.Enabled, AllowInsecureHTTP: value.AllowInsecureHTTP, AllowPrivateHosts: value.AllowPrivateHosts, UpstreamHosts: value.UpstreamHosts}
	}
	if upstream != nil {
		options.Do = upstream.DoWithTLS
	}
	if tls != nil {
		options.ResolveTLS = tls.ResolveRequestTLS
	}
	source := provideradapter.NewUpstreamUsageHTTPExecution(options)
	core := provider.NewUpstreamUsageService(store, source, provider.UpstreamUsageOptions{Now: time.Now})
	// Query activity stops after producer shutdown and before shared SQL or
	// HTTP cleanup. The manager returns an error if this service cannot finish within its budget.
	manager.Register(lifecycle.Hook{Name: "ProviderUpstreamUsage", StopOrder: shutdownStageProbesAndRefresh, Stop: core.StopContext})
	return core
}

// provideUpstreamUsageHTTP exposes assembled upstream usage queries through
// administrator HTTP.
func provideUpstreamUsageHTTP(source *provider.UpstreamUsageService, alerts *provider.BalanceAlertMonitor) *providerhttp.UpstreamUsageHandler {
	return providerhttp.NewUpstreamUsageHandler(source, alerts)
}

// provideProviderUsage constructs shared quota consumption with provider events
// and snapshot publication.
func provideProviderUsage(db *sql.DB, store *providerpostgres.ProviderStore, cache scheduler.SnapshotCache) *billingpostgres.ProviderUsageStore {
	return billingpostgres.NewProviderUsageStore(db, providerUsageEvents(store, cache, db))
}
