// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package service

import (
	context "context"
	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	pricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
	slog "log/slog"
	time "time"
)

type legacyMarketplaceGroups struct{ source GroupRepository }

func (g legacyMarketplaceGroups) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, err := g.source.ListActive(ctx)
	if v == nil {
		return nil, err
	}
	out := make([]routing.Group, len(v))
	for i := range v {
		out[i] = *RoutingGroupView(&v[i])
	}
	return out, err
}

type legacyMarketplaceModels struct{ gateway *GatewayService }

func (m legacyMarketplaceModels) Prefetch(ctx context.Context) ([]routing.CatalogueAccount, bool, error) {
	if m.gateway.accountRepo == nil {
		return nil, false, nil
	}
	v, err := m.gateway.accountRepo.ListSchedulable(ctx)
	return legacyCatalogueAccounts(m.gateway, v), err == nil, err
}
func (m legacyMarketplaceModels) ResolveRequestableModels(ctx context.Context, id *int64, platform string) routing.RequestableModelsResult {
	return m.gateway.ResolveRequestableModels(ctx, id, platform)
}

// LegacyMarketplaceModels 只将旧平台模型执行能力投影到新目录读取端口。
func LegacyMarketplaceModels(gateway *GatewayService) routing.MarketplaceModels {
	if gateway == nil {
		return nil
	}
	return legacyMarketplaceModels{gateway}
}
func LegacyRequestableResolver(gateway *GatewayService) routing.RequestableResolver {
	return gateway.requestableModelResolver()
}
func LegacyMarketplaceOptions(timezone string) routing.MarketplaceOptions {
	return routing.MarketplaceOptions{Timezone: timezone, Now: time.Now, Warn: slog.Warn, DefaultModels: defaultMarketplaceModelDefs, DisplayNames: marketplaceDisplayNameLookup}
}

type legacyMarketplacePrices struct {
	calculator *BillingService
	resolver   *ModelPricingResolver
}

func (p legacyMarketplacePrices) Quote(ctx context.Context, req routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	resolver := p.resolver
	if resolver == nil {
		resolver = NewModelPricingResolver(nil, p.calculator)
	}
	return resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: req.Model, GroupID: &req.GroupID, Group: &billing.PriceGroup{ModelPricing: req.ModelPricing, LongContextPricingEnabled: req.LongContextPricingEnabled}}, RateMultiplier: req.RateMultiplier, FreeFastApplicable: req.FreeFastApplicable})
}
func (p legacyMarketplacePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}
func (s *ModelMarketplaceService) marketplaceCore() *routing.Marketplace {
	if s.marketplace != nil {
		return s.marketplace
	}
	var source routing.MarketplaceModels
	var prices routing.MarketplacePrices
	var resolver *ModelPricingResolver
	if s.gatewayService != nil {
		source = LegacyMarketplaceModels(s.gatewayService)
		resolver = s.gatewayService.resolver
	}
	if s.billingService != nil {
		prices = legacyMarketplacePrices{s.billingService, resolver}
	}
	timezone := ""
	if s.cfg != nil {
		timezone = s.cfg.Timezone
	}
	var groups routing.MarketplaceGroups
	if s.groupRepo != nil {
		groups = legacyMarketplaceGroups{s.groupRepo}
	}
	var capacity routing.MarketplaceCapacity
	if s.capacityService != nil {
		capacity = s.capacityService
	}
	return routing.NewMarketplace(groups, s.settingRepo, source, s.gatewayService.requestableModelResolver(), prices, capacity, s.availabilityRepo, LegacyMarketplaceOptions(timezone))
}
func WrapModelMarketplace(core *routing.Marketplace) *ModelMarketplaceService {
	return &ModelMarketplaceService{marketplace: core}
}

func parseMarketplaceAvailabilityWindowSettings(settings map[string]string) (int, int) {
	return routing.ParseMarketplaceAvailabilityWindowSettings(settings)
}

// CoreMarketplace 供旧构造器转接；新装配直接使用 routing.Marketplace。
func (s *ModelMarketplaceService) CoreMarketplace() *routing.Marketplace { return s.marketplaceCore() }
