//go:build unit

package service

import (
	"context"
	"log/slog"

	"github.com/TokenFlux/TokenRouter/internal/billing"
	"github.com/TokenFlux/TokenRouter/internal/creative"
	"github.com/TokenFlux/TokenRouter/internal/gateway/provider/modelidentity"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

func creativeGroupProjection(value *routing.Group) *creative.GroupView {
	if value == nil {
		return nil
	}
	return &creative.GroupView{ID: value.ID, Name: value.Name, Platform: value.Platform, IsExclusive: value.IsExclusive, AllowImageGeneration: value.AllowImageGeneration, Active: value.IsActive(), RateMultiplier: value.RateMultiplier, Operations: creative.OperationsForGroup(value.Platform, value.ResponsesImagePolicy != "" || value.ProtocolFallbacks != nil, value.AllowsClientProtocol), Price: billing.PriceGroup{ModelPricing: value.ModelPricing, LongContextPricingEnabled: value.LongContextPricingEnabled}}
}

// creativePriceFixture 只投影可选目录/解析器，价格算法与回退仍调用 billing。
func creativePriceFixture(calculator *billing.Calculator, resolver *billing.PriceResolver) func(context.Context, *creative.GroupView, string, string) (float64, bool) {
	return func(ctx context.Context, group *creative.GroupView, model, size string) (float64, bool) {
		if group == nil {
			return 0, false
		}
		selected := resolver
		if selected == nil && calculator != nil {
			selected = billing.NewPriceResolver(nil, calculator, modelidentity.Identity, func(model string, err error) {
				slog.Debug("failed to get model pricing from LiteLLM, using fallback", "model", model, "error", err)
			})
		}
		value, err := selected.ResolveImageUnitPrice(ctx, billing.PricingInput{Model: model, GroupID: &group.ID, Group: &group.Price}, size)
		return value, err == nil
	}
}
