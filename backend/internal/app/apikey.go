// 本文件维护 app 的所属能力；兼容入口复用唯一实现。
package app

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"

	"database/sql"

	dbent "github.com/TokenFlux/TokenRouter/ent"

	"github.com/TokenFlux/TokenRouter/internal/apikey"

	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"

	"github.com/TokenFlux/TokenRouter/internal/config"

	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"

	"time"

	"github.com/TokenFlux/TokenRouter/internal/team"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// provideKeyStore 直接组合 Key 事务存储与 usage 批量统计，不改变查询形状。
func provideKeyStore(client *dbent.Client, db *sql.DB, settings *preaggregation.PreAggregationSettingsService) *keypostgres.KeyStore {
	return keypostgres.NewKeyStore(client, db, func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return usagepostgres.ReadAPIKeyUsageTotals(ctx, db, settings, ids)
	})
}
func provideKeyRepository(keys *keypostgres.KeyStore) apikey.APIKeyRepository {
	return keys
}

// provideKeys 在装配阶段注入路由策略与资金接口；构造不启动 L1 或订阅。
func provideKeys(
	keys *keypostgres.KeyStore,
	users *identitypostgres.UserStore,
	groups *routingpostgres.GroupStore,
	subs billing.UserSubscriptionRepository,
	rates billing.UserGroupRateRepository,
	cache apikey.APIKeyCache,
	cfg *config.Config,
	billingCache *billing.Eligibility,
	concurrency *scheduler.ConcurrencyService,
	teams team.TeamRepository,
	calendar timezone.Calendar,
) *apikey.APIKeyService {
	options := &apikey.Options{
		Now:      time.Now,
		Calendar: calendar,
		APIKeyAuth: apikey.APIKeyAuthCacheConfig{
			L1Size:             cfg.APIKeyAuth.L1Size,
			L1TTLSeconds:       cfg.APIKeyAuth.L1TTLSeconds,
			L2TTLSeconds:       cfg.APIKeyAuth.L2TTLSeconds,
			NegativeTTLSeconds: cfg.APIKeyAuth.NegativeTTLSeconds,
			JitterPercent:      cfg.APIKeyAuth.JitterPercent,
			Singleflight:       cfg.APIKeyAuth.Singleflight,
			LookupConcurrency:  cfg.APIKeyAuth.LookupConcurrency,
			InvalidAbuse:       apikey.InvalidAuthAbuseConfig(cfg.APIKeyAuth.InvalidAbuse),
		},
		GroupFastPolicy: keyGroupFastPolicy,
	}
	options.Default.APIKeyPrefix = cfg.Default.APIKeyPrefix
	options.Team.Enabled = cfg.Team.Enabled
	core := apikey.NewAPIKeyService(keys, users, keyGroups{Repository: groups}, subs, rates, cache, options)
	core.SetRateLimitCacheInvalidator(billingCache)
	core.SetConcurrencyService(concurrency)
	core.SetTeamRepository(teams)
	return core
}

func provideKeyInvalidator(core *apikey.APIKeyService) apikey.APIKeyAuthCacheInvalidator {
	return core
}
