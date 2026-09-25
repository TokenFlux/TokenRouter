// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
package routing_test

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

type marketplaceQuoteFixture struct {
	calculator *billing.Calculator
	resolver   *billing.PriceResolver
}

func (p marketplaceQuoteFixture) Quote(ctx context.Context, req routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	resolver := p.resolver
	if resolver == nil {
		resolver = billing.NewPriceResolver(nil, p.calculator, modelidentity.Identity, func(model string, err error) {
			slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
		})
	}
	return resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: req.Model, GroupID: &req.GroupID, Group: &billing.PriceGroup{ModelPricing: req.ModelPricing, LongContextPricingEnabled: req.LongContextPricingEnabled}}, RateMultiplier: req.RateMultiplier, FreeFastApplicable: req.FreeFastApplicable})
}
func (p marketplaceQuoteFixture) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}
