package pricing

import (
	"strings"
)

// MatchPriceCard 在显式价卡列表中保留精确优先与首个通配规则。
func MatchPriceCard(cards []ChannelModelPricing, candidate string) *ChannelModelPricing {

	candidate = NormalizeChannelPricingModelName(candidate)
	var wildcard *ChannelModelPricing
	for i := range cards {
		entry := &cards[i]
		if !entry.HasEffectivePricing() {
			continue
		}
		for _, pattern := range entry.Models {
			normalized := NormalizeChannelPricingModelName(pattern)
			if normalized == candidate {
				cp := entry.Clone()
				return &cp
			}
			if strings.HasSuffix(normalized, "*") && strings.HasPrefix(candidate, strings.TrimSuffix(normalized, "*")) && wildcard == nil {
				cp := entry.Clone()
				wildcard = &cp
			}
		}
	}
	return wildcard
}
