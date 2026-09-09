package service

// 测试夹具通过模型价卡表达媒体价格，不再构造废弃的分组价格字段。
func testImageModelPricing(prices map[string]*float64) []ChannelModelPricing {
	return testMediaModelPricing(BillingModeImage, prices)
}

func testVideoModelPricing(prices map[string]*float64) []ChannelModelPricing {
	return testMediaModelPricing(BillingModeVideo, prices)
}

func testMediaModelPricing(mode BillingMode, prices map[string]*float64) []ChannelModelPricing {
	card := ChannelModelPricing{Models: []string{"*"}, BillingMode: mode}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []ChannelModelPricing{card}
}
