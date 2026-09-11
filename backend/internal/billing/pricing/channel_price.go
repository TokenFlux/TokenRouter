package pricing

// ApplyChannelPrice 在独立副本上应用渠道覆盖，保留 nil 图片价和倍率语义。
func ApplyChannelPrice(pricing *ModelPricing, channelPricing *ChannelModelPricing) *ModelPricing {
	if channelPricing == nil {
		return pricing
	}
	// 防止修改 fallbackPrices 中的共享指针
	cloned := *pricing
	pricing = &cloned
	ApplyChannelTokenPriceOverrides(pricing, channelPricing)
	if channelPricing.ImageOutputPrice != nil {
		pricing.ImageOutputPricePerToken = *channelPricing.ImageOutputPrice
	} else {
		pricing.ImageOutputPricePerToken = 0
	}
	pricing.ImageOutputPriceExplicit = true
	ApplyChannelImageInputPrice(channelPricing, pricing)
	multiplier, configured := NormalizedPriceMultiplier(channelPricing)
	if configured {
		pricing = MultiplyModelPricing(pricing, multiplier)
	}
	ApplyChannelFastModeMultiplier(pricing, channelPricing)
	ApplyChannelFlexMultiplier(pricing, channelPricing)
	if channelPricing.MaxReasoningEffortMultiplier != nil {
		pricing.MaxReasoningEffortMultiplier = channelPricing.MaxReasoningEffortMultiplier
	}
	return pricing
}
