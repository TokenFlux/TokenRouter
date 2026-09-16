package completion

import (
	"strings"

	"github.com/TokenFlux/TokenRouter/internal/protocol"

	"github.com/TokenFlux/TokenRouter/internal/billing/pricing"
)

type ServiceTierBillingResolution struct {
	Requested  string // 请求发送的档位，空值表示未指定
	Observed   string // 上游响应声明的档位，空值表示未声明
	Billing    string // 实际用于计费和日志的档位
	Downgraded bool   // 计费档位是否低于请求档位
}

func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	resolution := ServiceTierBillingResolution{Requested: requested, Observed: observed, Billing: requested}
	if observed == "" || observed == requested {
		return resolution
	}
	observedRank, known := ServiceTierCostRank(observed)
	if !known {
		return resolution
	}
	requestedRank, _ := ServiceTierCostRank(requested)
	if observedRank >= requestedRank {
		return resolution
	}
	resolution.Billing = observed
	resolution.Downgraded = true
	return resolution
}
func ServiceTierCostRank(tier string) (rank int, known bool) {
	switch normalizeBillingServiceTier(tier) {
	case "flex":
		return 0, true
	case "", "default", "standard", "auto", "scale":
		return 1, true
	case "priority", "fast":
		return 2, true
	default:
		return 1, false
	}
}
func applyCacheOverride(usage *TokenUsage, target string) bool {
	projected := protocol.TokenUsage{CacheCreationInputTokens: usage.CacheCreationInputTokens, CacheCreation5mTokens: usage.CacheCreation5mTokens, CacheCreation1hTokens: usage.CacheCreation1hTokens}
	changed := protocol.ApplyCacheTTLOverride(&projected, target)
	usage.CacheCreation5mTokens = projected.CacheCreation5mTokens
	usage.CacheCreation1hTokens = projected.CacheCreation1hTokens
	return changed
}

// normalizeResult 只依据请求快照与实际观察降档，不查询平台或修改共享结果。
func (s *Recorder) normalizeResult(r *Result, a *AccountSnapshot, openAI bool, observedAccount *AccountSnapshot) {
	if r.ImageCount > 0 && (!openAI || r.VideoCount <= 0) {
		input := strings.TrimSpace(r.ImageInputSize)
		if input == "" && strings.TrimSpace(r.ImageSize) != pricing.ImageBillingSize2K {
			input = strings.TrimSpace(r.ImageSize)
		}
		sizes := r.ImageOutputSizes
		if len(sizes) == 0 && strings.TrimSpace(r.ImageOutputSize) != "" {
			sizes = []string{r.ImageOutputSize}
		}
		v := pricing.ResolveImageBillingSize(input, sizes)
		r.ImageSize, r.ImageInputSize, r.ImageOutputSize, r.ImageSizeSource, r.ImageSizeBreakdown = v.BillingSize, v.InputSize, v.OutputSize, v.Source, v.Breakdown
	}
	requested := stringValueOrEmpty(r.ServiceTier)
	if !openAI && requested == "" {
		requested = strings.TrimSpace(r.Usage.Speed)
	}
	resolution := ResolveBillingServiceTier(requested, r.UpstreamResponseServiceTier)
	if openAI {
		resolution = ResolveOpenAIServiceTierBilling(a != nil && a.OAuthLike, requested, r.UpstreamResponseServiceTier)
	}
	if resolution.Downgraded {
		tier := resolution.Billing
		r.ServiceTier = &tier
		if !openAI && r.Usage.Speed != "" {
			r.Usage.Speed = tier
		}
		event := BillingEvent{Kind: "tier_downgrade", Component: "service.gateway", RequestID: strings.TrimSpace(r.RequestID), RequestedTier: resolution.Requested, ObservedTier: resolution.Observed, BilledTier: resolution.Billing}
		if openAI {
			event.Component = "service.openai_gateway"
		}
		if observedAccount != nil {
			event.AccountID = observedAccount.ID
			event.Platform = observedAccount.Platform
		}
		s.observeEvent(event)
	}
}

// ResolveOpenAIServiceTierBilling 保留 OAuth default 回显的非权威性，其余响应只允许降档。
func ResolveOpenAIServiceTierBilling(oauthLike bool, requested, observed string) ServiceTierBillingResolution {
	if oauthLike && CodexOAuthResponseTierIsNonAuthoritative(observed) {
		return ServiceTierBillingResolution{Requested: normalizeBillingServiceTier(requested), Observed: normalizeBillingServiceTier(observed), Billing: normalizeBillingServiceTier(requested)}
	}
	return ResolveBillingServiceTier(requested, observed)
}
func CodexOAuthResponseTierIsNonAuthoritative(observed string) bool {
	return normalizeBillingServiceTier(observed) == "default"
}
