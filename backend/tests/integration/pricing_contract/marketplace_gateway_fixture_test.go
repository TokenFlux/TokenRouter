//go:build unit

package pricingcontract

import (
	"log/slog"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// newPricingMarketplaceFixture 只装配网关目录与原生报价端口，不持有市场规则或缓存。
func newPricingMarketplaceFixture(groupRepo routing.GroupRepository, settingRepo settings.Repository, resolver *billing.PriceResolver, billingService *billing.Calculator, capacityService *routing.CapacityService, availabilityRepo routing.GroupAvailabilityProbeRepository, cfg *config.Config) *routing.Marketplace {
	projection := routing.RequestableResolver{Defaults: gatewayprovider.CatalogueDefaults(), Warn: slog.Warn}
	source := &routing.RequestableCatalogue{Models: &routing.ModelList{}, Resolver: projection, Warn: slog.Warn}
	var prices routing.MarketplacePrices
	if billingService != nil {
		prices = marketplaceFixturePrices{billingService, resolver}
	}
	timezone := ""
	if cfg != nil {
		timezone = cfg.Timezone
	}
	var groups routing.MarketplaceGroups
	if groupRepo != nil {
		groups = marketplaceFixtureGroups{groupRepo}
	}
	var capacity routing.MarketplaceCapacity
	if capacityService != nil {
		capacity = capacityService
	}
	return routing.NewMarketplace(groups, settingRepo, source, projection, prices, capacity, availabilityRepo, routing.MarketplaceOptions{Timezone: timezone, Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames})
}
