package service

import (
	"context"
	"fmt"
	"math"
	"strings"
)

// ResolveImageUnitPrice 为创作台和批量图片解析固定单张价。
// 这些任务按张预占和结算；token 价卡不能把每 token 单价当作每张价格。
func (r *ModelPricingResolver) ResolveImageUnitPrice(ctx context.Context, input PricingInput, size string) (float64, error) {
	if r == nil || r.billingService == nil || strings.TrimSpace(input.Model) == "" {
		return 0, ErrModelPricingUnavailable
	}
	resolved := r.Resolve(ctx, input)
	var price float64
	var found bool
	if resolved != nil && (resolved.Mode == BillingModeImage || resolved.Mode == BillingModePerRequest) {
		price, found = r.GetRequestTierPriceValue(resolved, strings.TrimSpace(size))
		if !found && resolved.channelPricing != nil && resolved.channelPricing.PerRequestPrice != nil {
			price, found = resolved.DefaultPerRequestPrice, true
		}
	}
	if !found {
		// 未匹配到图片价时保留内置按张回退，空默认价不能误作免费；显式零价已在上面命中。
		price = r.billingService.getDefaultImagePrice(input.Model, NormalizeImageBillingTierOrDefault(size))
	}
	if math.IsNaN(price) || math.IsInf(price, 0) || price < 0 {
		return 0, fmt.Errorf("invalid image unit price: %w", ErrModelPricingUnavailable)
	}
	return price, nil
}
