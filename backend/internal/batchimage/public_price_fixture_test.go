//go:build unit

package batchimage_test

import (
	"context"
	"log/slog"
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	pricingprovider "github.com/TokenFlux/TokenRouter/internal/billing/provider"
	billingtestkit "github.com/TokenFlux/TokenRouter/internal/billing/testkit"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 模型配置与价卡夹具只提供原输入，编译和报价仍调用真实模块。
type publicPricingConfigFixture struct {
	routing.PricingConfigRepository
	pricingConfig routing.PricingConfig
	platforms     map[int64]string
	policy        routing.GroupRoutingPolicy
}

func (r *publicPricingConfigFixture) ListAll(context.Context) ([]routing.PricingConfig, error) {
	return []routing.PricingConfig{r.pricingConfig}, nil
}

func (r *publicPricingConfigFixture) GetGroupPlatforms(context.Context, []int64) (map[int64]string, error) {
	return r.platforms, nil
}

func makePublicPricingConfigFixture(pricingConfig routing.PricingConfig, platforms map[int64]string, policies ...routing.GroupRoutingPolicy) *publicPricingConfigFixture {
	policy := routing.GroupRoutingPolicy{}
	if len(policies) > 0 {
		policy = policies[0]
	}
	return &publicPricingConfigFixture{pricingConfig: pricingConfig, platforms: platforms, policy: policy}
}

func newPublicPricingConfigFixture(repo *publicPricingConfigFixture) *routing.PricingConfigService {
	return routing.NewPricingConfigService(repo, nil, routing.PricingConfigOptions{Warn: slog.Warn, Now: time.Now, LoadLocation: pricingprovider.LoadPricingLocation, ReadGroup: func(_ context.Context, id int64) (*routing.Group, error) {
		return &routing.Group{ID: id, Platform: repo.platforms[id], RoutingPolicy: repo.policy.Clone()}, nil
	}})
}

func publicPriceResolverFixture() *billing.PriceResolver {
	return billing.NewPriceResolver(nil, billingtestkit.Calculator(0, nil, nil), modelidentity.Identity, func(model string, err error) {
		slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
	})
}

func testImageModelPricing(prices map[string]*float64) []routing.ModelPricingEntry {
	card := routing.ModelPricingEntry{Models: []string{"*"}, BillingMode: routing.BillingModeImage}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ModelPricingEntry{card}
}
