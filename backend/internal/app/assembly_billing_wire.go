//go:build wireinject

package app

import (
	"github.com/TokenFlux/TokenRouter/internal/billing"

	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"

	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"

	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	identity "github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/google/wire"
)

// billing 模块的组合根登记；这里只分组原 provider，不创建资源或复制业务实现。
var billingAssemblyProviders = wire.NewSet(
	wire.Bind(new(identity.DefaultSubscriptionAssigner), new(*billing.SubscriptionService)),
	wire.Bind(new(completion.Store), new(*billingpostgres.SettlementStore)),
	provideBalanceNotifications,
	provideAccountUsage,
	provideGroupRateAdmin,
	provideBillingCalculator,
	provideBillingPriceResolver,
	provideSubscriptionExpiry,
	provideBillingPlans,
	wire.Bind(new(billinghttpapi.RedeemAdministrator), new(*billing.RedeemAdmin)),
	provideRedeemAdministration,
	provideBalanceAdjuster,
	provideBillingRedeem,
	providePlatformQuotas,
	provideQuotaHTTP,
	billing.NewQuotaCoordinator,
	provideBillingEligibility,
	provideWindowCostCache,
	providePlatformQuotaFlusher,
	provideBillingSubscriptions,
	provideSettlementStore,
	provideBillingFunds,
	providePricingService,
	billingredis.NewBillingCache,
	wire.Bind(new(billing.BillingCache), new(*billingredis.Cache)),
	providePlatformQuotaStore,
)
