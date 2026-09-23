//go:build unit

package service

import (
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
	"github.com/TokenFlux/TokenRouter/internal/billing/provider"
	"github.com/TokenFlux/TokenRouter/internal/routing"
)

// 本文件只保留原 unit 测试的私有入口，全部调用唯一的新实现；S04/S16 随测试归属清理。
// tryCustomRules 委托唯一账号统计定价规则。
func tryCustomRules(
	channel *routing.Channel, accountID, groupID int64,
	platform, model string, tokens purepricing.UsageTokens, requestCount int,
) *float64 {
	return purepricing.TryCustomRules(channel.AccountStatsPricingRules, accountID, groupID, platform, model, tokens, requestCount)
}

// matchAccountStatsRule 委托唯一账号统计定价规则。
func matchAccountStatsRule(rule *routing.AccountStatsPricingRule, accountID, groupID int64) bool {
	return purepricing.MatchAccountStatsRule(rule, accountID, groupID)
}

// findPricingForModel 委托唯一账号统计定价规则。
func findPricingForModel(pricingList []routing.ChannelModelPricing, platform, modelLower string) *routing.ChannelModelPricing {
	return purepricing.FindPricingForModel(pricingList, platform, modelLower)
}

// calculateStatsCost 委托唯一账号统计定价规则。
func calculateStatsCost(pricing *routing.ChannelModelPricing, tokens purepricing.UsageTokens, requestCount int) *float64 {
	return purepricing.CalculateStatsCost(pricing, tokens, requestCount)
}

func resolvedChannelTimeMultiplier(resolved *purepricing.ResolvedPricing, at time.Time) float64 {
	if resolved == nil || resolved.Mode != routing.BillingModeToken || resolved.ChannelPricing == nil {
		return 1
	}
	return channelTimeMultiplierAt(resolved.ChannelPricing.TimePricing, at)
}

const deepseekFlashOffPeakInputPrice = purepricing.DeepseekFlashOffPeakInputPrice

// channelTimeMultiplierAt 在兼容边界加载时区，纯算法只接收显式 Location。
func channelTimeMultiplierAt(config *routing.ChannelTimePricing, at time.Time) float64 {
	if config == nil || len(config.Periods) == 0 || at.IsZero() {
		return 1
	}
	location, err := provider.LoadPricingLocation(config.Timezone)
	if err != nil {
		return 1
	}
	return config.MultiplierAt(at, location)
}
