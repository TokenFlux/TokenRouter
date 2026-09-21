package provider

import (
	"github.com/TokenFlux/TokenRouter/internal/account"
	"github.com/TokenFlux/TokenRouter/internal/upstream/grok"
)

// NewGrokQuotaView 将供应商档位解释绑定到账号展示规则，不持有缓存或客户端。
func NewGrokQuotaView() *account.GrokQuotaView {
	return &account.GrokQuotaView{
		FreeTokenLimit:      grok.GrokFreeRolling24hTokenLimit,
		NeedsReauth:         account.GrokNeedsReauth,
		JWTSubscriptionTier: grok.SubscriptionTierFromJWT,
		CanonicalPlan:       grok.CanonicalGrokPlan,
		ParseTime:           account.ParseUsageTime,
	}
}
