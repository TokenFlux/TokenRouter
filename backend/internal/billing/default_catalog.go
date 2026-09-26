package billing

import (
	"time"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// WithPriceCatalog 在独立目录快照上复用相同算法、平台规则和静态回退。
func (s *Calculator) WithPriceCatalog(catalog PriceCatalog) *Calculator {
	out := *s
	out.catalog = catalog
	now := s.options.Now()
	out.options.Now = func() time.Time { return now }
	return &out
}

// DefaultModelPrice 直接投影实际基础计费规则，显式零价仍是已定价。
func (s *Calculator) DefaultModelPrice(model, platform, mode string) pricing.DefaultModelPrice {
	result := pricing.DefaultModelPrice{Model: model, Platform: platform, BillingMode: "token", PriceStatus: "unpriced", Prices: []pricing.DefaultPriceValue{}}
	add := func(key string, value float64, unit string) {
		result.Prices = append(result.Prices, pricing.DefaultPriceValue{Key: key, Value: &value, Unit: unit})
	}
	raw := s.RawModelPricing(model)
	if mode == "video" {
		result.BillingMode = "video"
		for _, size := range []string{"480p", "720p", "1080p"} {
			add(size, s.DefaultVideoPrice(model, size), "USD/s")
		}
		result.PriceStatus = "priced"
		return result
	}
	imageModel := mode == "image" || pricing.HasExplicitImageGenerationPricing(raw) || pricing.LooksLikeImageModel(model)
	if imageModel {
		result.BillingMode = "image"
		for _, size := range []string{"1K", "2K", "4K"} {
			add(size, s.DefaultImagePrice(model, size), "USD/image")
		}
		result.PriceStatus = "priced"
	}
	base, err := s.GetModelPricing(model)
	if err != nil || base == nil {
		return result
	}
	result.PriceStatus = "priced"
	base = pricing.ApplyDeepSeekPeakPricing(model, base, s.options.Now())
	// 用一个计费单位复用实际算法，避免展示层重新实现 Fast、缓存和图片 token 回退。
	addTokenPrices := func(prefix, tier string) {
		quote := func(tokens UsageTokens) *CostBreakdown {
			return pricing.ComputeTokenBreakdown(base, tokens, 1, tier, false)
		}
		addOptional := func(key string, value float64, applicable bool) {
			if applicable {
				add(prefix+key, value*1e6, "USD/MTok")
			} else {
				result.Prices = append(result.Prices, pricing.DefaultPriceValue{Key: prefix + key, Unit: "USD/MTok"})
			}
		}
		add(prefix+"input", quote(UsageTokens{InputTokens: 1}).InputCost*1e6, "USD/MTok")
		add(prefix+"output", quote(UsageTokens{OutputTokens: 1}).OutputCost*1e6, "USD/MTok")
		readPresent := base.CacheReadPricePerToken > 0 || raw != nil && raw.CacheReadPricePresent
		writePresent := base.CacheCreationPricePerToken > 0 || base.SupportsCacheBreakdown || raw != nil && raw.CacheCreationPricePresent
		addOptional("cache_read", quote(UsageTokens{CacheReadTokens: 1}).CacheReadCost, readPresent)
		addOptional("cache_write", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation5mTokens: 1}).CacheCreationCost, writePresent)
		addOptional("cache_write_1h", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation1hTokens: 1}).CacheCreationCost, base.SupportsCacheBreakdown)
		addOptional("image_input", quote(UsageTokens{InputTokens: 1, ImageInputTokens: 1}).ImageInputCost, base.ImageInputPricePerToken > 0 || raw != nil && (raw.SupportsVision || raw.ImageInputPricePresent))
		addOptional("image_output", quote(UsageTokens{OutputTokens: 1, ImageOutputTokens: 1}).ImageOutputCost, base.ImageOutputPricePerToken > 0 || raw != nil && raw.ImageOutputPricePresent)
	}
	addTokenPrices("", "")
	if _, ok := pricing.FastModeDisplayPricing(base); ok {
		addTokenPrices("fast_", "priority")
	}
	if base.SupportsServiceTier {
		addTokenPrices("flex_", "flex")
	}
	if base.MaxReasoningEffortMultiplier != nil {
		add("max_reasoning", *base.MaxReasoningEffortMultiplier, "multiplier")
	}
	if base.LongContextInputThreshold > 0 {
		result.LongContextThreshold = base.LongContextInputThreshold
		result.LongContextThresholdInclusive = base.LongContextThresholdInclusive
		add("long_context_input", pricing.LongContextMultiplierOrOne(base.LongContextInputMultiplier), "multiplier")
		add("long_context_output", pricing.LongContextMultiplierOrOne(base.LongContextOutputMultiplier), "multiplier")
	}

	if pricing.IsDeepSeekModel(model) {
		add("peak", pricing.DeepseekPeakMultiplierAt(s.options.Now()), "multiplier")
	}
	return result
}
