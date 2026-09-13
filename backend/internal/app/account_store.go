package app

import (
	"database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/repository"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// provideAccountStore 固定唯一账号存储，跨模块只注入值映射和原事件写入。
func provideAccountStore(client *dbent.Client, db *sql.DB, cache service.SchedulerCache) *accountpostgres.AccountStore {
	store := accountpostgres.NewAccountStore(client, db, accountpostgres.AccountStoreOptions{
		Group: func(g *dbent.Group) *accessview.GroupConfig {
			return (*accessview.GroupConfig)(routingpostgres.GroupFromEnt(g))
		},
		OllamaIdentity: acctcore.IsOllamaCloudUsageAccount,
		Proxy:          egresspostgres.ProxyEntity, Now: time.Now, LoadLocation: time.LoadLocation,
		Observe: func(format string, args ...any) { logging.LegacyPrintf("repository.account", format, args...) },
	})
	store.SetEvents(legacybridge.NewAccountEvents(store, cache))
	return store
}
func provideLegacyAccountStore(store *accountpostgres.AccountStore, usage *billingpostgres.AccountUsageStore, client *dbent.Client, db *sql.DB, cache service.SchedulerCache) service.AdminAccountRepository {
	return repository.WrapAccountStore(store, usage, client, db, cache)
}
func provideLegacyAccountReader(store service.AdminAccountRepository) service.AccountRepository {
	return store
}
