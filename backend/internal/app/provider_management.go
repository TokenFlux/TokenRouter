package app

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/google/uuid"
	"github.com/imroc/req/v3"

	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/egress"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	egressadapter "github.com/TokenFlux/TokenRouter/internal/egress/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/httpclient"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerhttp "github.com/TokenFlux/TokenRouter/internal/provider/httpapi"
	"github.com/TokenFlux/TokenRouter/internal/provider/modelrules"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	provideradapter "github.com/TokenFlux/TokenRouter/internal/provider/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
	"github.com/TokenFlux/TokenRouter/internal/upstream/openai"
	usagetypes "github.com/TokenFlux/TokenRouter/internal/usage"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// provider_management.go assembles provider administration, model queries,
// privacy operations, and runtime presentation. Provider management combines
// current runtime status, stored configuration, model catalogs, privacy, and
// coordinated refresh. New provider snapshots enter the import probe queue,
// while model and privacy query services register their own waits before shared
// transport and storage are closed.

// providerGroupReferences adapts routing group data for provider administration
// rules.
type providerGroupReferences struct {
	store *routingpostgres.GroupStore // routing group references
}

// providePrivacyClientFactory returns shared req clients using a thirty-second
// timeout and impersonation. Each invocation passes its proxy URL to the shared
// pool; application transport cleanup owns idle connections reused by those
// clients.
func providePrivacyClientFactory() openai.PrivacyClientFactory {
	return func(proxyURL string) (*req.Client, error) {
		return httpclient.GetSharedReqClient(httpclient.ReqClientOptions{
			ProxyURL:    proxyURL,
			Timeout:     30 * time.Second,
			Impersonate: true,
		})
	}
}

// provideProviderAdmin binds provider writes, quota counters, privacy, groups,
// and creation hooks. tasks owns background administration work, and the shared
// store supplies configuration plus duplicate reads before the constructed
// admin is returned.
func provideProviderAdmin(store *providerpostgres.ProviderStore, usage *billingpostgres.ProviderUsageStore, blocker provider.RuntimeUnblocker, privacy *provider.PrivacyService, groups *routingpostgres.GroupStore, proxies *egresspostgres.ProxyStore, tasks *lifecycle.Tasks, upstream httpclient.UpstreamTransport, tls *egressadapter.TLSProfiles) *provider.Admin {
	return provider.NewAdmin(store, provider.AdminOptions{ShadowModels: provideradapter.DefaultSparkShadowModels, Duplicates: store, Quotas: usage, RuntimeBlocker: blocker, Privacy: privacy, Groups: providerGroupReferences{groups}, Proxies: proxies, Creation: provider.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString}, Credentials: provideradapter.CreateCredentialHooks(upstream, tls), Background: tasks.Go, Error: slog.Error})
}

// GetGroup returns identity and OAuth-only policy for one stored routing group.
// A nil stored result remains nil, while an available group reference is
// returned with the original repository error.
func (g providerGroupReferences) GetGroup(ctx context.Context, id int64) (*provider.GroupReference, error) {
	value, err := g.store.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return &provider.GroupReference{ID: value.ID, Name: value.Name, RequireOAuthOnly: value.RequireOAuthOnly}, err
}

// ActiveGroups converts all active routing groups and ignores the platform
// argument. Nil repository results remain nil, and available identity plus
// OAuth-only flags are returned alongside the read error.
func (g providerGroupReferences) ActiveGroups(ctx context.Context, platform string) ([]provider.GroupReference, error) {
	rows, err := g.store.ListActive(ctx)
	if rows == nil {
		return nil, err
	}
	out := make([]provider.GroupReference, len(rows))
	for i, v := range rows {
		out[i] = provider.GroupReference{ID: v.ID, Name: v.Name, RequireOAuthOnly: v.RequireOAuthOnly}
	}
	return out, err
}

// ValidateGroups delegates group-ID existence checks to routing with the shared
// store.
func (g providerGroupReferences) ValidateGroups(ctx context.Context, ids []int64) error {
	return routing.ValidateGroupIDs(ctx, g.store, ids)
}

// provideProviderManagement binds administration, reports, runtime
// presentation, and batch operations for HTTP. Report dates use the supplied
// calendar, query errors retain their cause, and both single and batch creation
// schedule captured provider snapshots through the shared import probe queue.
func provideProviderManagement(admin *provider.Admin, presenter *providerhttp.RuntimePresenter, ollama *provider.OllamaCloudUsageService, privacy *provider.PrivacyService, probes *provider.GrokImportProbeScheduler, quota *provider.GrokQuotaService, managed *provider.ManagedRefreshService, tasks *lifecycle.Tasks, recovery *provider.RecoveryService, listing *provider.ManagementList, catalog *routing.AdminCatalog, models *provider.ModelSyncService, usage *usagepostgres.Store, calendar timezone.Calendar) *providerhttp.ManagementHandler {
	query := func(ctx context.Context, id int64, start, end time.Time) (*usagetypes.ProviderUsageStatsResponse, error) {
		value, err := usage.GetProviderUsageStats(ctx, id, start, end)
		if err != nil {
			return nil, fmt.Errorf("get provider usage stats failed: %w", err)
		}
		return value, nil
	}
	return providerhttp.NewManagementHandler(admin, providerhttp.ManagementOptions{Models: models, Reports: providerhttp.ProviderReportOptions{Now: calendar.Now, StartOfDay: calendar.StartOfDay, Query: query}, Catalog: catalog, ModelDefaults: provideradapter.ModelDefaults(), ModelRules: func(v *provider.Record) routing.CatalogueRules {
		return (gatewayprovider.ModelPolicy{Record: v}).CatalogueRules()
	}, List: listing, RuntimePresenter: presenter, Recovery: recovery, Batch: provider.NewManagementBatch(admin, managed, provider.ManagementCreationOptions{Privacy: privacy, Background: tasks.Go, AfterCreate: func(v *provider.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(grokImportQuotaProbe{source: quota}, &snapshot)
	}, Error: slog.Error}), Managed: managed, Presenter: presenter, Ollama: ollama, Privacy: privacy, AfterCreate: func(v *provider.Record) {
		if v == nil {
			return
		}
		snapshot := v.RoutingSnapshot()
		probes.Schedule(grokImportQuotaProbe{source: quota}, &snapshot)
	}})
}

// provideAdminModelCatalog binds administrator model queries to the shared
// catalog service.
func provideAdminModelCatalog(catalog *catalogprovider.Service) *routing.AdminCatalog {
	return routing.NewAdminCatalog(routingprovider.AdminCatalogOptions(catalog))
}

// provideProviderModelSync constructs credential-aware model queries and
// registers their shutdown wait. cfg and manager are required; operator hosts
// are cloned, response bytes use the configured model-read limit, and the
// shared task coordinator handles agent identity preparation. The live catalog
// supplies OpenCode protocol suggestions, which use bundled tables until a
// catalog is published.
func provideProviderModelSync(catalog *catalogprovider.Service, store *providerpostgres.ProviderStore, transport httpclient.UpstreamTransport, claude *provider.ClaudeTokenSource, gemini *provider.GeminiTokenSource, grok *provider.GrokTokenSource, antigravity *provider.AntigravityTokenSource, qoderCatalog *provider.QoderCatalogService, profiles *egressadapter.TLSProfiles, tasks *provideradapter.ProbeTasks, settings *gateway.RuntimeSettings, cfg *config.Config, manager *lifecycle.Manager) *provider.ModelSyncService {
	policy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	limit := resolveModelsListReadLimit(cfg)
	queries := &provideradapter.ModelCatalogue{
		Transport: transport, Profiles: profiles, ClaudeTokens: claude, GeminiTokens: gemini, GrokTokens: grok, AntigravityTokens: antigravity, DefaultGrokBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings), Read: store.GetByID, EnsureTask: tasks.Ensure,
		Options: provideradapter.ModelCatalogueOptions{ValidateURL: policy.Validate, OperatorValidator: policy.Validate, BodyLimit: limit, CodexModelsURL: provideradapter.DefaultCodexModelsURL},
	}
	core := provider.NewModelSyncService(queries.FetchUpstreamSupportedModels)
	core.Qoder = qoderCatalog
	core.Suggest = provider.OpenCodeProtocolSuggestions(func(mode string) map[string]string {
		return modelrules.NewView(catalog.Publication()).OpenCodeProtocols(mode)
	})
	manager.Register(lifecycle.Hook{Name: "ProviderModelList", StopOrder: 26, Stop: core.StopContext})
	return core
}

// provideProviderPrivacy constructs privacy operations using shared stores and
// platform clients. Call before lifecycle startup; its registered stop cancels
// and waits at level twenty-six before HTTP idle connections and storage
// cleanup.
func provideProviderPrivacy(store *providerpostgres.ProviderStore, proxies *egresspostgres.ProxyStore, factory openai.PrivacyClientFactory, manager *lifecycle.Manager) *provider.PrivacyService {
	core := provider.NewPrivacyService(store, proxies, provideradapter.PrivacyOptions(factory, openai.PrivacyEndpoints{}))
	manager.Register(lifecycle.Hook{Name: "ProviderPrivacy", StopOrder: 26, Stop: core.StopContext})
	return core
}

// provideProviderRuntimePresenter combines shared batch runtime readers with
// provider and Ollama presentation.
func provideProviderRuntimePresenter(admin *provider.Admin, ollama *provider.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *provider.QuotaSettingsCache) *providerhttp.RuntimePresenter {
	return providerhttp.NewRuntimePresenter(provider.NewRuntimeStatusReader(provideradapter.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), admin, ollama)
}

// provideProviderManagementList combines administrator list reads with runtime
// status and scheduler feedback. Score options use the same shared state as
// execution; settings and startup configuration are supplied to that option
// adapter.
func provideProviderManagementList(admin *provider.Admin, ollama *provider.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *provider.QuotaSettingsCache, shared *schedulerSharedState, store *settingscore.Store, cfg *config.Config) *provider.ManagementList {
	return provider.NewManagementList(admin, provider.NewRuntimeStatusReader(provideradapter.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), provider.NewSchedulerScoreView(admin, providerScoreOptions(concurrency, shared, store, cfg)), ollama)
}
