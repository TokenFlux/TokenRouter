package catalogue_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// catalogueRows 提供全量和按分组的可调度提供商查询。
type catalogueRows interface {
	ListSchedulable(context.Context) ([]provider.Record, error)
	ListSchedulableByGroupID(context.Context, int64) ([]provider.Record, error)
}

type modelsListProviderRepoStub struct {
	byGroup          map[int64][]provider.Record
	all              []provider.Record
	err              error
	listByGroupCalls atomic.Int64
	listAllCalls     atomic.Int64
}

type catalogueFixture struct {
	*routing.RequestableCatalogue
	prices *billing.PriceResolver
}

type providerStatsSource struct{ pricingConfigs *routing.PricingConfigService }

type cataloguePrices struct {
	resolver   *billing.PriceResolver
	calculator *billing.Calculator
}

func (s *modelsListProviderRepoStub) ListSchedulableByGroupID(_ context.Context, id int64) ([]provider.Record, error) {
	s.listByGroupCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	values, ok := s.byGroup[id]
	if !ok {
		return nil, nil
	}
	out := make([]provider.Record, len(values))
	copy(out, values)
	return out, nil
}

func (s *modelsListProviderRepoStub) ListSchedulable(context.Context) ([]provider.Record, error) {
	s.listAllCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]provider.Record, len(s.all))
	copy(out, s.all)
	return out, nil
}

// newCatalogueFixture 使用提供商查询、分组策略和价格解析器构造可请求模型目录。
func newCatalogueFixture(rows catalogueRows, pricingConfigs *routing.PricingConfigService, prices *billing.PriceResolver) *catalogueFixture {
	var read func(context.Context, *int64) ([]routing.CatalogueProvider, error)
	if rows != nil {
		read = func(ctx context.Context, id *int64) ([]routing.CatalogueProvider, error) {
			var values []provider.Record
			var err error
			if id != nil {
				values, err = rows.ListSchedulableByGroupID(ctx, *id)
			} else {
				values, err = rows.ListSchedulable(ctx)
			}
			if err != nil {
				return nil, err
			}
			// 按平台为缺少认证类型的测试提供商补齐默认类型。
			for index := range values {
				if values[index].Type == "" {
					switch values[index].Platform {
					case provider.PlatformQoder:
						values[index].Type = provider.ProviderTypeCosy
					case provider.PlatformAntigravity:
						values[index].Type = provider.ProviderTypeOAuth
					default:
						values[index].Type = provider.ProviderTypeAPIKey
					}
				}
			}
			return gatewayprovider.CatalogueProviders(values), nil
		}
	}
	var pricingConfigPort routing.CataloguePolicies
	if pricingConfigs != nil {
		pricingConfigPort = pricingConfigs
	}
	core := &routing.RequestableCatalogue{Read: read, Resolver: routing.RequestableResolver{GroupPolicies: pricingConfigPort, Warn: slog.Warn}, Warn: slog.Warn}
	return &catalogueFixture{RequestableCatalogue: core, prices: prices}
}

func (s providerStatsSource) ProviderStatsGroup(ctx context.Context, id int64) (*billing.ProviderStatsPricingConfig, error) {
	v, e := s.pricingConfigs.GetPricingConfigForGroup(ctx, id)
	if e != nil || v == nil {
		return nil, e
	}
	return &billing.ProviderStatsPricingConfig{Rules: v.ProviderStatsPricingRules}, nil
}

func cataloguePriceResolver(pricingConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ConfigPrices
	var stats billing.ProviderStatsSource
	if pricingConfigs != nil {
		source = pricingConfigs
		stats = providerStatsSource{pricingConfigs}
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
	}, stats)
}

func (p cataloguePrices) Quote(ctx context.Context, request routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	return p.resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: request.Model, GroupID: &request.GroupID}, RateMultiplier: request.RateMultiplier, FreeFastApplicable: request.FreeFastApplicable})
}

func (p cataloguePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

func newCatalogueMarketplace(groups routing.MarketplaceGroups, catalogue *catalogueFixture, calculator *billing.Calculator) *routing.Marketplace {
	var source routing.MarketplaceModels
	resolver := routing.RequestableResolver{Warn: slog.Warn}
	var priceSource routing.MarketplacePrices
	if catalogue != nil {
		source = catalogue.RequestableCatalogue
		resolver = catalogue.Resolver
	}
	if calculator != nil {
		var prices *billing.PriceResolver
		if catalogue != nil {
			prices = catalogue.prices
		}
		if prices == nil {
			prices = cataloguePriceResolver(nil, calculator)
		}
		priceSource = cataloguePrices{prices, calculator}
	}
	return routing.NewMarketplace(groups, nil, source, resolver, priceSource, nil, nil, routing.MarketplaceOptions{Now: time.Now, Warn: slog.Warn})
}
