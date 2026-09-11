//go:build unit

package service

import (
	"strings"
	"time"

	purepricing "github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

// 本文件只保留原 unit 测试的私有入口，全部调用唯一的新实现；S04/S16 随测试归属清理。
// tryCustomRules 委托唯一账号统计定价规则。
func tryCustomRules(
	channel *Channel, accountID, groupID int64,
	platform, model string, tokens UsageTokens, requestCount int,
) *float64 {
	return purepricing.TryCustomRules(channel.AccountStatsPricingRules, accountID, groupID, platform, model, tokens, requestCount)
}

// matchAccountStatsRule 委托唯一账号统计定价规则。
func matchAccountStatsRule(rule *AccountStatsPricingRule, accountID, groupID int64) bool {
	return purepricing.MatchAccountStatsRule(rule, accountID, groupID)
}

// findPricingForModel 委托唯一账号统计定价规则。
func findPricingForModel(pricingList []ChannelModelPricing, platform, modelLower string) *ChannelModelPricing {
	return purepricing.FindPricingForModel(pricingList, platform, modelLower)
}

// calculateStatsCost 委托唯一账号统计定价规则。
func calculateStatsCost(pricing *ChannelModelPricing, tokens UsageTokens, requestCount int) *float64 {
	return purepricing.CalculateStatsCost(pricing, tokens, requestCount)
}

// serviceTierCostMultiplier 委托纯定价实现，旧查询与配置投影保留在适配层。
func serviceTierCostMultiplier(serviceTier string) float64 {
	return purepricing.ServiceTierCostMultiplier(serviceTier)
}

func resolvedChannelTimeMultiplier(resolved *ResolvedPricing, at time.Time) float64 {
	if resolved == nil || resolved.Mode != BillingModeToken || resolved.ChannelPricing == nil {
		return 1
	}
	return channelTimeMultiplierAt(resolved.ChannelPricing.TimePricing, at)
}

const deepseekFlashOffPeakInputPrice = purepricing.DeepseekFlashOffPeakInputPrice

const deepseekFlashOffPeakOutputPrice = purepricing.DeepseekFlashOffPeakOutputPrice

const deepseekFlashOffPeakCacheRead = purepricing.DeepseekFlashOffPeakCacheRead

const deepseekProOffPeakInputPrice = purepricing.DeepseekProOffPeakInputPrice

const deepseekProOffPeakOutputPrice = purepricing.DeepseekProOffPeakOutputPrice

const deepseekProOffPeakCacheRead = purepricing.DeepseekProOffPeakCacheRead

// deepseekPeakMultiplierAt 委托纯定价实现，旧查询与配置投影保留在适配层。
func deepseekPeakMultiplierAt(now time.Time) float64 {
	return purepricing.DeepseekPeakMultiplierAt(now)
}

// getFallbackPricing 根据模型系列获取回退价格
func (s *BillingService) getFallbackPricing(model string) *ModelPricing {
	return purepricing.LookupFallbackPrice(s.fallbackPrices, model, pricingModelPolicy(strings.ToLower(model)))
}

// computeCacheCreationCost 委托纯定价实现，旧查询与配置投影保留在适配层。
func (s *BillingService) computeCacheCreationCost(pricing *ModelPricing, tokens UsageTokens, price, multiplier float64) float64 {
	return purepricing.ComputeCacheCreationCost(pricing, tokens, price, multiplier)
}

// normalizeCacheCreationBreakdown 委托纯定价实现，旧查询与配置投影保留在适配层。
func normalizeCacheCreationBreakdown(tokens UsageTokens) (int, int) {
	return purepricing.NormalizeCacheCreationBreakdown(tokens)
}

// applyLongContextDisplayMultipliers 委托纯定价实现，旧查询与配置投影保留在适配层。
func applyLongContextDisplayMultipliers(pricing *ModelPricing) *ModelPricing {
	return purepricing.ApplyLongContextDisplayMultipliers(pricing)
}

// channelTimeMultiplierAt 在兼容边界加载时区，纯算法只接收显式 Location。
func channelTimeMultiplierAt(config *ChannelTimePricing, at time.Time) float64 {
	if config == nil || len(config.Periods) == 0 || at.IsZero() {
		return 1
	}
	location, err := loadChannelTimePricingLocation(config.Timezone)
	if err != nil {
		return 1
	}
	return config.MultiplierAt(at, location)
}

// applyTokenOverrides 委托纯定价实现，旧查询与配置投影保留在适配层。
func (r *ModelPricingResolver) applyTokenOverrides(chPricing *ChannelModelPricing, resolved *ResolvedPricing) {
	purepricing.ApplyTokenOverrides(chPricing, resolved)
}

// filterValidTokenIntervals 委托纯定价实现，旧查询与配置投影保留在适配层。
func filterValidTokenIntervals(intervals []PricingInterval) []PricingInterval {
	return purepricing.FilterValidTokenIntervals(intervals)
}

// filterValidRequestIntervals 委托纯定价实现，旧查询与配置投影保留在适配层。
func filterValidRequestIntervals(intervals []PricingInterval) []PricingInterval {
	return purepricing.FilterValidRequestIntervals(intervals)
}

// intervalToModelPricing 委托纯定价实现，旧查询与配置投影保留在适配层。
func intervalToModelPricing(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ChannelModelPricing) *ModelPricing {
	return purepricing.IntervalToModelPricing(iv, supportsCacheBreakdown, chPricing)
}

// intervalToModelPricingWithBase 委托纯定价实现，旧查询与配置投影保留在适配层。
func intervalToModelPricingWithBase(iv *PricingInterval, supportsCacheBreakdown bool, chPricing *ChannelModelPricing, base *ModelPricing) *ModelPricing {
	return purepricing.IntervalToModelPricingWithBase(iv, supportsCacheBreakdown, chPricing, base)
}
