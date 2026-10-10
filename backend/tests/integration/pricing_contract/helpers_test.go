package pricingcontract

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/config"
	"github.com/TokenFlux/TokenRouter/internal/gateway/completion"
	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	completiontestkit "github.com/TokenFlux/TokenRouter/internal/gateway/testkit"
	"github.com/TokenFlux/TokenRouter/internal/identity"
	"github.com/TokenFlux/TokenRouter/internal/modelcatalog/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/settings"
	usagecore "github.com/TokenFlux/TokenRouter/internal/usage"
)

type marketplaceFixtureGroups struct{ source routing.GroupRepository }

type marketplaceFixturePrices struct {
	calculator *billing.Calculator
	resolver   *billing.PriceResolver
}

// catalogFixture 保存测试提供的模型价格目录。
type catalogFixture struct {
	pricingData map[string]*pricing.CatalogModelPricing
}

// newCalculator 使用测试目录构造计价器。
func newCalculator(catalog *provider.Service) *billing.Calculator {
	return newCalculatorWithPrices(catalog, nil)
}

func newCalculatorWithPrices(catalog *provider.Service, prices map[string]*pricing.ModelPricing) *billing.Calculator {
	return testkit.Calculator(catalog, prices)
}

// contractProviderStatsCost 计算独立提供商成本，先匹配自定义规则，再查询模型默认价。
// 无可用成本价时返回 nil，由日志层计算回退成本。
// Qoder 自定义规则依次按请求模型、分组映射模型和最终上游模型匹配。
// totalCost 是测试调用参数，提供商成本由独立成本价计算。
func contractProviderStatsCost(
	ctx context.Context,
	pricingConfigService *routing.PricingConfigService,
	billingService *billing.Calculator,
	actualPlatform string,
	providerID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	tokens pricing.UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	return contractProviderStatsWithMapping(ctx, pricingConfigService, billingService, actualPlatform, providerID, groupID, upstreamModel, requestedModel, "", tokens, requestCount, totalCost, serviceTier, reasoningEfforts...)
}

// contractProviderStatsWithMapping 通过 billing 定价解析器计算提供商成本。
func contractProviderStatsWithMapping(
	ctx context.Context,
	pricingConfigService *routing.PricingConfigService,
	billingService *billing.Calculator,
	actualPlatform string,
	providerID int64,
	groupID int64,
	upstreamModel string,
	requestedModel string,
	groupMappedModel string,
	tokens pricing.UsageTokens,
	requestCount int,
	totalCost float64,
	serviceTier string,
	reasoningEfforts ...string,
) *float64 {
	effort := ""
	if len(reasoningEfforts) > 0 {
		effort = reasoningEfforts[0]
	}
	var source billing.ProviderStatsSource
	if pricingConfigService != nil {
		source = gatewayprovider.ProviderStatsSource{Service: pricingConfigService}
	}
	var calculator *billing.Calculator
	if billingService != nil {
		calculator = billingService
	}
	resolver := billing.NewPriceResolver(nil, calculator, nil, nil, source)
	return resolver.ResolveProviderStats(ctx, billing.ProviderStatsCostInput{PreferRequestedModel: actualPlatform == "qoder", ProviderID: providerID, GroupID: groupID, UpstreamModel: upstreamModel, RequestedModel: requestedModel, MappedModel: groupMappedModel, Tokens: tokens, RequestCount: requestCount, ServiceTier: serviceTier, ReasoningEffort: effort})
}

// newTestPricingConfigServiceForStats 为指定分组装配单个共享价格配置。
func newTestPricingConfigServiceForStats(t *testing.T, pricingConfig *routingtestkit.Configuration, groupID int64, platform string) *routing.PricingConfigService {
	t.Helper()
	cache := routingtestkit.NewModelConfigData()
	cache.ByGroup[groupID] = pricingConfig
	cache.Platforms[groupID] = platform

	cache.LoadedAt = time.Now()
	cs := routingtestkit.ModelConfigFromData(cache)
	return cs
}

// newPricingMarketplaceFixture 使用网关目录和 billing 报价接口构造市场服务。
func newPricingMarketplaceFixture(groupRepo routing.GroupRepository, settingRepo settings.Repository, resolver *billing.PriceResolver, billingService *billing.Calculator, capacityService *routing.CapacityService, availabilityRepo routing.GroupAvailabilityProbeRepository, cfg *config.Config) *routing.Marketplace {
	projection := routing.RequestableResolver{Warn: slog.Warn}
	source := &routing.RequestableCatalogue{Resolver: projection, Warn: slog.Warn}
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
	return routing.NewMarketplace(groups, settingRepo, source, projection, prices, capacity, availabilityRepo, routing.MarketplaceOptions{Timezone: timezone, Now: time.Now, Warn: slog.Warn})
}

func (g marketplaceFixtureGroups) ListActive(ctx context.Context) ([]routing.Group, error) {
	v, err := g.source.ListActive(ctx)
	if v == nil {
		return nil, err
	}
	out := make([]routing.Group, len(v))
	for i := range v {
		out[i] = *routing.CloneGroup(&v[i])
	}
	return out, err
}

func (p marketplaceFixturePrices) Quote(ctx context.Context, req routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	resolver := p.resolver
	if resolver == nil {
		resolver = billing.NewPriceResolver(nil, p.calculator, modelidentity.Identity, func(model string, err error) {
			slog.Debug("failed to get model pricing from model catalog, using fallback", "model", model, "error", err)
		})
	}
	return resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: req.Model, GroupID: &req.GroupID}, RateMultiplier: req.RateMultiplier, FreeFastApplicable: req.FreeFastApplicable})
}

func (p marketplaceFixturePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}

func newCatalogFixture(fixture catalogFixture) *provider.Service {
	return provider.NewServiceFromSnapshot(provider.Options{
		ModelLookupCandidates: modelidentity.CandidatesFactory,
	}, nil, provider.Snapshot{Data: fixture.pricingData})
}

// newOpenAIRecordUsageServiceForTest 使用测试存储构造 completion.Recorder 的记录夹具。
func newOpenAIRecordUsageServiceForTest(logs usagecore.UsageLogRepository, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, &completiontestkit.SettlementStore{}, rates, true)
}

func newOpenAIRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository, rates billing.UserGroupRateRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, rates, false)
}

func newGatewayRecordUsageServiceWithBillingRepoForTest(logs usagecore.UsageLogRepository, funds completion.Store, _ identity.UserRepository, _ billing.UserSubscriptionRepository) *completiontestkit.Recording {
	return completiontestkit.NewRecording(logs, funds, nil, false)
}

// testPtrFloat64 返回浮点数指针。
func testPtrFloat64(v float64) *float64 { return &v }

// testPtrInt 返回整数指针。
func testPtrInt(v int) *int { return &v }
