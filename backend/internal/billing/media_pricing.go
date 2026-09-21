package billing

import (
	"context"
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// ResolveImageUnitPrice 为创作台和批量图片解析固定单张价；token 价不作为单张价。
func (r *PriceResolver) ResolveImageUnitPrice(ctx context.Context, input PricingInput, size string) (float64, error) {
	if r == nil || r.calculator == nil || strings.TrimSpace(input.Model) == "" {
		return 0, pricing.ErrModelPricingUnavailable
	}
	resolved := r.Resolve(ctx, input)
	price, found := pricing.ConfiguredImageUnitPrice(resolved, size)
	if !found {
		// 缺少图片价时沿用内置回退，显式零价已在前面命中。
		price = r.calculator.DefaultImagePrice(input.Model, pricing.NormalizeImageBillingTierOrDefault(size))
	}
	return pricing.ValidateImageUnitPrice(price)
}
