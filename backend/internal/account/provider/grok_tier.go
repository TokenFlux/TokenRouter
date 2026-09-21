// Grok 声明解析接入账号纯规则，不持有缓存或凭据副本。
package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

func GrokTierRules() account.GrokTierRules {
	return account.GrokTierRules{
		SubscriptionTierFromJWT:   grok.SubscriptionTierFromJWT,
		NormalizeSubscriptionTier: grok.NormalizeSubscriptionTier,
		IsFreeRollingTokenLimit:   grok.IsGrokFreeRolling24hTokenLimit,
	}
}
