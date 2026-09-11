package pricing

// ResolveConfiguredPricing 应用显式价卡的区间、默认桶和倍率，不修改输入价卡。
func ResolveConfiguredPricing(config *ChannelModelPricing, base *ModelPricing, source string) *ResolvedPricing {
	mode := config.BillingMode
	if mode == "" {
		mode = BillingModeToken
	}
	resolved := &ResolvedPricing{Mode: mode, Source: source, ChannelPricing: config}
	if mode == BillingModePerRequest || mode == BillingModeImage || mode == BillingModeVideo {
		ApplyRequestTierOverrides(config, resolved)
		ApplyResolvedPriceMultiplier(resolved, config)
		return resolved
	}
	resolved.BasePricing = base
	resolved.SupportsCacheBreakdown = resolved.BasePricing != nil && resolved.BasePricing.SupportsCacheBreakdown
	resolved.SupportsServiceTier = resolved.BasePricing != nil && resolved.BasePricing.SupportsServiceTier
	ApplyTokenOverrides(config, resolved)
	ApplyResolvedPriceMultiplier(resolved, config)
	ApplyResolvedFastModeMultiplier(resolved, config)
	return resolved
}

// PriceCardOverrides 区分显式价格/区间与仅覆盖倍率的条目。
func PriceCardOverrides(card *ChannelModelPricing) bool {
	return card != nil && (HasExplicitPricingPrice(*card) || len(FilterValidTokenIntervals(card.Intervals)) > 0)
}

// SelectPriceCard 在已经投影的输入上固定分组优先于渠道的规则。
func SelectPriceCard(group, channel *ChannelModelPricing) *ChannelModelPricing {
	if PriceCardOverrides(group) {
		return group
	}
	if PriceCardOverrides(channel) {
		return channel
	}
	return nil
}

// PriceCardNeedsBase 判断是否需要读取 token 基础价，供旧适配保持按需查询。
func PriceCardNeedsBase(card *ChannelModelPricing) bool {
	return card == nil || (card.BillingMode != BillingModePerRequest && card.BillingMode != BillingModeImage && card.BillingMode != BillingModeVideo)
}

// ResolvePriceCards 统一价格来源优先级及纯倍率继承，不获取任何外部数据。
// @project-doc docs/domains/routing_and_billing.md#group_model_pricing
func ResolvePriceCards(group, channel *ChannelModelPricing, base *ModelPricing, baseSource string, longContextEnabled bool) *ResolvedPricing {
	var resolved *ResolvedPricing
	if PriceCardOverrides(group) {
		resolved = ResolveConfiguredPricing(group, base, PricingSourceGroup)
	} else {
		if PriceCardOverrides(channel) {
			resolved = ResolveConfiguredPricing(channel, base, PricingSourceChannel)
		} else {
			resolved = &ResolvedPricing{
				Mode:                   BillingModeToken,
				BasePricing:            base,
				Source:                 baseSource,
				SupportsCacheBreakdown: base != nil && base.SupportsCacheBreakdown,
				SupportsServiceTier:    base != nil && base.SupportsServiceTier,
			}
			ApplyPricingModifiers(resolved, channel)
		}
		ApplyPricingModifiers(resolved, group)
	}
	resolved.LongContextPricingEnabled = longContextEnabled
	return resolved
}
