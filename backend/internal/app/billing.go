// 本文件由 billing 拥有资金契约与规则；旧入口仅作过渡适配。
package app

import (
	"context"
	sql "database/sql"
	"time"

	dbent "github.com/TokenFlux/TokenRouter/ent"
	legacybridge "github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	lifecycle "github.com/TokenFlux/TokenRouter/internal/app/lifecycle"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	config "github.com/TokenFlux/TokenRouter/internal/config"
	identitypostgres "github.com/TokenFlux/TokenRouter/internal/identity/postgres"
	logging "github.com/TokenFlux/TokenRouter/internal/infra/telemetry/logging"
	timingwheel "github.com/TokenFlux/TokenRouter/internal/infra/timingwheel"
	"github.com/TokenFlux/TokenRouter/internal/pkg/timezone"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	schedulerpostgres "github.com/TokenFlux/TokenRouter/internal/scheduler/postgres"
	service "github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/google/uuid"
)

func billingEligibilityOptions(c *config.Config) billing.EligibilityOptions {
	calendar := timezone.NewCalendar(timezone.Location())
	return billing.EligibilityOptions{Dates: billing.DateRuntime{Now: time.Now, Calendar: &calendar}, RunMode: c.RunMode, Billing: billing.BillingOptions{MinimumBalanceReserve: c.Billing.MinimumBalanceReserve, UserPlatformQuotaCacheTTLSeconds: c.Billing.UserPlatformQuotaCacheTTLSeconds, UserPlatformQuotaSentinelTTLSeconds: c.Billing.UserPlatformQuotaSentinelTTLSeconds, CircuitBreaker: billing.CircuitBreakerOptions{Enabled: c.Billing.CircuitBreaker.Enabled, FailureThreshold: c.Billing.CircuitBreaker.FailureThreshold, ResetTimeoutSeconds: c.Billing.CircuitBreaker.ResetTimeoutSeconds, HalfOpenRequests: c.Billing.CircuitBreaker.HalfOpenRequests}}, Database: billing.QuotaMirrorOptions{UserPlatformQuotaFlusherEnabled: c.Database.UserPlatformQuotaFlusherEnabled}}
}

// provideBillingEligibility 与管理及镜像写回共享同一个按用户协调器。
func provideBillingEligibility(cache billing.BillingCache, users *identitypostgres.UserStore, keys service.APIKeyRepository, quotas billing.UserPlatformQuotaRepository, cfg *config.Config, coordinator *billing.QuotaCoordinator, tasks *lifecycle.Tasks) *billing.Eligibility {
	options := billingEligibilityOptions(cfg)
	return billing.NewEligibility(cache, billingIdentityUsers{Repository: users}, keys, quotas, func() billing.EligibilityOptions { return options }, logging.LegacyPrintf, coordinator, func(name string, fn func()) { tasks.Go(name, fn) })
}
func provideLegacyBillingEligibility(core *billing.Eligibility, cfg *config.Config, rpm service.UserRPMCache, rates billing.UserGroupRateRepository) *service.BillingCacheService {
	return service.WrapBillingEligibility(core, cfg, rpm, rates)
}
func providePlatformQuotaFlusher(cfg *config.Config, cache billing.BillingCache, quotas billing.UserPlatformQuotaRepository, wheel *timingwheel.Wheel, coordinator *billing.QuotaCoordinator) *billing.UserPlatformQuotaUsageFlusher {
	options := billing.FlusherOptions{UserPlatformQuotaFlushBatchSize: cfg.Database.UserPlatformQuotaFlushBatchSize, UserPlatformQuotaFlushIntervalMs: cfg.Database.UserPlatformQuotaFlushIntervalMs, UserPlatformQuotaFlusherEnabled: cfg.Database.UserPlatformQuotaFlusherEnabled}
	return billing.NewUserPlatformQuotaUsageFlusher(options, cache, quotas, wheel, coordinator, logging.LegacyPrintf)
}
func provideBillingSubscriptions(groups *routingpostgres.GroupStore, repo billing.UserSubscriptionRepository, client *dbent.Client) *billing.SubscriptionService {
	calendar := timezone.NewCalendar(timezone.Location())
	return billing.NewSubscriptionService(billingGroups{Repository: groups}, repo, billingpostgres.NewSubscriptionMutations(client), billing.DateRuntime{Now: time.Now, Calendar: &calendar})
}
func provideSettlementStore(db *sql.DB) *billingpostgres.SettlementStore {
	return billingpostgres.NewSettlementStore(db, schedulerpostgres.EnqueueAccountQuotaChangedInTx)
}
func provideBillingFunds(store *billingpostgres.SettlementStore) *billing.Funds {
	return billing.NewFunds(store)
}

func provideBillingRedeem(repo billing.RedeemCodeRepository, users *identitypostgres.UserStore, subs *billing.SubscriptionService, cache billing.RedeemCache, eligibility *billing.Eligibility, client *dbent.Client, auth service.APIKeyAuthCacheInvalidator, affiliate *service.AffiliateService, tasks *lifecycle.Tasks) *billing.RedeemService {
	return billing.NewRedeemService(repo, billingIdentityUsers{Repository: users}, subs, cache, eligibility, billingpostgres.NewRedeemMutations(client, billingpostgres.RedeemWriters{Balances: billingpostgres.NewBalanceStore(client), Concurrency: identitypostgres.NewConcurrencyStore(client)}), auth, legacybridge.RedeemAffiliate{Service: affiliate}, billing.RedeemRuntime{Now: time.Now, Observe: logging.LegacyPrintf, Background: func(name string, fn func()) { tasks.Go(name, fn) }})
}

func provideRedeemAdministration(repo billing.RedeemCodeRepository, client *dbent.Client) *billing.RedeemAdmin {
	return billing.NewRedeemAdmin(repo, billingpostgres.NewRedeemAdministrationMutations(client), time.Now)
}
func provideBalanceAdjuster(client *dbent.Client) billing.BalanceAdjuster {
	return billingpostgres.NewBalanceStore(client)
}

func provideBillingPlans(client *dbent.Client) *billing.Plans {
	return billing.NewPlans(billingpostgres.NewPlanStore(client), legacybridge.PlanOrders{Client: client})
}

// provideSubscriptionExpiry 注入旧通知与锁策略，构造期间不启动后台任务。
func provideSubscriptionExpiry(repo billing.UserSubscriptionRepository, settings service.SettingRepository, notification *service.NotificationEmailService, lock service.LeaderLockCache, db *sql.DB) *billing.SubscriptionExpiryService {
	return billing.NewSubscriptionExpiryService(repo, billing.ExpiryOptions{Interval: time.Minute, Owner: uuid.NewString(), Now: time.Now, Observe: func(format string, args ...any) { logging.LegacyPrintf("service.subscription_expiry", format, args...) }, Settings: settings, Notifier: legacybridge.ExpiryNotifications{Service: notification}, Lease: func(ctx context.Context, key, owner string, ttl time.Duration) (func(), bool) {
		return legacybridge.BillingMaintenanceLease(ctx, lock, db, key, owner, ttl)
	}})
}
