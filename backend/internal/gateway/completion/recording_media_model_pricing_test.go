package completion_test

import "github.com/TokenFlux/TokenRouter/internal/routing"

// 测试夹具通过模型价卡表达媒体价格，不再构造废弃的分组价格字段。
func testImageModelPricing(prices map[string]*float64) []routing.ChannelModelPricing {
	return testMediaModelPricing(routing.BillingModeImage, prices)
}

func testVideoModelPricing(prices map[string]*float64) []routing.ChannelModelPricing {
	return testMediaModelPricing(routing.BillingModeVideo, prices)
}

func testMediaModelPricing(mode routing.BillingMode, prices map[string]*float64) []routing.ChannelModelPricing {
	card := routing.ChannelModelPricing{Models: []string{"*"}, BillingMode: mode}
	for tier, price := range prices {
		card.Intervals = append(card.Intervals, routing.PricingInterval{TierLabel: tier, PerRequestPrice: price})
	}
	return []routing.ChannelModelPricing{card}
}
