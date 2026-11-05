package app

import (
	"context"
	"database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	keypostgres "github.com/TokenFlux/TokenRouter/internal/apikey/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/scheduler"
	"github.com/TokenFlux/TokenRouter/internal/settings/preaggregation"
	"github.com/TokenFlux/TokenRouter/internal/team"
	usagepostgres "github.com/TokenFlux/TokenRouter/internal/usage/postgres"
)

// apikey.go constructs API key storage and services with billing, routing,
// team, and concurrency dependencies.
// API key authentication needs group policy, user funding, and live concurrency
// information from other services. Application construction supplies those
// readers and cache settings before lifecycle startup creates authentication
// caches and subscriptions used by gateway requests.

// provideKeyStore binds bulk usage queries to the API key transaction store.
func provideKeyStore(client *dbent.Client, db *sql.DB, settings *preaggregation.PreAggregationSettingsService) *keypostgres.KeyStore {
	return keypostgres.NewKeyStore(client, db, func(ctx context.Context, ids []int64) (map[int64]float64, error) {
		return usagepostgres.ReadAPIKeyUsageTotals(ctx, db, settings, ids)
	})
}

// provideKeyRepository exposes the shared key store through the key repository interface.
func provideKeyRepository(keys *keypostgres.KeyStore) apikey.APIKeyRepository {
	return keys
}

// provideKeys constructs the key service with process cache settings and the
// application calendar. It connects billing invalidation, live concurrency, and
// team reads before the service starts its authentication cache lifecycle.
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
			L1Size:             cfg.APIKey.AuthCache.L1Size,
			L1TTLSeconds:       cfg.APIKey.AuthCache.L1TTLSeconds,
			L2TTLSeconds:       cfg.APIKey.AuthCache.L2TTLSeconds,
			NegativeTTLSeconds: cfg.APIKey.AuthCache.NegativeTTLSeconds,
			JitterPercent:      cfg.APIKey.AuthCache.JitterPercent,
			Singleflight:       cfg.APIKey.AuthCache.Singleflight,
			LookupConcurrency:  cfg.APIKey.LookupConcurrency,
			InvalidAbuse:       apikey.InvalidAuthAbuseConfig(cfg.APIKey.InvalidKeyAbuse),
		},
		GroupFastPolicy: keyGroupFastPolicy,
	}
	options.Default.APIKeyPrefix = cfg.APIKey.Prefix
	options.Team.Enabled = cfg.Team.Enabled
	core := apikey.NewAPIKeyService(keys, users, keyGroups{Repository: groups}, subs, rates, cache, options)
	core.SetRateLimitCacheInvalidator(billingCache)
	core.SetConcurrencyService(concurrency)
	core.SetTeamRepository(teams)
	return core
}

// provideKeyInvalidator exposes the assembled key service for authentication cache invalidation.
func provideKeyInvalidator(core *apikey.APIKeyService) apikey.APIKeyAuthCacheInvalidator {
	return core
}
