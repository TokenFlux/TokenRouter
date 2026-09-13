// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	apikeypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	postgresinfra "github.com/TokenFlux/TokenRouter/internal/infra/postgres"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
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
			return repository.EnqueueSchedulerChange(ctx, exec, service.SchedulerOutboxEventGroupChanged, nil, id, nil)
		},
	})
}
func provideLegacyGroupStore(store *routingpostgres.GroupStore) service.AdminGroupRepository {
	return repository.WrapGroupStore(store)
}
func provideLegacyGroupReader(store service.AdminGroupRepository) service.GroupRepository {
	return store
}

func provideRoutingGroupAdmin(store *routingpostgres.GroupStore, accounts *accountpostgres.AccountStore, keys *apikeypostgres.KeyStore, invalidator service.APIKeyAuthCacheInvalidator, channels *routing.ChannelService, settings *service.SettingService) *routing.GroupAdmin {
	return routing.NewGroupAdmin(store, store, store, routingGroupAccounts{Store: accounts, Defaults: legacybridge.AccountAdminModelDefaults()}, keys, invalidator, channels, routing.GroupAdminOptions{
		Pricing:       routing.ChannelValidation{LoadLocation: pricingprovider.LoadPricingLocation},
		DefaultModels: legacybridge.GroupDefaultModels, NormalizeMappedModel: legacybridge.NormalizeGroupMappedModel,
		GlobalWeights: legacybridge.GroupValidationWeights(settings), Mutate: store.Mutate,
	})
}
