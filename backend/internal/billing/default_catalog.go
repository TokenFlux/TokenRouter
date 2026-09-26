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
	// 长上下文报价需先越过阈值门槛才能触发倍率，与真实计费走同一条分支。
	longGateTokens := 0
	if base.LongContextInputThreshold > 0 {
		longGateTokens = base.LongContextInputThreshold
		if !base.LongContextThresholdInclusive {
			longGateTokens++
		}
	}
	addTokenPrices := func(prefix, tier string, applyLongCtx bool) {
		gate := 0
		if applyLongCtx {
			gate = longGateTokens
		}
		quote := func(tokens UsageTokens) *CostBreakdown {
			tokens.InputTokens += gate
			return pricing.ComputeTokenBreakdown(base, tokens, 1, tier, applyLongCtx)
		}
		perMTok := func(cost float64, tokens int) float64 {
			return cost / float64(tokens) * 1e6
		}
		addOptional := func(key string, value float64, applicable bool) {
			if applicable {
				add(prefix+key, value*1e6, "USD/MTok")
			} else {
				result.Prices = append(result.Prices, pricing.DefaultPriceValue{Key: prefix + key, Unit: "USD/MTok"})
			}
		}
		add(prefix+"input", perMTok(quote(UsageTokens{InputTokens: 1}).InputCost, 1+gate), "USD/MTok")
		add(prefix+"output", quote(UsageTokens{OutputTokens: 1}).OutputCost*1e6, "USD/MTok")
		readPresent := base.CacheReadPricePerToken > 0 || raw != nil && raw.CacheReadPricePresent
		writePresent := base.CacheCreationPricePerToken > 0 || base.SupportsCacheBreakdown || raw != nil && raw.CacheCreationPricePresent
		addOptional("cache_read", quote(UsageTokens{CacheReadTokens: 1}).CacheReadCost, readPresent)
		addOptional("cache_write", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation5mTokens: 1}).CacheCreationCost, writePresent)
		addOptional("cache_write_1h", quote(UsageTokens{CacheCreationTokens: 1, CacheCreation1hTokens: 1}).CacheCreationCost, base.SupportsCacheBreakdown)
		addOptional("image_input", quote(UsageTokens{InputTokens: 1, ImageInputTokens: 1}).ImageInputCost, base.ImageInputPricePerToken > 0 || raw != nil && (raw.SupportsVision || raw.ImageInputPricePresent))
		addOptional("image_output", quote(UsageTokens{OutputTokens: 1, ImageOutputTokens: 1}).ImageOutputCost, base.ImageOutputPricePerToken > 0 || raw != nil && raw.ImageOutputPricePresent)
	}
	addTokenPrices("", "", false)
	hasFast := false
	if _, ok := pricing.FastModeDisplayPricing(base); ok {
		addTokenPrices("fast_", "priority", false)
		hasFast = true
	}
	hasFlex := base.SupportsServiceTier
	if hasFlex {
		addTokenPrices("flex_", "flex", false)
	}
	if base.MaxReasoningEffortMultiplier != nil {
		add("max_reasoning", *base.MaxReasoningEffortMultiplier, "multiplier")
	}
	// 长上下文价格投影为应用倍率后的绝对单价（含 Fast/Flex 组合），口径与
	// ShouldApplySessionLongContextPricing 一致：任一倍率大于 1 才存在长上下文阶梯。
	if base.LongContextInputThreshold > 0 && (base.LongContextInputMultiplier > 1 || base.LongContextOutputMultiplier > 1) {
		result.LongContextThreshold = base.LongContextInputThreshold
		result.LongContextThresholdInclusive = base.LongContextThresholdInclusive
		addTokenPrices("long_", "", true)
		if hasFast {
			addTokenPrices("long_fast_", "priority", true)
		}
		if hasFlex {
			addTokenPrices("long_flex_", "flex", true)
		}
	}

	if pricing.IsDeepSeekModel(model) {
		add("peak", pricing.DeepseekPeakMultiplierAt(s.options.Now()), "multiplier")
	}
	return result
}
