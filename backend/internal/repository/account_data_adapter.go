// 本文件维护 repository 的所属能力；兼容入口复用唯一实现。
package repository

import (
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	accountcore "github.com/TokenFlux/TokenRouter/internal/account"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	egresspostgres "github.com/TokenFlux/TokenRouter/internal/egress/postgres"
	logger "github.com/TokenFlux/TokenRouter/internal/pkg/logger"
	accessview "github.com/TokenFlux/TokenRouter/internal/routing/accessview"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
)

// accountData 只为旧独立构造入口补齐值映射；完整应用传入同一个 AccountStore。
func (r *accountRepository) accountData() *accountpostgres.AccountStore {
	if r == nil {
		return nil
	}
	if r.data != nil {
		return r.data
	}
	return accountpostgres.NewAccountStore(r.client, r.sql, accountpostgres.AccountStoreOptions{
		Group: func(g *dbent.Group) *accessview.GroupConfig {
			return (*accessview.GroupConfig)(routingpostgres.GroupFromEnt(g))
		},
		Proxy: egresspostgres.ProxyEntity,
		OllamaIdentity: func(record *accountcore.Record) bool {
			return service.IsOllamaCloudUsageAccount(service.AccountFromRecord(record))
		},
		Events:  AccountEventBinding{Read: r.GetByID, ReadMany: r.GetByIDs, Cache: r.schedulerCache},
		Observe: func(format string, args ...any) { logger.LegacyPrintf("repository.account", format, args...) },
	})
}
func WrapAccountStore(data *accountpostgres.AccountStore, usage *billingpostgres.AccountUsageStore, client *dbent.Client, db *sql.DB, cache service.SchedulerCache) service.AdminAccountRepository {
	return &accountRepository{usage: usage, data: data, client: client, sql: db, schedulerCache: cache}
}
