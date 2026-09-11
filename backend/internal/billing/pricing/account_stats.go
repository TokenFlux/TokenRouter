package pricing

import (
	"strings"
)

func UniqueNonEmptyAccountStatsModels(models []string) []string {
	out := make([]string, 0, len(models))
	seen := make(map[string]struct{}, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		key := strings.ToLower(model)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, model)
	}
	return out
}

// TryCustomRules 遍历自定义规则，按数组顺序先命中为准。
func TryCustomRules(
	rules []AccountStatsPricingRule, accountID, groupID int64,
	platform, model string, tokens UsageTokens, requestCount int,
) *float64 {
	modelLower := strings.ToLower(model)
	for _, rule := range rules {
		if !MatchAccountStatsRule(&rule, accountID, groupID) {
			continue
		}
		pricing := FindEffectivePricingForModel(rule.Pricing, platform, modelLower)
		if pricing == nil {
			continue // 规则匹配但模型不在规则定价中，继续下一条
		}
		// 自定义统计价是独立的最终成本基数，不继承用户侧的模型/推理倍率。
		if cost := CalculateStatsCost(pricing, tokens, requestCount); cost != nil {
			return cost
		}
	}
	return nil
}

// MatchAccountStatsRule 检查规则是否匹配指定的 accountID 和 groupID。
// 匹配条件：accountID ∈ rule.AccountIDs 或 groupID ∈ rule.GroupIDs。
// 如果规则的 AccountIDs 和 GroupIDs 都为空，视为不匹配。
func MatchAccountStatsRule(rule *AccountStatsPricingRule, accountID, groupID int64) bool {
	if len(rule.AccountIDs) == 0 && len(rule.GroupIDs) == 0 {
		return false
	}
	for _, id := range rule.AccountIDs {
		if id == accountID {
			return true
		}
	}
	for _, id := range rule.GroupIDs {
		if id == groupID {
			return true
		}
	}
	return false
}

// FindPricingForModel 在定价列表中查找匹配的模型定价。
// 先精确匹配，再通配符匹配（按配置顺序，先匹配先使用）。
//
//nolint:unused // 兼容旧测试入口；生产路径需要过滤空定价行并调用 FindEffectivePricingForModel。
func FindPricingForModel(pricingList []ChannelModelPricing, platform, modelLower string) *ChannelModelPricing {
	return FindPricingForModelByPredicate(pricingList, platform, modelLower, nil)
}

// FindEffectivePricingForModel 用于账号统计成本规则。
// 空定价行只是配置占位，不是成本规则；显式 0 指针仍视为有效，返回 0 成本覆盖。
func FindEffectivePricingForModel(pricingList []ChannelModelPricing, platform, modelLower string) *ChannelModelPricing {
	return FindPricingForModelByPredicate(pricingList, platform, modelLower, func(p *ChannelModelPricing) bool {
		return p != nil && p.HasEffectivePricing()
	})
}

func FindPricingForModelByPredicate(pricingList []ChannelModelPricing, platform, modelLower string, include func(*ChannelModelPricing) bool) *ChannelModelPricing {
	if include == nil {
		include = func(*ChannelModelPricing) bool { return true }
	}
	// 精确匹配优先
	for i := range pricingList {
		p := &pricingList[i]
		if !include(p) || !IsPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			if strings.ToLower(m) == modelLower {
				return p
			}
		}
	}
	// 通配符匹配：按配置顺序，先匹配先使用
	for i := range pricingList {
		p := &pricingList[i]
		if !include(p) || !IsPlatformMatch(platform, p.Platform) {
			continue
		}
		for _, m := range p.Models {
			ml := strings.ToLower(m)
			if !strings.HasSuffix(ml, "*") {
				continue
			}
			prefix := strings.TrimSuffix(ml, "*")
			if strings.HasPrefix(modelLower, prefix) {
				return p
			}
		}
	}
	return nil
}

// IsPlatformMatch 判断平台是否匹配（空平台视为不限平台）。
func IsPlatformMatch(queryPlatform, pricingPlatform string) bool {
	if queryPlatform == "" || pricingPlatform == "" {
		return true
	}
	return queryPlatform == pricingPlatform
}

// CalculateStatsCost 使用给定的定价计算费用，并在最后应用可选的定价倍率。
func CalculateStatsCost(pricing *ChannelModelPricing, tokens UsageTokens, requestCount int) *float64 {
	if pricing == nil {
		return nil
	}
	var cost *float64
	switch pricing.BillingMode {
	case BillingModePerRequest, BillingModeImage:
		cost = CalculatePerRequestStatsCost(pricing, requestCount)
	default:
		cost = CalculateTokenStatsCost(pricing, tokens)
	}
	if cost == nil {
		return nil
	}
	// 账号统计规则与实际渠道计费共用同一倍率语义。
	if multiplier, configured := NormalizedPriceMultiplier(pricing); configured {
		scaled := *cost * multiplier
		cost = &scaled
	}
	return cost
}

// CalculatePerRequestStatsCost 按次/图片计费。
func CalculatePerRequestStatsCost(pricing *ChannelModelPricing, requestCount int) *float64 {
	if pricing.PerRequestPrice == nil {
		return nil
	}
	if requestCount <= 0 {
		requestCount = 1
	}
	cost := *pricing.PerRequestPrice * float64(requestCount)
	if cost < 0 {
		return nil
	}
	return &cost
}

// CalculateTokenStatsCost Token 计费。
// If the pricing has intervals, find the matching interval by total token count
// and use its prices instead of the flat pricing fields.
func CalculateTokenStatsCost(pricing *ChannelModelPricing, tokens UsageTokens) *float64 {
	p := pricing
	if validIntervals := FilterValidTokenIntervals(pricing.Intervals); len(validIntervals) > 0 {
		totalTokens := tokens.InputTokens + tokens.OutputTokens + tokens.CacheCreationTokens + tokens.CacheReadTokens
		if iv := FindMatchingInterval(validIntervals, totalTokens); iv != nil {
			p = &ChannelModelPricing{
				InputPrice:        iv.InputPrice,
				OutputPrice:       iv.OutputPrice,
				CacheWritePrice:   iv.CacheWritePrice,
				CacheWrite1hPrice: iv.CacheWrite1hPrice,
				CacheReadPrice:    iv.CacheReadPrice,
				ImageOutputPrice:  pricing.ImageOutputPrice,
			}
		}
	}
	if !HasAnyTokenStatsPrice(p) || !HasAnyStatsTokenUsage(tokens) {
		return nil
	}
	deref := func(ptr *float64) float64 {
		if ptr == nil {
			return 0
		}
		return *ptr
	}
	cacheCreationCost := float64(tokens.CacheCreationTokens) * deref(p.CacheWritePrice)
	if p.CacheWrite1hPrice != nil {
		cache5m, cache1h := NormalizeCacheCreationBreakdown(tokens)
		if cache5m > 0 || cache1h > 0 {
			cacheCreationCost = float64(cache5m)*deref(p.CacheWritePrice) +
				float64(cache1h)*deref(p.CacheWrite1hPrice)
		}
	}
	cost := float64(tokens.InputTokens)*deref(p.InputPrice) +
		float64(tokens.OutputTokens)*deref(p.OutputPrice) +
		cacheCreationCost +
		float64(tokens.CacheReadTokens)*deref(p.CacheReadPrice) +
		float64(tokens.ImageOutputTokens)*deref(p.ImageOutputPrice)
	if cost < 0 {
		return nil
	}
	return &cost
}

func HasAnyTokenStatsPrice(pricing *ChannelModelPricing) bool {
	return pricing != nil && (pricing.InputPrice != nil ||
		pricing.OutputPrice != nil ||
		pricing.CacheWritePrice != nil ||
		pricing.CacheWrite1hPrice != nil ||
		pricing.CacheReadPrice != nil ||
		pricing.ImageOutputPrice != nil)
}

func HasAnyStatsTokenUsage(tokens UsageTokens) bool {
	return tokens.InputTokens > 0 ||
		tokens.OutputTokens > 0 ||
		tokens.CacheCreationTokens > 0 ||
		tokens.CacheReadTokens > 0 ||
		tokens.ImageOutputTokens > 0
}

// AccountStatsInput 是已查询渠道的只读投影，nil 成本与显式零价保持不同。
type AccountStatsInput struct {
	Rules              []AccountStatsPricingRule
	AccountID, GroupID int64
	Platform           string
	Models             []string
	Tokens             UsageTokens
	RequestCount       int
	UserTotalCost      float64
	ApplyUserPrice     bool
}

// ResolveAccountStatsOverride 返回 handled，区分明确不覆盖与继续查询模型目录。
func ResolveAccountStatsOverride(input AccountStatsInput) (*float64, bool) {
	for _, model := range input.Models {
		if cost := TryCustomRules(input.Rules, input.AccountID, input.GroupID, input.Platform, model, input.Tokens, input.RequestCount); cost != nil {
			return cost, true
		}
	}
	if input.ApplyUserPrice {
		cost := input.UserTotalCost
		if cost <= 0 {
			return nil, true
		}
		return &cost, true
	}
	return nil, false
}
