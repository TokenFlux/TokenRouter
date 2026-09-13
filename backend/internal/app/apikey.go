// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	sql "database/sql"
	dbent "github.com/TokenFlux/TokenRouter/ent"
	apikey "github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	repository "github.com/TokenFlux/TokenRouter/internal/repository"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	team "github.com/TokenFlux/TokenRouter/internal/team"
	"time"
)

// provideKeyStore 固定事务适配和旧用量查询投影，数据写入只有一份实现。
func provideKeyStore(client *dbent.Client, db *sql.DB, settings *service.PreAggregationSettingsService) *keypostgres.KeyStore {
	return keypostgres.NewKeyStore(client, db, legacybridge.KeyUsageTotals(db, settings))
}
func provideLegacyKeys(keys *keypostgres.KeyStore, client *dbent.Client, db *sql.DB, settings *service.PreAggregationSettingsService) service.APIKeyRepository {
	return repository.WrapKeyStore(keys, client, db, settings)
}

// provideKeys 在装配阶段注入路由策略与资金接口；构造不启动 L1 或订阅。
func provideKeys(keys *keypostgres.KeyStore, users *identitypostgres.UserStore, groups *routingpostgres.GroupStore, subs service.UserSubscriptionRepository, rates service.UserGroupRateRepository, cache apikey.APIKeyCache, cfg *config.Config, billingCache *service.BillingCacheService, concurrency *service.ConcurrencyService, teams team.TeamRepository) *apikey.APIKeyService {
	options := &apikey.Options{Now: time.Now, APIKeyAuth: apikey.APIKeyAuthCacheConfig{L1Size: cfg.APIKeyAuth.L1Size, L1TTLSeconds: cfg.APIKeyAuth.L1TTLSeconds, L2TTLSeconds: cfg.APIKeyAuth.L2TTLSeconds, NegativeTTLSeconds: cfg.APIKeyAuth.NegativeTTLSeconds, JitterPercent: cfg.APIKeyAuth.JitterPercent, Singleflight: cfg.APIKeyAuth.Singleflight, LookupConcurrency: cfg.APIKeyAuth.LookupConcurrency, InvalidAbuse: apikey.InvalidAuthAbuseConfig(cfg.APIKeyAuth.InvalidAbuse)}, GroupFastPolicy: keyGroupFastPolicy}
	options.Default.APIKeyPrefix = cfg.Default.APIKeyPrefix
	options.Team.Enabled = cfg.Team.Enabled
	core := apikey.NewAPIKeyService(keys, users, keyGroups{Repository: groups}, subs, rates, cache, options)
	core.SetRateLimitCacheInvalidator(billingCache)
	core.SetConcurrencyService(concurrency)
	core.SetTeamRepository(teams)
	return core
}
func provideLegacyKeyService(core *apikey.APIKeyService) *service.APIKeyService {
	return &service.APIKeyService{APIKeyService: core}
}
func provideKeyInvalidator(core *apikey.APIKeyService) service.APIKeyAuthCacheInvalidator {
	return core
}
