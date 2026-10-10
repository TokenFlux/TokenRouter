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

type providerGroupReferences struct{ store *routingpostgres.GroupStore }

// providePrivacyClientFactory 为隐私请求配置超时、Chrome 指纹和共享连接池。
func providePrivacyClientFactory() openai.PrivacyClientFactory {
	return func(proxyURL string) (*req.Client, error) {
		return httpclient.GetSharedReqClient(httpclient.ReqClientOptions{
			ProxyURL:    proxyURL,
			Timeout:     30 * time.Second,
			Impersonate: true,
		})
	}
}

// provideProviderAdmin 绑定共享的提供商管理用例和平台执行接口。
func provideProviderAdmin(store *providerpostgres.ProviderStore, usage *billingpostgres.ProviderUsageStore, blocker provider.RuntimeUnblocker, privacy *provider.PrivacyService, groups *routingpostgres.GroupStore, proxies *egresspostgres.ProxyStore, tasks *lifecycle.Tasks, upstream httpclient.UpstreamTransport, tls *egressadapter.TLSProfiles) *provider.Admin {
	return provider.NewAdmin(store, provider.AdminOptions{ShadowModels: provideradapter.DefaultSparkShadowModels, Duplicates: store, Quotas: usage, RuntimeBlocker: blocker, Privacy: privacy, Groups: providerGroupReferences{groups}, Proxies: proxies, Creation: provider.CreationOptions{Now: time.Now, LoadLocation: time.LoadLocation, NewSeed: uuid.NewString}, Credentials: provideradapter.CreateCredentialHooks(upstream, tls), Background: tasks.Go, Error: slog.Error})
}

func (g providerGroupReferences) GetGroup(ctx context.Context, id int64) (*provider.GroupReference, error) {
	value, err := g.store.GetByID(ctx, id)
	if value == nil {
		return nil, err
	}
	return &provider.GroupReference{ID: value.ID, Name: value.Name, RequireOAuthOnly: value.RequireOAuthOnly}, err
}

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

func (g providerGroupReferences) ValidateGroups(ctx context.Context, ids []int64) error {
	return routing.ValidateGroupIDs(ctx, g.store, ids)
}

// provideProviderManagement 为提供商管理 HTTP 接口绑定管理用例和展示参数。
func provideProviderManagement(admin *provider.Admin, presenter *providerhttp.RuntimePresenter, ollama *provider.OllamaCloudUsageService, privacy *provider.PrivacyService, probes *provider.GrokImportProbeScheduler, quota *provider.GrokQuotaService, managed *provider.ManagedRefreshService, tasks *lifecycle.Tasks, recovery *provider.RecoveryService, listing *provider.ManagementList, catalog *routing.AdminCatalog, tier *provider.TierManagement, models *provider.ModelSyncService, usage *usagepostgres.Store, calendar timezone.Calendar) *providerhttp.ManagementHandler {
	query := func(ctx context.Context, id int64, start, end time.Time) (*usagetypes.ProviderUsageStatsResponse, error) {
		value, err := usage.GetProviderUsageStats(ctx, id, start, end)
		if err != nil {
			return nil, fmt.Errorf("get provider usage stats failed: %w", err)
		}
		return value, nil
	}
	return providerhttp.NewManagementHandler(admin, providerhttp.ManagementOptions{Models: models, Reports: providerhttp.ProviderReportOptions{Now: calendar.Now, StartOfDay: calendar.StartOfDay, Query: query}, Tier: tier, Catalog: catalog, ModelDefaults: provideradapter.ModelDefaults(), ModelRules: func(v *provider.Record) routing.CatalogueRules {
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

// provideAdminModelCatalog 在每次查询时读取平台快照。
func provideAdminModelCatalog(catalog *catalogprovider.Service) *routing.AdminCatalog {
	return routing.NewAdminCatalog(routingprovider.AdminCatalogOptions(catalog))
}

// provideProviderModelSync 为模型目录同步绑定提供商、凭据和请求函数，并管理共享查询实例的启停。
func provideProviderModelSync(store *providerpostgres.ProviderStore, transport httpclient.UpstreamTransport, claude *provider.ClaudeTokenSource, gemini *provider.GeminiTokenSource, grok *provider.GrokTokenSource, antigravity *provider.AntigravityTokenSource, profiles *egressadapter.TLSProfiles, tasks *provideradapter.ProbeTasks, settings *gateway.RuntimeSettings, cfg *config.Config, manager *lifecycle.Manager) *provider.ModelSyncService {
	policy := egress.OperatorURLPolicy{Enabled: cfg.Security.URLAllowlist.Enabled, AllowInsecureHTTP: cfg.Security.URLAllowlist.AllowInsecureHTTP, AllowPrivateHosts: cfg.Security.URLAllowlist.AllowPrivateHosts, UpstreamHosts: slices.Clone(cfg.Security.URLAllowlist.UpstreamHosts)}
	limit := resolveModelsListReadLimit(cfg)
	queries := &provideradapter.ModelCatalogue{
		Transport: transport, Profiles: profiles, ClaudeTokens: claude, GeminiTokens: gemini, GrokTokens: grok, AntigravityTokens: antigravity, DefaultGrokBaseURL: gatewayprovider.GrokDefaultBaseURLReader(settings), Read: store.GetByID, EnsureTask: tasks.Ensure,
		Options: provideradapter.ModelCatalogueOptions{ValidateURL: policy.Validate, OperatorValidator: policy.Validate, BodyLimit: limit, CodexModelsURL: provideradapter.DefaultCodexModelsURL},
	}
	core := provider.NewModelSyncService(queries.FetchUpstreamSupportedModels)
	manager.Register(lifecycle.Hook{Name: "ProviderModelList", StopOrder: 26, Stop: core.StopContext})
	return core
}

// provideProviderPrivacy 绑定管理操作与后台刷新共用的隐私设置组件和平台客户端。
func provideProviderPrivacy(store *providerpostgres.ProviderStore, proxies *egresspostgres.ProxyStore, factory openai.PrivacyClientFactory, manager *lifecycle.Manager) *provider.PrivacyService {
	core := provider.NewPrivacyService(store, proxies, provideradapter.PrivacyOptions(factory, openai.PrivacyEndpoints{}))
	manager.Register(lifecycle.Hook{Name: "ProviderPrivacy", StopOrder: 26, Stop: core.StopContext})
	return core
}

// provideProviderRuntimePresenter 绑定提供商运行状态展示组件。
func provideProviderRuntimePresenter(admin *provider.Admin, ollama *provider.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *provider.QuotaSettingsCache) *providerhttp.RuntimePresenter {
	return providerhttp.NewRuntimePresenter(provider.NewRuntimeStatusReader(provideradapter.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), admin, ollama)
}

// provideProviderManagementList 复用当前调度反馈和技术读取实例，保持列表批量查询。
func provideProviderManagementList(admin *provider.Admin, ollama *provider.OllamaCloudUsageService, concurrency *scheduler.ConcurrencyService, usage *usagepostgres.Store, sessions scheduler.SessionLimitCache, rpm scheduler.RPMCache, settings *provider.QuotaSettingsCache, shared *schedulerSharedState, store *settingscore.Store, cfg *config.Config) *provider.ManagementList {
	return provider.NewManagementList(admin, provider.NewRuntimeStatusReader(provideradapter.RuntimeStatusOptions(concurrency, usage, sessions, rpm, settings)), provider.NewSchedulerScoreView(admin, providerScoreOptions(concurrency, shared, store, cfg)), ollama)
}

// provideProviderTier 绑定共享的提供商配置用例，停止等待计入后台任务预算。
func provideProviderTier(admin *provider.Admin, source *provider.GeminiAuthorization, manager *lifecycle.Manager) *provider.TierManagement {
	core := provider.NewTierManagement(admin, provider.ProviderTierManagementOptions(source))
	manager.Register(lifecycle.Hook{Name: "ProviderTier", StopOrder: 26, Stop: core.StopContext})
	return core
}
