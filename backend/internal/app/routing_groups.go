package app

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/google/uuid"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	apikeypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/config"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/selection"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	providerpostgres "github.com/TokenFlux/TokenRouter/internal/provider/postgres"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	routingadapter "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// routingGroupProviders 转换提供商存储记录，平台默认模型目录按需查询。
type routingGroupProviders struct {
	Store *providerpostgres.ProviderStore
}

func (r routingGroupProviders) GetByIDs(ctx context.Context, ids []int64) ([]routing.GroupProvider, error) {
	values, err := r.Store.GetByIDs(ctx, ids)
	// 批量查询无结果时返回空数组，列表查询的 nil 结果保持为 nil。
	out := make([]routing.GroupProvider, len(values))
	for i, v := range values {
		out[i] = r.project(v)
	}
	return out, err
}

func (r routingGroupProviders) ListSchedulableByGroupID(ctx context.Context, id int64) ([]routing.CatalogueProvider, error) {
	values, err := r.Store.ListSchedulableByGroupID(ctx, id)
	return gatewayprovider.CatalogueProviders(values), err
}

func (r routingGroupProviders) project(v *provider.Record) routing.GroupProvider {
	return routing.GroupProvider{ID: v.ID, Platform: v.Platform, Type: v.Type}
}

// provideRoutingGroupStore 为分组读取入口构造共享存储和同连接事务参与工厂。
func provideRoutingGroupStore(client *dbent.Client, db *sql.DB) *routingpostgres.GroupStore {
	return routingpostgres.NewGroupStore(client, db, routingpostgres.GroupStoreOptions{
		Providers: func(exec postgresinfra.Executor) routingpostgres.GroupLinkParticipant {
			return providerpostgres.GroupLinksInTx(exec)
		},
		Users: func(exec postgresinfra.Executor) routingpostgres.GroupAccessParticipant {
			return identitypostgres.GroupAccessDeletionInTx(exec)
		},
		Enqueue: func(ctx context.Context, exec postgresinfra.Executor, id *int64) error {
			return schedulerpostgres.EnqueueSchedulerChange(ctx, exec, scheduler.SchedulerOutboxEventGroupChanged, nil, id, nil)
		},
	})
}

func provideGroupReader(store *routingpostgres.GroupStore) routing.GroupRepository {
	return store
}

func provideRoutingGroupAdmin(catalog *catalogprovider.Service, store *routingpostgres.GroupStore, providers *providerpostgres.ProviderStore, keys *apikeypostgres.KeyStore, invalidator apikey.APIKeyAuthCacheInvalidator, modelConfigs *routing.PricingConfigService, settings *settingscore.Store, defaults *scheduler.AdminDefaults) *routing.GroupAdmin {
	return routing.NewGroupAdmin(store, store, store, routingGroupProviders{Store: providers}, keys, invalidator, modelConfigs, routing.GroupAdminOptions{
		DefaultModels: func(string) []string { return catalog.ModelIDs() },
		ModelResolver: routing.RequestableResolver{
			GroupPolicies: modelConfigs,
			Warn:          slog.Warn,
		},
		GlobalWeights: func(ctx context.Context) (policy.ScoreWeights, error) {
			return scheduler.LoadValidationWeights(ctx, settings, *defaults)
		}, Mutate: store.Mutate,
	})
}

// provideGroupProbeRunner 注入时区、实例租约标识和探测执行器，cron 在生命周期 Start 时启动。
func provideGroupProbeRunner(repo routing.GroupAvailabilityProbeRepository, tests *provider.TestService, gateway *selection.Generic, openai *selection.Compatible, gemini *selection.Gemini, cfg *config.Config) *routing.GroupAvailabilityProbeRunnerService {
	location := time.Local
	if cfg != nil {
		if parsed, err := time.LoadLocation(cfg.Timezone); err == nil && parsed != nil {
			location = parsed
		}
	}
	executor := selection.NewProbe(tests, gateway, openai, gemini)
	return routing.NewGroupAvailabilityProbeRunnerService(repo, executor, routing.GroupProbeOptions{InstanceID: uuid.NewString(), Now: time.Now, Schedule: routingadapter.NewGroupProbeSchedule(location), Observe: func(format string, args ...any) {
		logging.LegacyPrintf("service.group_availability_probe", format, args...)
	}})
}
