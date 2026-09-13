package app

import (
	"context"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/app/legacybridge"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/service"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideMarketplace 直接使用新分组、设置和报价能力；平台模型端口保留原动态来源。
func provideMarketplace(groups *routingpostgres.GroupStore, store *settings.Store, gateway *service.GatewayService, prices *service.ModelPricingResolver, calculator *billing.Calculator, capacity *routing.CapacityService, availability routing.GroupAvailabilityProbeRepository, cfg *config.Config) *routing.Marketplace {
	options := legacybridge.MarketplaceDefaults()
	options.Timezone = cfg.Timezone
	options.Now = time.Now
	options.Warn = slog.Warn
	return routing.NewMarketplace(groups, store, legacybridge.MarketplaceModels(gateway), legacybridge.RequestableModels(gateway), marketplacePrices{prices.PriceResolver, calculator}, capacity, availability, options)
}

type marketplacePrices struct {
	resolver   *billing.PriceResolver
	calculator *billing.Calculator
}

func (p marketplacePrices) Quote(ctx context.Context, request routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	return p.resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{
		Model: request.Model, GroupID: &request.GroupID,
		Group: &billing.PriceGroup{ModelPricing: request.ModelPricing, LongContextPricingEnabled: request.LongContextPricingEnabled},
	}, RateMultiplier: request.RateMultiplier, FreeFastApplicable: request.FreeFastApplicable})
}

func (p marketplacePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

// provideMarketplaceHTTP 绑定真实新 Handler 和旧公开统计的窄读取端口。
func provideMarketplaceHTTP(core *routing.Marketplace, dashboard *service.DashboardService) *routinghttp.MarketplaceHandler {
	return routinghttp.NewMarketplaceHandler(core, legacybridge.MarketplaceStats{Source: dashboard})
}
