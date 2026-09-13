package app

import (
	"database/sql"
	accountpostgres "github.com/TokenFlux/TokenRouter/internal/account/postgres"
	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
)

// provideAccountUsage 为账号管理和旧累计入口绑定唯一资金存储。
func provideAccountUsage(db *sql.DB, store *accountpostgres.AccountStore, cache service.SchedulerCache) *billingpostgres.AccountUsageStore {
	return billingpostgres.NewAccountUsageStore(db, legacybridge.AccountUsageEvents(store, cache, db))
}
