package pricing

// ApplyConfigPrice 在独立副本上应用价卡覆盖，保留 nil 图片价和倍率语义。
func ApplyConfigPrice(pricing *ModelPricing, configPricing *ModelPricingEntry) *ModelPricing {
	if configPricing == nil {
		return pricing
	}
	// 防止修改 fallbackPrices 中的共享指针
	cloned := *pricing
	pricing = &cloned
	ApplyConfigTokenPriceOverrides(pricing, configPricing)
	if configPricing.ImageOutputPrice != nil {
		pricing.ImageOutputPricePerToken = *configPricing.ImageOutputPrice
	} else {
		pricing.ImageOutputPricePerToken = 0
	}
	pricing.ImageOutputPriceExplicit = true
	ApplyConfigImageInputPrice(configPricing, pricing)
	multiplier, configured := NormalizedPriceMultiplier(configPricing)
	if configured {
		pricing = MultiplyModelPricing(pricing, multiplier)
	}
	ApplyConfigFastModeMultiplier(pricing, configPricing)
	ApplyConfigFlexMultiplier(pricing, configPricing)
	if configPricing.MaxReasoningEffortMultiplier != nil {
		pricing.MaxReasoningEffortMultiplier = configPricing.MaxReasoningEffortMultiplier
	}
	return pricing
}
