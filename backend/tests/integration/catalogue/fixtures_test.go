package catalogue_test

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingprovider "github.com/TokenFlux/TokenRouter/internal/routing/provider"
)

const modelRateLimitsKey = "model_rate_limits"

// catalogueRows 只提供原两种目录查询，保留计数及失败顺序断言。
type catalogueRows interface {
	ListSchedulable(context.Context) ([]account.Record, error)
	ListSchedulableByGroupID(context.Context, int64) ([]account.Record, error)
}
type modelsListAccountRepoStub struct {
	byGroup          map[int64][]account.Record
	all              []account.Record
	err              error
	listByGroupCalls atomic.Int64
	listAllCalls     atomic.Int64
}

func (s *modelsListAccountRepoStub) ListSchedulableByGroupID(_ context.Context, id int64) ([]account.Record, error) {
	s.listByGroupCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	values, ok := s.byGroup[id]
	if !ok {
		return nil, nil
	}
	out := make([]account.Record, len(values))
	copy(out, values)
	return out, nil
}

func (s *modelsListAccountRepoStub) ListSchedulable(context.Context) ([]account.Record, error) {
	s.listAllCalls.Add(1)
	if s.err != nil {
		return nil, s.err
	}
	out := make([]account.Record, len(s.all))
	copy(out, s.all)
	return out, nil
}

type catalogueFixture struct {
	*routing.RequestableCatalogue
	prices *billing.PriceResolver
}

// newCatalogueFixture 构造原无缓存手工装配，读取和资格均使用原生实现。
func newCatalogueFixture(rows catalogueRows, pricingConfigs *routing.PricingConfigService, prices *billing.PriceResolver) *catalogueFixture {
	var read func(context.Context, *int64) ([]routing.CatalogueAccount, error)
	if rows != nil {
		read = func(ctx context.Context, id *int64) ([]routing.CatalogueAccount, error) {
			var values []account.Record
			var err error
			if id != nil {
				values, err = rows.ListSchedulableByGroupID(ctx, *id)
			} else {
				values, err = rows.ListSchedulable(ctx)
			}
			if err != nil {
				return nil, err
			}
			return gatewayprovider.CatalogueAccounts(values), nil
		}
	}
	var pricingConfigPort routing.CataloguePolicies
	if pricingConfigs != nil {
		pricingConfigPort = pricingConfigs
	}
	core := &routing.RequestableCatalogue{Models: &routing.ModelList{Read: read}, Read: read, Resolver: routing.RequestableResolver{GroupPolicies: pricingConfigPort, Defaults: gatewayprovider.CatalogueDefaults(), Warn: slog.Warn}, Warn: slog.Warn}
	return &catalogueFixture{RequestableCatalogue: core, prices: prices}
}

type accountStatsSource struct{ pricingConfigs *routing.PricingConfigService }

func (s accountStatsSource) AccountStatsGroup(ctx context.Context, id int64) (*billing.AccountStatsPricingConfig, error) {
	v, e := s.pricingConfigs.GetPricingConfigForGroup(ctx, id)
	if e != nil || v == nil {
		return nil, e
	}
	return &billing.AccountStatsPricingConfig{Rules: v.AccountStatsPricingRules, ApplyUserPrice: v.ApplyPricingToAccountStats}, nil
}

func (s accountStatsSource) AccountStatsPlatform(ctx context.Context, id int64) billing.AccountStatsPlatform {
	v := s.pricingConfigs.GetGroupPlatform(ctx, id)
	return billing.AccountStatsPlatform{ID: v, PreferRequestedModel: v == routing.PlatformQoder}
}

func cataloguePriceResolver(pricingConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ConfigPrices
	var stats billing.AccountStatsSource
	if pricingConfigs != nil {
		source = pricingConfigs
		stats = accountStatsSource{pricingConfigs}
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, stats)
}

type cataloguePrices struct {
	resolver   *billing.PriceResolver
	calculator *billing.Calculator
}

func (p cataloguePrices) Quote(ctx context.Context, request routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	return p.resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: request.Model, GroupID: &request.GroupID, Group: &billing.PriceGroup{ModelPricing: request.ModelPricing, LongContextPricingEnabled: request.LongContextPricingEnabled}}, RateMultiplier: request.RateMultiplier, FreeFastApplicable: request.FreeFastApplicable})
}

func (p cataloguePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

func newCatalogueMarketplace(groups routing.MarketplaceGroups, catalogue *catalogueFixture, calculator *billing.Calculator) *routing.Marketplace {
	var source routing.MarketplaceModels
	resolver := routing.RequestableResolver{Defaults: gatewayprovider.CatalogueDefaults(), Warn: slog.Warn}
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
	return routing.NewMarketplace(groups, nil, source, resolver, priceSource, nil, nil, routing.MarketplaceOptions{Now: time.Now, Warn: slog.Warn, DefaultModels: routingprovider.MarketplaceModelDefs, DisplayNames: routingprovider.MarketplaceDisplayNames})
}
