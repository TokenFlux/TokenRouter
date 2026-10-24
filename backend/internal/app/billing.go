package app

import (
	"context"
	"database/sql"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	"github.com/TokenFlux/TokenRouter/internal/apikey"
	"github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	"github.com/TokenFlux/TokenRouter/internal/batchimage"
	batchpostgres "github.com/TokenFlux/TokenRouter/internal/batchimage/postgres"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingadapter "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	creativepostgres "github.com/TokenFlux/TokenRouter/internal/creative/postgres"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	"github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	catalogprovider "github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	notificationcore "github.com/TokenFlux/TokenRouter/internal/notification"
	paymentpostgres "github.com/TokenFlux/TokenRouter/internal/payment/postgres"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	"github.com/TokenFlux/TokenRouter/internal/promotion"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	settingscore "github.com/TokenFlux/TokenRouter/internal/settings"
)

// billing.go constructs billing admission, funding, subscription, redemption,
// and pricing services.
// Billing admission and settlement use the same application stores and
// calendar. Task funding participants receive the settlement transaction, and
// deferred redemption effects use the application task tracker so shutdown can
// wait before closing their dependencies.

// billingEligibilityOptions copies admission and circuit-breaker settings from
// c with the supplied calendar. The configuration is required; the resulting
// options retain that calendar for billing date calculations.
func billingEligibilityOptions(c *config.Config, calendar timezone.Calendar) billing.EligibilityOptions {
	return billing.EligibilityOptions{Dates: billing.DateRuntime{Now: time.Now, Calendar: &calendar}, Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.CircuitBreaker.Enabled, FailureThreshold: c.Billing.CircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.CircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.CircuitBreaker.HalfOpenRequests}}}
}

// provideBillingEligibility constructs admission using shared balance and Key
// caches. It captures options at assembly time and supplies tracked maintenance
// callbacks; runtime lifecycle hooks start and drain the service's cache
// workers separately.
func provideBillingEligibility(cache billing.BillingCache, users *identitypostgres.UserStore, keys apikey.APIKeyRepository, cfg *config.Config, tasks *lifecycle.Tasks, calendar timezone.Calendar) *billing.Eligibility {
	options := billingEligibilityOptions(cfg, calendar)
	return billing.NewEligibility(cache, billingIdentityUsers{Repository: users}, keys, func() billing.EligibilityOptions { return options }, logging.LegacyPrintf, func(name string, fn func()) { tasks.Go(name, fn) })
}

// provideBillingSubscriptions binds subscription mutations and localized group
// reads to the shared calendar.
func provideBillingSubscriptions(groups *routingpostgres.GroupStore, repo billing.UserSubscriptionRepository, client *dbent.Client, calendar timezone.Calendar) *billing.SubscriptionService {
	return billing.NewSubscriptionService(billingGroups{Repository: groups}, repo, billingpostgres.NewSubscriptionMutations(client), billing.DateRuntime{Now: time.Now, Calendar: &calendar})
}

// provideSettlementStore binds funds persistence and task participants to the
// application SQL pool. Creative and batch-image participants receive the
// transaction selected by SettlementStore, while provider quota changes use the
// same transactional outbox callback.
func provideSettlementStore(db *sql.DB, calendar timezone.Calendar) *billingpostgres.SettlementStore {
	return billingpostgres.NewSettlementStore(db, calendar, schedulerpostgres.EnqueueProviderQuotaChangedInTx, billingpostgres.TaskProjectionFactories{
		creative.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return creativepostgres.NewFundingParticipant(tx, ref.ID)
		},
		batchimage.FundingScope: func(tx *sql.Tx, ref billing.TaskReference) billingpostgres.TaskProjection {
			return batchpostgres.NewFundingParticipant(tx, ref.ID)
		},
	})
}

// provideBillingFunds exposes funding commands backed by the shared settlement
// store.
func provideBillingFunds(store *billingpostgres.SettlementStore) *billing.Funds {
	return billing.NewFunds(store)
}

// provideBillingRedeem assembles redemption, entitlement writers, paid-order
// lookup, and cache effects. Its stores use the shared Ent client, and its
// background callback registers work with tasks for application shutdown to
// await.
func provideBillingRedeem(repo billing.RedeemCodeRepository, users *identitypostgres.UserStore, subs *billing.SubscriptionService, cache billing.RedeemCache, eligibility *billing.Eligibility, client *dbent.Client, auth apikey.APIKeyAuthCacheInvalidator, affiliate *promotion.AffiliateService, tasks *lifecycle.Tasks) *billing.RedeemService {
	// Redemption mutations and paid-order lookup use stores built from the shared Ent client.
	mutations := billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{
		Balances:    billingpostgres.NewBalanceStore(client),
		Concurrency: identitypostgres.NewConcurrencyStore(client),
	})
	runtime := billing.RedeemRuntime{
		HasPaidOrder: paymentpostgres.NewOrderStore(client).HasPaidOrder,
		Now:          time.Now,
		Observe:      logging.LegacyPrintf,
		Background:   func(name string, fn func()) { tasks.Go(name, fn) },
	}
	return billing.NewRedeemService(repo, billingIdentityUsers{Repository: users}, subs, cache, eligibility, mutations, auth, affiliate, runtime)
}

// provideRedeemAdministration binds redemption management to Ent mutations and
// the system clock.
func provideRedeemAdministration(repo billing.RedeemCodeRepository, client *dbent.Client) *billing.RedeemAdmin {
	return billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(client), time.Now)
}

// provideBalanceAdjuster supplies the Ent-backed balance writer for billing
// consumers.
func provideBalanceAdjuster(client *dbent.Client) billing.BalanceAdjuster {
	return billingpostgres.NewBalanceStore(client)
}

// provideBillingPlans combines subscription-plan storage with payment-instance usage checks
// and the routing group lookup that confirms declared group selections.
func provideBillingPlans(client *dbent.Client, orders *paymentpostgres.InstanceStore, groups *routingpostgres.GroupStore) *billing.Plans {
	return billing.NewPlans(billingpostgres.NewPlanStore(client), orders, planGroupValidator{lookup: groups})
}

// provideSubscriptionExpiry constructs one-minute reminders using notification
// settings and singleton leases. The generated owner identifies this instance;
// the lease helper receives Redis and database acquisition capabilities, and
// lifecycle startup begins the reminder loop.
func provideSubscriptionExpiry(repo billing.UserSubscriptionRepository, settings settingscore.Repository, notification *notificationcore.NotificationEmailService, lock provider.CNMonitorLeader, db *sql.DB) *billing.SubscriptionExpiryService {
	return billing.NewSubscriptionExpiryService(repo, billing.ExpiryOptions{Interval: time.Minute, Owner: uuid.NewString(), Now: time.Now, Observe: func(format string, args ...any) { logging.LegacyPrintf("service.subscription_expiry", format, args...) }, Settings: settings, Notifier: expiryNotifications{Service: notification}, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return provider.AcquireSingletonLease(ctx, lock, databaseAdvisoryLease(db), key, owner, ttl)
	}})
}

// provideWindowCostCache creates the Redis-backed rolling billing cost cache.
func provideWindowCostCache(rdb *redis.Client) billing.WindowCostCache {
	return billingredis.NewWindowCostCache(rdb)
}

// provideBillingCalculator binds catalog pricing to the application calendar
// and location loader.
func provideBillingCalculator(catalog *catalogprovider.Service, calendar timezone.Calendar) *billing.Calculator {
	return billing.NewCalculator(catalog, billing.CalculatorOptions{Now: calendar.Now, LoadLocation: billingadapter.LoadPricingLocation})
}

// provideBillingPriceResolver combines group prices, platform model identity,
// and provider statistics. Catalog lookup failures are reported at debug level
// by the supplied callback, leaving price resolution decisions to
// billing.PriceResolver.
func provideBillingPriceResolver(modelConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	return billing.NewPriceResolver(modelConfigs, calculator, modelidentity.Identity, func(model string, err error) {
		slog.DebugContext(context.Background(), "model catalog pricing unavailable", "model", model, "error", err)
	}, gatewayprovider.ProviderStatsSource{Service: modelConfigs})
}
