//go:build unit

package service

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// newGatewayMarketplaceFixture 只装配网关目录与原生报价端口，不持有市场规则或缓存。
func newGatewayMarketplaceFixture(groupRepo routing.GroupRepository, settingRepo settings.Repository, gatewayService *GatewayService, billingService *billing.Calculator, capacityService *routing.CapacityService, availabilityRepo routing.GroupAvailabilityProbeRepository, cfg *config.Config) *routing.Marketplace {
	var source routing.MarketplaceModels
	var prices routing.MarketplacePrices
	var resolver *billing.PriceResolver
	if gatewayService != nil {
		source = gatewayService.modelCatalogueCore()
		resolver = gatewayService.resolver
	}
	if billingService != nil {
		prices = legacyMarketplacePrices{billingService, resolver}
	}
	timezone := ""
	if cfg != nil {
		timezone = cfg.Timezone
	}
	var groups routing.MarketplaceGroups
	if groupRepo != nil {
		groups = legacyMarketplaceGroups{groupRepo}
	}
	var capacity routing.MarketplaceCapacity
	if capacityService != nil {
		capacity = capacityService
	}
	return routing.NewMarketplace(groups, settingRepo, source, gatewayService.requestableModelResolver(), prices, capacity, availabilityRepo, routing.MarketplaceOptions{Timezone: timezone, Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames})
}
