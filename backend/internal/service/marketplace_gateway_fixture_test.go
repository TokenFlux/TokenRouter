//go:build unit

package service

import (
	"context"
	"log/slog"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"

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
		source = gatewayCatalogueForTest(gatewayService)
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
	return routing.NewMarketplace(groups, settingRepo, source, gatewayCatalogueResolverForTest(gatewayService), prices, capacity, availabilityRepo, routing.MarketplaceOptions{Timezone: timezone, Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames})
}

// 剩余结算/执行交叉测试只投影原行数据，目录规则和缓存由 routing 拥有。
func gatewayCatalogueForTest(gateway *GatewayService) *routing.RequestableCatalogue {
	if gateway == nil {
		return nil
	}
	var read func(context.Context, *int64) ([]routing.CatalogueAccount, error)
	if gateway.accountRepo != nil {
		read = func(ctx context.Context, id *int64) ([]routing.CatalogueAccount, error) {
			var values []gatewayprovider.ExecutionAccount
			var err error
			if id != nil {
				values, err = gateway.accountRepo.ListSchedulableByGroupID(ctx, *id)
			} else {
				values, err = gateway.accountRepo.ListSchedulable(ctx)
			}
			if err != nil || values == nil {
				return nil, err
			}
			out := make([]routing.CatalogueAccount, len(values))
			for i := range values {
				out[i] = gatewayprovider.CatalogueAccount(gatewayprovider.ExecutionRecord(&values[i]), values[i].Route)
			}
			return out, err
		}
	}
	return &routing.RequestableCatalogue{Models: &routing.ModelList{Read: read}, Read: read, Resolver: gatewayCatalogueResolverForTest(gateway), Warn: slog.Warn}
}
func gatewayCatalogueResolverForTest(gateway *GatewayService) routing.RequestableResolver {
	var channels routing.CatalogueChannels
	if gateway != nil && gateway.channelService != nil {
		channels = gateway.channelService
	}
	return routing.RequestableResolver{Channels: channels, Defaults: gatewayprovider.CatalogueDefaults(), Warn: slog.Warn}
}
