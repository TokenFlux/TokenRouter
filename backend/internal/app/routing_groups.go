// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"
	"database/sql"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/scheduler/policy"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	accountprovider "github.com/TokenFlux/TokenRouter/internal/account/provider"

	apikeypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"

	"github.com/TokenFlux/TokenRouter/internal/routing"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
)

// provideRoutingGroupStore 为新旧读取入口持有唯一存储及同连接参与工厂。
func provideRoutingGroupStore(client *dbent.Client, db *sql.DB) *routingpostgres.GroupStore {
	return routingpostgres.NewGroupStore(client, db, routingpostgres.GroupStoreOptions{
		Accounts: func(exec postgresinfra.Executor) routingpostgres.GroupLinkParticipant {
			return accountpostgres.GroupLinksInTx(exec)
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

func provideRoutingGroupAdmin(store *routingpostgres.GroupStore, accounts *accountpostgres.AccountStore, keys *apikeypostgres.KeyStore, invalidator apikey.APIKeyAuthCacheInvalidator, modelConfigs *routing.PricingConfigService, settings *settingscore.Store, defaults *scheduler.AdminDefaults) *routing.GroupAdmin {
	return routing.NewGroupAdmin(store, store, store, routingGroupAccounts{Store: accounts, Defaults: accountprovider.ModelDefaults()}, keys, invalidator, modelConfigs, routing.GroupAdminOptions{
		Pricing:       routing.PricingConfigValidation{LoadLocation: pricingprovider.LoadPricingLocation},
		DefaultModels: routingprovider.DefaultGroupModelCandidates, NormalizeMappedModel: gatewayprovider.NormalizeOpenAICompatRequestedModel,
		GlobalWeights: func(ctx context.Context) (policy.ScoreWeights, error) {
			return scheduler.LoadValidationWeights(ctx, settings, *defaults)
		}, Mutate: store.Mutate,
	})
}
