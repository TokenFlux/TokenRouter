package app

import (
	"context"
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egresshttp "github.com/TokenFlux/TokenRouter/internal/egress/httpapi"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	egressprovider "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// egress.go constructs proxy administration, probes, transfers, expiry checks,
// and TLS services.
// Proxy edits can affect linked providers and scheduler visibility. The proxy
// store receives transaction participants for those changes, while probing,
// administrative tasks, and TLS collection use configured network policy and
// the application's shared task tracker.

// provideEgressProxyStore binds proxy changes to provider mutations and
// scheduler outbox writes. Each callback receives the store's executor so
// linked changes can participate in the same database operation.
func provideEgressProxyStore(client *dbent.Client, db *sql.DB) *egresspostgres.ProxyStore {
	return egresspostgres.NewProxyStore(client, db, egresspostgres.ProxyStoreOptions{
		Providers: func(exec postgresinfra.Executor) egresspostgres.ProxyProviderParticipant {
			return providerpostgres.ProxyChangesInTx(exec)
		},
		Enqueue: func(ctx context.Context, exec postgresinfra.Executor, payload any) error {
			return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, scheduler.SchedulerOutboxEventProviderBulkChanged, nil, nil, payload)
		},
	})
}

// provideEgressProbe constructs proxy probes using configured targets and
// destination checks. With nil cfg, resolved-IP validation starts enabled; a
// supplied cfg controls that validation, private hosts, TLS verification, and
// the response-size limit.
func provideEgressProbe(cfg *config.Config) egress.ProxyExitInfoProber {
	options := egressprovider.ProxyProbeOptions{ValidateResolvedIP: true}
	if cfg != nil {
		options.AllowPrivateHosts = cfg.Security.URLAllowlist.AllowPrivateHosts
		options.ValidateResolvedIP = cfg.Security.URLAllowlist.Enabled
		options.MaxResponseBytes = cfg.Egress.ProxyProbe.ResponseMaxBytes
		for _, target := range cfg.Egress.ProxyProbe.URLs {
			options.Targets = append(options.Targets, egressprovider.ProxyProbeTarget{URL: target.URL, Parser: target.Parser})
		}
	}
	return egressprovider.NewProxyExitInfoProber(options)
}

// provideEgressAdmin connects proxy administration to probing and tracked
// background work.
func provideEgressAdmin(store *egresspostgres.ProxyStore, prober egress.ProxyExitInfoProber, cache egress.ProxyLatencyCache, tasks *lifecycle.Tasks) *egress.ProxyAdmin {
	return egress.NewProxyAdmin(store, prober, cache, egressprovider.ProxyQualityHTTP{}, egress.ProxyAdminOptions{Now: time.Now, Tasks: tasks, Diagnostics: egress.Diagnostics{Logf: logging.LegacyPrintf}})
}

// provideEgressProfiles binds persisted TLS profiles to their shared cache.
func provideEgressProfiles(repo egress.TLSFingerprintProfileRepository, cache egress.TLSFingerprintProfileCache) *egress.TLSFingerprintProfileService {
	return egress.NewTLSFingerprintProfileService(repo, cache, egress.Diagnostics{Logf: logging.LegacyPrintf})
}

// provideEgressRouters binds persisted TLS routing rules to their cache.
func provideEgressRouters(repo egress.TLSFingerprintRouterRepository, cache egress.TLSFingerprintRouterCache) *egress.TLSFingerprintRouterService {
	return egress.NewTLSFingerprintRouterService(repo, cache, egress.Diagnostics{Logf: logging.LegacyPrintf})
}

// provideEgressCollector constructs TLS fingerprint collection with the system
// clock. A supplied cfg sets listener, certificate, session, and record
// options; listener lifetime is managed separately by the collector and
// application lifecycle.
func provideEgressCollector(cfg *config.Config) *egressprovider.TLSFingerprintCollectorService {
	options := egressprovider.CollectorOptions{Now: time.Now, Diagnostics: egress.Diagnostics{Logf: logging.LegacyPrintf}}
	if cfg != nil {
		v := cfg.Egress.TLSFingerprintCollector
		options.Host = v.Host
		options.Port = v.Port
		options.PublicBaseURL = v.PublicBaseURL
		options.CertFile = v.CertFile
		options.KeyFile = v.KeyFile
		options.SessionTTLSeconds = v.SessionTTLSeconds
		options.MaxRecordsPerSession = v.MaxRecordsPerSession
	}
	return egressprovider.NewTLSFingerprintCollectorService(options)
}

// provideEgressProfileHTTP joins profile administration and fingerprint
// collection for HTTP.
func provideEgressProfileHTTP(core *egress.TLSFingerprintProfileService, collector *egressprovider.TLSFingerprintCollectorService) *egresshttp.TLSFingerprintProfileHandler {
	return egresshttp.NewTLSFingerprintProfileHandler(core, collector)
}

// provideProxyTransfer binds proxy transfers to tracked tasks and system time.
func provideProxyTransfer(admin *egress.ProxyAdmin, tasks *lifecycle.Tasks) *egress.ProxyTransfer {
	return egress.NewProxyTransfer(admin, tasks, time.Now)
}

// provideProxyHTTP exposes proxy administration and transfer operations through
// HTTP.
func provideProxyHTTP(admin *egress.ProxyAdmin, transfers *egress.ProxyTransfer) *egresshttp.ProxyHandler {
	return egresshttp.NewProxyHandler(admin, transfers)
}

// provideProxyExpiry constructs one-minute proxy expiry checks for lifecycle
// startup.
func provideProxyExpiry(repo egress.ProxyRepository) *egress.ProxyExpiryService {
	return egress.NewProxyExpiryService(repo, time.Minute)
}
