package testkit

import (
	"context"
	"log/slog"
	"testing"
	"time"

	gatewayprovider "github.com/TokenFlux/TokenRouter/internal/gateway/provider"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"

	"github.com/TokenFlux/TokenRouter/internal/billing"

	billingpricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"

	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
	"github.com/TokenFlux/TokenRouter/internal/routing/capability"

	routingtestkit "github.com/TokenFlux/TokenRouter/internal/routing/testkit"
)

// ResolverCalculator 为区间用例提供独立的基础价输入。
func ResolverCalculator() *billing.Calculator {
	return Calculator(0, nil, ResolverFallbackPrices())
}

// ResolverFallbackPrices 每个夹具独立持有输入，避免从计算器读取私有状态。
func ResolverFallbackPrices() map[string]*billingpricing.ModelPricing {
	return map[string]*billingpricing.ModelPricing{"claude-sonnet-4": {
		InputPricePerToken:         3e-6,
		OutputPricePerToken:        15e-6,
		CacheCreationPricePerToken: 3.75e-6,
		CacheReadPricePerToken:     0.3e-6,
		SupportsCacheBreakdown:     false,
	}}
}

// ResolverWithCards 使用指定的基础定价构造共享价格配置解析器。
func ResolverWithCards(t *testing.T, bs *billing.Calculator, pricing []routing.ModelPricingEntry) *billing.PriceResolver {
	t.Helper()
	const groupID = 100
	platform := capability.PlatformAnthropic
	if len(pricing) > 0 && pricing[0].Platform != "" {
		platform = pricing[0].Platform
	}
	repo := &routingtestkit.PricingConfigRepositoryStub{
		ListAllFn: func(_ context.Context) ([]routingtestkit.Configuration, error) {
			return []routingtestkit.Configuration{{
				ID:           1,
				Name:         "test-price-config",
				Status:       billing.StatusActive,
				GroupIDs:     []int64{groupID},
				ModelPricing: pricing,
			}}, nil
		},
		GetGroupPlatformsFn: func(_ context.Context, _ []int64) (map[int64]string, error) {
			return map[int64]string{groupID: platform}, nil
		},
	}
	cs := routingtestkit.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{
		Warn: slog.
			Warn,
		Now: time.
			Now, LoadLocation: pricingprovider.
			LoadPricingLocation,
	},
	)
	return PriceResolver(cs, bs)
}

// GroupID 返回约定的测试分组编号。
func GroupID() *int64 { v := int64(100); return &v }

// PriceResolver 仅组合测试输入，测试直接使用原生解析器。
func PriceResolver(pricingConfigs *routing.PricingConfigService, calculator *billing.Calculator) *billing.PriceResolver {
	var source billing.ConfigPrices
	var stats billing.AccountStatsSource
	if pricingConfigs != nil {
		source = pricingConfigs
		stats = gatewayprovider.AccountStatsSource{Service: pricingConfigs}
	}
	return billing.NewPriceResolver(source, calculator, modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	}, stats)
}
