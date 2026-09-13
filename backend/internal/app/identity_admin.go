// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	context "context"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	pagination "github.com/TokenFlux/TokenRouter/internal/pkg/pagination"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"time"
)

// identityAdminKeys 在新模块之间转换只读列表，保留原分页查询与排序。
type identityAdminKeys struct{ *keypostgres.KeyStore }

func (p identityAdminKeys) List(ctx context.Context, id int64, page, size int, sortBy, order string) ([]identity.AdminKeySummary, int64, error) {
	keys, pagination, e := p.ListByUserID(ctx, id, pagination.PaginationParams{Page: page, PageSize: size, SortBy: sortBy, SortOrder: order}, apikey.APIKeyListFilters{})
	if e != nil {
		return nil, 0, e
	}
	var out []identity.AdminKeySummary
	if keys != nil {
		out = make([]identity.AdminKeySummary, len(keys))
		for i, k := range keys {
			out[i] = identity.AdminKeySummary{ID: k.ID, Key: k.Key, GroupID: k.GroupID}
		}
	}
	if pagination == nil {
		return out, 0, nil
	}
	return out, pagination.Total, nil
}

// provideIdentityAdmin 固定同连接参与工厂，成功提交前不发布失效。
func provideIdentityAdmin(client *dbent.Client, users *identitypostgres.UserStore, keys *keypostgres.KeyStore, groups *routingpostgres.GroupStore, rates service.UserGroupRateRepository, rpm service.UserRPMCache, settings *service.SettingService, subs service.DefaultSubscriptionAssigner, balances billing.BalanceAdjuster, records *billing.RedeemAdmin, invalidator service.APIKeyAuthCacheInvalidator, cache *service.BillingCacheService, affiliates *service.AffiliateService, tasks *lifecycle.Tasks) *identity.UserAdmin {
	observe := identity.Observer{Log: logging.LegacyPrintf}
	transactions := &identitypostgres.AdminMutations{Client: client, Users: users, Keys: keys, KeysInTx: func(tx *dbent.Tx) identity.AdminKeyParticipant { return keys.LifecycleInTx(tx) }, Observer: observe}
	return identity.NewUserAdmin(identity.AdminDependencies{Now: time.Now, Users: users, Groups: identityAdminGroups{Repository: groups}, Keys: identityAdminKeys{keys}, Rates: rates, RPM: rpm, Settings: settings, Subscriptions: subs, Balances: balances, Records: records, Invalidator: invalidator, BalanceCache: cache, Affiliates: affiliates, Transactions: transactions, Observer: observe, Background: tasks.Go})
}
func provideKeyAdmin(client *dbent.Client, keys *keypostgres.KeyStore, users *identitypostgres.UserStore, groups *routingpostgres.GroupStore, invalidator service.APIKeyAuthCacheInvalidator, cache *service.BillingCacheService) *apikey.Admin {
	mutations := &keypostgres.AdminGroupMutations{Client: client, Keys: keys, Users: users, UsersInTx: func(tx *dbent.Tx) keypostgres.GroupAccessWriter { return identitypostgres.GroupAccessInTx(tx) }, Observer: logging.LegacyPrintf}
	return &apikey.Admin{Keys: keys, Users: users, Groups: keyGroups{Repository: groups}, Mutations: mutations, Invalidator: invalidator, RateLimits: cache}
}
