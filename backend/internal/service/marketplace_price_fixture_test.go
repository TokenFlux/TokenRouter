// 本文件维护 service 的所属能力；兼容入口复用唯一实现。
//go:build unit

package service

import (
	context "context"
	slog "log/slog"

	billing "github.com/TokenFlux/TokenRouter/internal/billing"
	pricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	routing "github.com/TokenFlux/TokenRouter/internal/routing"
)

type legacyMarketplaceGroups struct{ source routing.GroupRepository }

func (g legacyMarketplaceGroups) ListActive(ctx context.Context) ([]routing.Group, error) {
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

type legacyMarketplacePrices struct {
	calculator *billing.Calculator
	resolver   *billing.PriceResolver
}

func (p legacyMarketplacePrices) Quote(ctx context.Context, req routing.MarketplaceQuoteRequest) pricing.ModelDisplayPricing {
	resolver := p.resolver
	if resolver == nil {
		resolver = billing.NewPriceResolver(nil, p.calculator, modelidentity.Identity, func(model string, err error) {
			slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
		})
	}
	return resolver.PublicQuote(ctx, billing.PublicQuoteInput{PricingInput: billing.PricingInput{Model: req.Model, GroupID: &req.GroupID, Group: &billing.PriceGroup{ModelPricing: req.ModelPricing, LongContextPricingEnabled: req.LongContextPricingEnabled}}, RateMultiplier: req.RateMultiplier, FreeFastApplicable: req.FreeFastApplicable})
}
func (p legacyMarketplacePrices) GetModelModalities(model string) ([]string, []string) {
	return p.calculator.GetModelModalities(model)
}
