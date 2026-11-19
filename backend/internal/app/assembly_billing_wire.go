//go:build wireinject

package app

import (
	"github.com/google/wire"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	billinghttpapi "github.com/TokenFlux/TokenRouter/internal/billing/httpapi"
	billingpostgres "github.com/TokenFlux/TokenRouter/internal/billing/postgres"
	billingredis "github.com/TokenFlux/TokenRouter/internal/billing/rediscache"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	"github.com/TokenFlux/TokenRouter/internal/identity"
)

// assembly_billing_wire.go registers billing constructors and binds
// settlement, subscription, and cache implementations for their consumers.
// Request settlement and user subscriptions share billing services and storage
// selected by the application. Wire binds the settlement store for gateway
// completion and the subscription service for identity defaults, alongside the
// billing cache used by billing consumers.

// billingAssemblyProviders supplies billing services and binds their
// settlement, subscription, redeem, and cache implementations for consumers.
var billingAssemblyProviders = wire.NewSet(
	wire.Bind(new(identity.DefaultSubscriptionAssigner), new(*billing.SubscriptionService)),
	wire.Bind(new(completion.Store), new(*billingpostgres.SettlementStore)),
	provideBalanceNotifications,
	provideProviderUsage,
	provideGroupRateAdmin,
	provideBillingCalculator,
	provideBillingPriceResolver,
	provideSubscriptionExpiry,
	provideBillingPlans,
	wire.Bind(new(billinghttpapi.RedeemAdministrator), new(*billing.RedeemAdmin)),
	provideRedeemAdministration,
	provideBalanceAdjuster,
	provideBillingRedeem,
	provideKeyUsageStore,
	provideBillingEligibility,
	provideWindowCostCache,
	provideBillingSubscriptions,
	provideSettlementStore,
	provideBillingFunds,
	billingredis.NewBillingCache,
	wire.Bind(new(billing.BillingCache), new(*billingredis.Cache)),
)
