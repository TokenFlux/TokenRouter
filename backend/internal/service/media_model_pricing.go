package service

import (
	"context"
	"strings"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// ResolveImageUnitPrice 为创作台和批量图片解析固定单张价。
// 这些任务按张预占和结算；token 价卡不能把每 token 单价当作每张价格。
func (r *ModelPricingResolver) ResolveImageUnitPrice(ctx context.Context, input PricingInput, size string) (float64, error) {
	if r == nil || r.billingService == nil || strings.TrimSpace(input.Model) == "" {
		return 0, ErrModelPricingUnavailable
	}
	resolved := r.Resolve(ctx, input)
	price, found := purepricing.ConfiguredImageUnitPrice(resolved, size)
	if !found {
		// 未匹配到图片价时保留内置按张回退，空默认价不能误作免费；显式零价已在上面命中。
		price = r.billingService.getDefaultImagePrice(input.Model, NormalizeImageBillingTierOrDefault(size))
	}
	return purepricing.ValidateImageUnitPrice(price)
}
