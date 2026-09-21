package routing_test

import (
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
)

// newMarketplaceFixture 只绑定原价卡及目录输入，规则由 routing 和 billing 唯一实现。
func newMarketplaceFixture(groups routing.MarketplaceGroups, settings routing.MarketplaceSettings, calculator *billing.Calculator, resolver *billing.PriceResolver) *routing.Marketplace {
	var prices routing.MarketplacePrices
	if calculator != nil {
		prices = marketplaceQuoteFixture{calculator: calculator, resolver: resolver}
	}
	return routing.NewMarketplace(groups, settings, nil, routing.RequestableResolver{}, prices, nil, nil, routing.MarketplaceOptions{Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames})
}

func newMarketplaceCalculator(catalog *provider.PricingService, prices map[string]*pricing.ModelPricing) *billing.Calculator {
	return billingtestkit.Calculator(0, catalog, prices)
}

func NewModelPricingResolver(channels *routing.ChannelService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ChannelPrices
	if channels != nil {
		source = channels
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	})
}
