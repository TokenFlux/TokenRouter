package billing

import (
	"context"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// PublicQuoteInput 是公开展示所需的价卡与倍率投影，FreeFastApplicable 由分组规则决定。
type PublicQuoteInput struct {
	PricingInput
	RateMultiplier     float64
	FreeFastApplicable bool
}

// PublicQuote 保留共享查价顺序；free Fast 只调整已支持该档位的展示副本。
func (r *PriceResolver) PublicQuote(ctx context.Context, input PublicQuoteInput) ModelDisplayPricing {
	resolved := r.Resolve(ctx, input.PricingInput)
	if input.FreeFastApplicable && pricing.ResolvedHasFastModeDisplayPricing(resolved) {
		cloned := *resolved
		standardMultiplier := 1.0
		pricing.ApplyPricingModifiers(&cloned, &ModelPricingEntry{FastMultiplier: &standardMultiplier})
		resolved = &cloned
	}
	return r.calculator.DisplayPricingWithResolvedMultipliers(input.Model, input.RateMultiplier, resolved)
}
