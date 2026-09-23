package app

import (
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	acctcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	"github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
)

// provideAccountStore 固定唯一账号存储，跨模块只注入值映射和原事件写入。
func provideAccountStore(client *dbent.Client, db *sql.DB, cache scheduler.SnapshotCache) *accountpostgres.AccountStore {
	store := accountpostgres.NewAccountStore(client, db, accountpostgres.AccountStoreOptions{
		Group: func(g *dbent.Group) *accessview.GroupConfig {
			return (*accessview.GroupConfig)(routingpostgres.GroupFromEnt(g))
		},
		OllamaIdentity: acctcore.IsOllamaCloudUsageAccount,
		Proxy:          egresspostgres.ProxyEntity, Now: time.Now, LoadLocation: time.LoadLocation,
		Observe: func(format string, args ...any) { logging.LegacyPrintf("repository.account", format, args...) },
	})
	store.SetEvents(newAccountEvents(store, cache))
	return store
}
func provideExecutionAccountStore(store *accountpostgres.AccountStore, usage *billingpostgres.AccountUsageStore) gatewayprovider.ExecutionAccountStore {
	return &executionAccountStore{data: store, usage: usage}
}
