package app

import (
	"context"

	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"

	"github.com/TokenFlux/TokenRouter/internal/usage"

	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routinghttp "github.com/TokenFlux/TokenRouter/internal/routing/httpapi"
	routingdto "github.com/TokenFlux/TokenRouter/internal/routing/httpapi/dto"

	routingpostgres "github.com/TokenFlux/TokenRouter/internal/routing/postgres"
	"github.com/TokenFlux/TokenRouter/internal/settings"
)

// provideMarketplace 直接使用新分组、设置和报价能力；平台模型端口保留原动态来源。
func provideMarketplace(groups *routingpostgres.GroupStore, store *settings.Store, catalogue *routing.RequestableCatalogue, prices *billing.PriceResolver, calculator *billing.Calculator, capacity *routing.CapacityService, availability routing.GroupAvailabilityProbeRepository, cfg *config.Config) *routing.Marketplace {
	options := routing.MarketplaceOptions{Timezone: cfg.Timezone, Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames}
	return routing.NewMarketplace(groups, store, catalogue, catalogue.Resolver, marketplacePrices{prices, calculator}, capacity, availability, options)
}

type marketplacePrices struct {
	resolver   *billing.PriceResolver
	calculator *billing.Calculator
}

// marketplaceStats 仅投影公开首页需要的 Dashboard 计数。
type marketplaceStats struct{ source *usage.DashboardService }

func (s marketplaceStats) PublicStats(ctx context.Context) (routingdto.ModelMarketplaceStats, error) {
	value, err := s.source.GetPublicDashboardStats(ctx)
	if err != nil {
		return routingdto.ModelMarketplaceStats{}, err
	}
	return routingdto.ModelMarketplaceStats{TodayTokens: value.TodayTokens, TotalTokens: value.TotalTokens, TotalUsers: value.TotalUsers}, nil
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
func provideMarketplaceHTTP(core *routing.Marketplace, dashboard *usage.DashboardService) *routinghttp.MarketplaceHandler {
	return routinghttp.NewMarketplaceHandler(core, marketplaceStats{source: dashboard})
}
